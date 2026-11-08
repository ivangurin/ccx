package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/pkg/jsonfile"
	"github.com/ivangurin/ccx/internal/pkg/strutil"
)

// DefaultDaemonPort is the default daemon port (fixed: settings.json points
// at it once; a port conflict is a comprehensible error + CCX_PORT).
const DefaultDaemonPort = 4567

// zaiProfileName is the display name of the profile imported from settings.json.
const zaiProfileName = "z.ai (GLM)"

// ErrProvidersExist means providers.json already exists; init without
// --force refuses (never silently overwrite user credentials).
var ErrProvidersExist = errors.New("config: providers.json already exists (use --force)")

// InitResult is the outcome of ccx init for display to the user.
type InitResult struct {
	Path     string
	Profiles []string // keys in creation order
	Warnings []string // things to fill in by hand
}

// InitProviders creates providers.json in the current schema (the map under
// "providers", unversioned, no daemon block — the port resolves via
// ResolveDaemonPort): import from settings.json/env.json + templates. It
// never touches settings.json; an existing file is replaced only with
// --force.
func InitProviders(cfg *Config, force bool) (*InitResult, error) {
	path := cfg.ProvidersPath()
	if _, err := os.Stat(path); err == nil && !force {
		return nil, ErrProvidersExist
	}

	providers, warnings := buildFromEnvironment(cfg)
	res := &InitResult{Path: path, Warnings: warnings}

	if err := SaveProviders(path, providers); err != nil {
		return nil, err
	}
	res.Profiles = append(res.Profiles, providers.Providers.Keys...)

	return res, nil
}

// buildFromEnvironment mirrors bash-init: zai from the settings.json env
// block, the work gateway from env.json (carry-extras), plus the
// openai/copilot/codex templates.
func buildFromEnvironment(cfg *Config) (*models.Providers, []string) {
	var warnings []string
	providers := &models.Providers{}

	zai := profileFromSettings(cfg.SettingsPath())
	if zai != nil {
		providers.Providers.Set("zai", zai)
	} else {
		providers.Providers.Set("zai", &models.Profile{Name: zaiProfileName, Protocol: models.ProtocolAnthropic, Env: map[string]string{}})
		warnings = append(warnings, "zai: ANTHROPIC_BASE_URL not found in settings.json — provider is empty, fill it in via ccx edit")
	}

	if work := profileFromEnvJSON(filepath.Join(cfg.Home, "env.json")); work != nil {
		providers.Providers.Set("work", work)
	}

	providers.Providers.Set("openai", builtinOpenAIProfile())
	providers.Providers.Set("copilot", builtinCopilotProfile())
	providers.Providers.Set("codex", builtinCodexProfile())

	return providers, warnings
}

// profileFromSettings builds a direct profile from the current settings.json
// env block; user keys (API_TIMEOUT_MS etc.) are NOT carried over — they
// stay in settings.json forever (bash semantics).
func profileFromSettings(path string) *models.Profile {
	var settings struct {
		Env map[string]string `json:"env"`
	}
	if err := jsonfile.ReadJSON(path, &settings); err != nil || settings.Env["ANTHROPIC_BASE_URL"] == "" {
		return nil
	}
	env := settings.Env
	primary := strutil.FirstNonEmpty(env["ANTHROPIC_DEFAULT_SONNET_MODEL"], env["ANTHROPIC_DEFAULT_OPUS_MODEL"], env["ANTHROPIC_DEFAULT_MODEL"])
	haiku := env["ANTHROPIC_DEFAULT_HAIKU_MODEL"]

	prof := &models.Profile{
		Name:     zaiProfileName,
		Protocol: models.ProtocolAnthropic,
		Env:      map[string]string{},
	}
	applyAnthropicEnv(prof, env)
	if primary != "" {
		prof.Models = append(prof.Models, models.Model{ID: primary, Label: primary, Default: true})
	}
	if haiku != "" && haiku != primary {
		prof.Models = append(prof.Models, models.Model{ID: haiku, Label: haiku})
	}

	return prof
}

// envJSONTail repairs a "truncated" env.json without wrapping braces
// (a stale copy-paste; parity with the bash version's try_read_env_block).
var envJSONTail = regexp.MustCompile(`,\s*}`)

func profileFromEnvJSON(path string) *models.Profile {
	raw, err := os.ReadFile(path) //nolint:gosec // path comes from the owning config, not user input
	if err != nil {
		return nil
	}
	content := strings.TrimSpace(string(raw))
	var env map[string]string
	if err := json.Unmarshal([]byte(content), &env); err != nil {
		wrapped := "{" + strings.TrimSuffix(content, ",") + "}"
		wrapped = envJSONTail.ReplaceAllString(wrapped, "}")
		if err := json.Unmarshal([]byte(wrapped), &env); err != nil {
			return nil
		}
	}
	if env["ANTHROPIC_BASE_URL"] == "" {
		return nil
	}

	prof := &models.Profile{
		Name:     "Work Gateway (Copilot)",
		Protocol: models.ProtocolAnthropic,
		Env:      dropAnthropicKeys(env),
	}
	applyAnthropicEnv(prof, env)

	return prof
}

// applyAnthropicEnv lifts direct anthropic credentials out of an env block
// (the settings.json/env.json import) into the flat profile fields.
func applyAnthropicEnv(prof *models.Profile, env map[string]string) {
	prof.BaseURL = env["ANTHROPIC_BASE_URL"]
	prof.AuthToken = env["ANTHROPIC_AUTH_TOKEN"]
	prof.APIKey = env["ANTHROPIC_API_KEY"]
}

func dropAnthropicKeys(env map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range env {
		if !strings.HasPrefix(k, "ANTHROPIC_") {
			out[k] = v
		}
	}

	return out
}
