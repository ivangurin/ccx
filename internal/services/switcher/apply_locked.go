package switcher

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/models"
)

func (s *Service) applyLocked(ctx context.Context, profileKey, modelArg string) (string, error) {
	providers, err := config.LoadProviders(s.cfg.ProvidersPath())
	if err != nil {
		// the init hint is only for a missing file: suggesting ccx init on a
		// corrupt one steers the user to --force, which replaces the file
		// and destroys their credentials — surface the real error instead
		if errors.Is(err, fs.ErrNotExist) {
			return "", errors.New("ccx: providers.json not found — run ccx init first")
		}

		return "", fmt.Errorf("ccx: providers.json is unreadable: %w", err)
	}
	profile := providers.Providers.Get(profileKey)
	if profile == nil {
		return "", fmt.Errorf("ccx: unknown provider: %s (see ccx list)", profileKey)
	}

	model, ok := profile.ResolveModelEntry(modelArg)
	if !ok {
		if modelArg == "" {
			return "", fmt.Errorf("ccx: provider %s has no default model — specify one: ccx %s <model>", profileKey, profileKey)
		}

		return "", fmt.Errorf("ccx: model %s not found in provider %s (see ccx list)", modelArg, profileKey)
	}

	// Credential fields may hold env references ($NAME / ${NAME}): expand
	// them BEFORE any side effect — a missing variable must fail the switch
	// here, not after the daemon restart (which would kill the live daemon
	// for nothing) and not as a literal "$NAME" in settings.json. providers
	// is never saved back, so the reference stays on disk.
	if err := config.ExpandCredentials(profile); err != nil {
		return "", fmt.Errorf("ccx: provider %s: %w — settings.json untouched, you stay on the previous provider", profileKey, err)
	}

	// An anthropic passthrough goes direct: the upstream speaks the native
	// protocol, so there is nothing to prepare (the anthropic translator's
	// Ready is trivial) — running the daemon would only add a hop.
	if profile.Protocol == models.ProtocolAnthropic {
		return s.applyDirect(ctx, profile, profileKey, model)
	}

	// Invariant: the restarted daemon is ready BEFORE the settings.json edit.
	// The daemon block is switcher-owned: the port resolves here, daemon.debug
	// comes from the same providers.json snapshot the route was built from —
	// both ride the spawn as flags; the daemon never reads the block.
	port := config.ResolveDaemonPort(s.cfg, 0)
	debug := providers.DaemonDebug()
	if err := s.daemon.Restart(ctx, profileKey, port, debug); err != nil {
		return "", s.switchAborted(ctx, providers, port, debug,
			fmt.Errorf("ccx: cancelled: daemon did not come up (%v)", err))
	}

	if err := s.writeSettings(daemonEnv(profile, model, port), model.ID, profile); err != nil {
		return "", s.switchAborted(ctx, providers, port, debug, err)
	}
	if err := s.writeState(profileKey, model.ID); err != nil {
		// no rollback here: the switch is already committed (the daemon and
		// settings.json agree on the new provider) — only state.json lags,
		// and respawning the previous daemon would contradict settings.json
		return "", err
	}

	return fmt.Sprintf("Switched: %s · model %s · %s", profile.Name, model.ID, baseURL(port)), nil
}

// switchAborted reports a failed switch and makes the "you stay on the
// previous provider" promise TRUE again: the failed Restart already killed
// the previous daemon (or a post-restart failure left the NEW daemon
// serving under an untouched settings.json that still names the old
// provider and its credential) — either way the previous provider must be
// serving again. The restore is best-effort: its failure lands in the
// error text together with the recovery command, never swallowed.
func (s *Service) switchAborted(
	ctx context.Context,
	providers *models.Providers,
	port int, debug bool,
	cause error,
) error {
	if rerr := s.rollbackDaemon(ctx, providers, port, debug); rerr != nil {
		return fmt.Errorf("%v — settings.json untouched; the previous daemon was NOT restored: %w", cause, rerr)
	}

	return fmt.Errorf("%v — settings.json untouched, you stay on the previous provider", cause)
}

// daemonEnv — the env block for settings.json: the daemon URL, the
// provider's own credential (Claude Code sends it; the daemon passes it
// through — auth_token, else the "ccx" placeholder: Claude Code expects a
// credential, the daemon ignores it for non-pass-through protocols) and the
// model pinning.
func daemonEnv(profile *models.Profile, model *models.Model, port int) map[string]string {
	env := map[string]string{}
	env["ANTHROPIC_BASE_URL"] = baseURL(port)
	env["ANTHROPIC_AUTH_TOKEN"] = credentialPlaceholder(profile)
	pinModel(env, profile, model)

	return env
}

// credentialPlaceholder — the profile's auth_token, or the "ccx" placeholder
// when it carries none (Claude Code insists on a credential value).
func credentialPlaceholder(profile *models.Profile) string {
	if profile.AuthToken != "" {
		return profile.AuthToken
	}

	return "ccx"
}
