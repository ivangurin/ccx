// Package switcher — profile switching: a port of bash-ccx apply_profile.
// Invariant: first readiness of everything the profile needs, only then the
// settings.json edit; on failure ⇒ files are untouched, the user stays on
// the previous profile. For translated protocols (openai/copilot/codex) that
// means a daemon restart with launch params (stop → spawn --provider/--port
// → health wait — /health answering means Ready() passed); an anthropic
// passthrough goes direct — the upstream URL and credential land in
// settings.json (bash-ccx semantics), the unused daemon is stopped
// best-effort.
package switcher

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/pkg/flock"
)

// Daemon — the daemon contract for switching (the implementation is an
// HTTP client + detached spawn; tests substitute a mock).
type Daemon interface {
	// Restart — stop any running daemon, spawn a fresh one with the launch
	// params and wait for readiness (health == translator Ready()).
	Restart(ctx context.Context, providerKey string, port int) error
	// Stop — best-effort shutdown of a running daemon (a direct anthropic
	// profile doesn't need it); a no-op success if it is not running.
	Stop(ctx context.Context) error
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
		return "", fmt.Errorf("ccx: failed to acquire the switcher lock: %w", err)
	}
	defer func() { _ = lock.Unlock() }()

	return s.applyLocked(ctx, profileKey, modelArg)
}

func lockPath(cfg *config.Config) string {
	return cfg.Home + "/.ccx.lock"
}

func (s *Switcher) applyLocked(ctx context.Context, profileKey, modelArg string) (string, error) {
	providers, err := config.LoadProviders(s.cfg.ProvidersPath())
	if err != nil {
		return "", fmt.Errorf("ccx: providers.json unavailable (%v) — run ccx init first", err)
	}
	profile := providers.Providers.Get(profileKey)
	if profile == nil {
		return "", fmt.Errorf("ccx: unknown profile: %s (see ccx list)", profileKey)
	}

	model, ok := profile.ResolveModel(modelArg)
	if !ok {
		if modelArg == "" {
			return "", fmt.Errorf("ccx: profile %s has no default model — specify one: ccx %s <model>", profileKey, profileKey)
		}

		return "", fmt.Errorf("ccx: model %s not found in profile %s (see ccx list)", modelArg, profileKey)
	}

	// An anthropic passthrough goes direct: the upstream speaks the native
	// protocol, so there is nothing to prepare (the anthropic translator's
	// Ready is trivial) — running the daemon would only add a hop.
	if profile.Protocol == models.ProtocolAnthropic {
		return s.applyDirect(ctx, providers, profile, profileKey, model)
	}

	// Invariant: the restarted daemon is ready BEFORE the settings.json edit.
	port := config.ResolveDaemonPort(s.cfg, 0)
	if err := s.daemon.Restart(ctx, profileKey, port); err != nil {
		return "", fmt.Errorf("ccx: cancelled: daemon did not come up (%v) — settings.json untouched, you stay on the previous profile", err)
	}

	if err := s.writeSettings(providers, daemonEnv(profile, model, port), model); err != nil {
		return "", err
	}
	if err := s.writeState(profileKey, model); err != nil {
		return "", err
	}

	return fmt.Sprintf("Switched: %s · model %s · %s", profile.Name, model, baseURL(port)), nil
}

// applyDirect — a direct anthropic profile: upstream URL + credential into
// settings.json, then a best-effort stop of the daemon (it is not needed
// anymore; a failure to stop must not fail the already-committed switch).
func (s *Switcher) applyDirect(
	ctx context.Context,
	providers *models.Providers,
	profile *models.Profile,
	profileKey, model string,
) (string, error) {
	env, err := directEnv(profile, model)
	if err != nil {
		return "", fmt.Errorf("%s — settings.json untouched, you stay on the previous profile", err)
	}

	if err := s.writeSettings(providers, env, model); err != nil {
		return "", err
	}
	if err := s.writeState(profileKey, model); err != nil {
		return "", err
	}

	out := fmt.Sprintf("Switched: %s · model %s · %s (direct)", profile.Name, model, profile.BaseURL)
	if err := s.daemon.Stop(ctx); err != nil {
		out += fmt.Sprintf("\nnote: the daemon is still running: %v (ccx stop)", err)
	}

	return out, nil
}

// directEnv — the env block for a direct anthropic profile: the upstream URL
// and its credential (proxy.go precedence: auth_token wins, api_key
// otherwise — exactly one credential var is written), extra_headers →
// ANTHROPIC_CUSTOM_HEADERS (Name: Value, newline-separated, key-sorted for
// deterministic output), plus the profile extras and model pinning.
func directEnv(profile *models.Profile, model string) (map[string]string, error) {
	if profile.BaseURL == "" {
		return nil, fmt.Errorf("ccx: profile %s has no base_url — add it to providers.json", profile.Name)
	}
	env := map[string]string{}
	for k, v := range profile.Env {
		env[k] = v
	}
	env["ANTHROPIC_BASE_URL"] = profile.BaseURL
	if profile.AuthToken != "" {
		env["ANTHROPIC_AUTH_TOKEN"] = profile.AuthToken
	} else if profile.APIKey != "" {
		env["ANTHROPIC_API_KEY"] = profile.APIKey
	}
	if len(profile.Headers) > 0 {
		parts := make([]string, 0, len(profile.Headers))
		for _, key := range slices.Sorted(maps.Keys(profile.Headers)) {
			parts = append(parts, key+": "+profile.Headers[key])
		}
		env["ANTHROPIC_CUSTOM_HEADERS"] = strings.Join(parts, "\n")
	}
	for _, key := range pinnedKeys {
		env[key] = model
	}

	return env, nil
}

// daemonEnv — the env block for settings.json: the daemon URL, the
// provider's own credential (Claude Code sends it; the daemon passes it
// through — direct-path precedence: auth_token > api_key, else a
// placeholder: Claude Code expects a credential, the daemon ignores it for
// non-pass-through protocols), the profile's extras and model pinning across
// all mappings (the haiku class points to the same model — Claude Code
// background tasks stay on the selected one).
func daemonEnv(profile *models.Profile, model string, port int) map[string]string {
	env := map[string]string{}
	for k, v := range profile.Env {
		env[k] = v
	}
	env["ANTHROPIC_BASE_URL"] = baseURL(port)
	switch {
	case profile.AuthToken != "":
		env["ANTHROPIC_AUTH_TOKEN"] = profile.AuthToken
	case profile.APIKey != "":
		env["ANTHROPIC_API_KEY"] = profile.APIKey
	default:
		env["ANTHROPIC_AUTH_TOKEN"] = "ccx"
	}
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

// writeSettings: backup → surgical env patch (managed keys) → model pinning.
// env is the fresh block content (daemonEnv or directEnv). The patcher edits
// the file's own text, so everything outside env/the pin — order, indent,
// user blocks — survives byte-for-byte; fresh keys land at the end of the
// env block.
func (s *Switcher) writeSettings(providers *models.Providers, env map[string]string, model string) error {
	settings, err := models.LoadSettings(s.cfg.SettingsPath())
	if err != nil {
		return fmt.Errorf("ccx: no %s — it is created by the first run of Claude Code", s.cfg.SettingsPath())
	}

	if err := backupSettings(s.cfg, settings); err != nil {
		return err
	}

	// managed: env keys of all profiles (past extras go away) + ANTHROPIC_*
	managed := map[string]bool{}
	for _, key := range providers.Providers.Keys {
		for k := range providers.Providers.Get(key).Env {
			managed[k] = true
		}
	}
	// stale = managed/ANTHROPIC_* keys that are present now but absent from
	// the fresh block; all ANTHROPIC_* are wiped, not only managed ones (past
	// profile keys and manual values must not survive a switch — parity with
	// bash with_entries). User keys are not touched at all.
	var stale []string
	for k := range settings.Env() {
		if _, keep := env[k]; keep {
			continue
		}
		if managed[k] || strings.HasPrefix(k, "ANTHROPIC_") {
			stale = append(stale, k)
		}
	}
	slices.Sort(stale)

	if err := settings.PatchEnv(stale, env); err != nil {
		return fmt.Errorf("ccx: %w", err)
	}
	if err := settings.SetModelPin(model); err != nil {
		return fmt.Errorf("ccx: %w", err)
	}
	if err := writeFileAtomic(s.cfg.SettingsPath(), settings.Bytes()); err != nil {
		return fmt.Errorf("ccx: writing settings.json: %w", err)
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
