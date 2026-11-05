// Package switcher — profile switching: a port of bash-ccx apply_profile.
// Invariant: first daemon readiness (Ensure + Use), only then the
// settings.json edit; on failure ⇒ files are untouched, the user stays on
// the previous profile.
package switcher

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ivangurin/ccms/internal/config"
	"github.com/ivangurin/ccms/internal/models"
	"github.com/ivangurin/ccms/internal/pkg/flock"
)

// Daemon — the daemon contract for switching (the implementation is an
// HTTP client to /admin/*; tests substitute a mock).
type Daemon interface {
	// Ensure — the daemon is alive (spawn if needed, wait for health).
	Ensure(ctx context.Context) error
	// Use — switch the daemon's translator to the profile (including the
	// Ready check).
	Use(ctx context.Context, profile, model string) error
}

// Switcher — applies a profile to settings.json under a lock.
type Switcher struct {
	cfg    *config.Config
	daemon Daemon
}

// New creates a Switcher.
func New(cfg *config.Config, daemon Daemon) *Switcher {
	return &Switcher{cfg: cfg, daemon: daemon}
}

// Apply switches the profile; modelArg is a name or id (empty = default).
// Returns the summary line for the user.
func (s *Switcher) Apply(ctx context.Context, profileKey, modelArg string) (string, error) {
	lock, err := flock.LockWait(lockPath(s.cfg), s.cfg.LockTimeout)
	if err != nil {
		return "", fmt.Errorf("ccms: failed to acquire the switcher lock: %w", err)
	}
	defer func() { _ = lock.Unlock() }()

	return s.applyLocked(ctx, profileKey, modelArg)
}

func lockPath(cfg *config.Config) string {
	return cfg.Home + "/.ccms.lock"
}

func (s *Switcher) applyLocked(ctx context.Context, profileKey, modelArg string) (string, error) {
	providers, err := config.LoadProviders(s.cfg.ProvidersPath())
	if err != nil {
		return "", fmt.Errorf("ccms: providers.json unavailable (%v) — run ccms init first", err)
	}
	profile := providers.Profiles.Get(profileKey)
	if profile == nil {
		return "", fmt.Errorf("ccms: unknown profile: %s (see ccms list)", profileKey)
	}

	model, ok := profile.ResolveModel(modelArg)
	if !ok {
		if modelArg == "" {
			return "", fmt.Errorf("ccms: profile %s has no default model — specify one: ccms %s <model>", profileKey, profileKey)
		}

		return "", fmt.Errorf("ccms: model %s not found in profile %s (see ccms list)", modelArg, profileKey)
	}

	// Invariant: the daemon is ready and the route is switched BEFORE the
	// settings.json edit.
	if err := s.daemon.Ensure(ctx); err != nil {
		return "", fmt.Errorf("ccms: cancelled: daemon did not come up (%v) — settings.json untouched, you stay on the previous profile", err)
	}
	if err := s.daemon.Use(ctx, profileKey, model); err != nil {
		return "", fmt.Errorf("ccms: cancelled: translator %s not ready (%v) — settings.json untouched, you stay on the previous profile", profileKey, err)
	}

	if err := s.writeSettings(providers, profile, model); err != nil {
		return "", err
	}
	if err := s.writeState(profileKey, model); err != nil {
		return "", err
	}

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", providers.Daemon.Port)

	return fmt.Sprintf("Switched: %s · model %s · %s", profile.Name, model, baseURL), nil
}

// daemonEnv — the env block for settings.json: the daemon URL/token + the
// profile's extras + model pinning across all mappings (the haiku class
// points to the same model — Claude Code background tasks stay on the
// selected one).
func daemonEnv(providers *models.Providers, profile *models.Profile, model string) map[string]string {
	env := map[string]string{}
	for k, v := range profile.Env {
		env[k] = v
	}
	env["ANTHROPIC_BASE_URL"] = fmt.Sprintf("http://127.0.0.1:%d", providers.Daemon.Port)
	env["ANTHROPIC_AUTH_TOKEN"] = providers.Daemon.AuthToken
	for _, key := range pinnedKeys {
		env[key] = model
	}

	return env
}

var pinnedKeys = []string{
	"ANTHROPIC_MODEL",
	"ANTHROPIC_DEFAULT_MODEL",
	"ANTHROPIC_DEFAULT_HAIKU_MODEL",
	"ANTHROPIC_DEFAULT_SONNET_MODEL",
	"ANTHROPIC_DEFAULT_OPUS_MODEL",
	"ANTHROPIC_DEFAULT_FABLE_MODEL",
	"ANTHROPIC_SMALL_FAST_MODEL",
}

// writeSettings: backup → atomic env edit (managed keys) → model pinning.
func (s *Switcher) writeSettings(providers *models.Providers, profile *models.Profile, model string) error {
	settings, err := models.LoadSettings(s.cfg.SettingsPath())
	if err != nil {
		return fmt.Errorf("ccms: no %s — it is created by the first run of Claude Code", s.cfg.SettingsPath())
	}

	if err := backupSettings(s.cfg, settings); err != nil {
		return err
	}

	// managed: env keys of all profiles (past extras go away) + ANTHROPIC_*
	managed := map[string]bool{}
	for _, key := range providers.Profiles.Keys {
		for k := range providers.Profiles.Get(key).Env {
			managed[k] = true
		}
	}
	env := settings.Env()
	fresh := map[string]string{}
	for k, v := range env {
		// all ANTHROPIC_* are wiped (not only managed ones): past profile keys
		// and manual values must not survive a switch (parity with bash
		// with_entries)
		if managed[k] || strings.HasPrefix(k, "ANTHROPIC_") {
			continue
		}
		fresh[k] = v
	}
	for k, v := range daemonEnv(providers, profile, model) {
		fresh[k] = v
	}
	settings.SetEnv(fresh)
	settings.SetModelPin(model)

	data, err := settings.Marshal()
	if err != nil {
		return fmt.Errorf("ccms: failed to render settings.json: %w", err)
	}
	if err := writeFileAtomic(s.cfg.SettingsPath(), data); err != nil {
		return fmt.Errorf("ccms: writing settings.json: %w", err)
	}

	return nil
}

func (s *Switcher) writeState(profileKey, model string) error {
	state := models.State{
		Profile: profileKey,
		Model:   model,
		Updated: time.Now().UTC().Format(time.RFC3339),
	}

	return writeStateFile(s.cfg.StatePath(), state)
}
