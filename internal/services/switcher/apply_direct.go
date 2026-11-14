package switcher

import (
	"context"
	"fmt"

	"github.com/ivangurin/ccx/internal/models"
)

// applyDirect — a direct anthropic profile: upstream URL + credential into
// settings.json, then a best-effort stop of the daemon (it is not needed
// anymore; a failure to stop must not fail the already-committed switch).
func (s *Service) applyDirect(
	ctx context.Context,
	profile *models.Profile,
	profileKey string,
	model *models.Model,
) (string, error) {
	env, err := directEnv(profile, model)
	if err != nil {
		return "", fmt.Errorf("%s — settings.json untouched, you stay on the previous provider", err)
	}

	if err := s.writeSettings(env, model.ID); err != nil {
		return "", err
	}
	if err := s.writeState(profileKey, model.ID); err != nil {
		return "", err
	}

	out := fmt.Sprintf("Switched: %s · model %s · %s (direct)", profile.Name, model.ID, profile.BaseURL)
	if err := s.daemon.Stop(ctx); err != nil {
		out += fmt.Sprintf("\nnote: the daemon is still running: %v (ccx stop)", err)
	}

	return out, nil
}

// directEnv — the env block for a direct anthropic profile: the upstream
// URL, its credential (auth_token — the only credential field; an empty one
// writes no credential var) and the model pinning.
func directEnv(profile *models.Profile, model *models.Model) (map[string]string, error) {
	if profile.BaseURL == "" {
		return nil, fmt.Errorf("ccx: provider %s has no base_url — add it to providers.json", profile.Name)
	}
	env := map[string]string{}
	env["ANTHROPIC_BASE_URL"] = profile.BaseURL
	if profile.AuthToken != "" {
		env["ANTHROPIC_AUTH_TOKEN"] = profile.AuthToken
	}
	pinModel(env, model)

	return env, nil
}
