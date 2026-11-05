package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/pkg/jsonfile"
)

// DefaultDaemonPort is the default daemon port (fixed: settings.json points
// at it once; a port conflict is a comprehensible error + CCX_PORT).
const DefaultDaemonPort = 4567

// zaiProfileName is the display name of the profile imported from settings.json.
const zaiProfileName = "z.ai (GLM)"

// ErrProvidersExist means providers.json already exists; init without
// --force refuses (bash-ccx parity: never silently overwrite user credentials).
var ErrProvidersExist = errors.New("config: providers.json already exists (use --force)")

// InitResult is the outcome of ccx init for display to the user.
type InitResult struct {
	Path     string
	Migrated bool     // converted v1 (proxy{}) → v2 (protocol)
	Profiles []string // keys in creation order
	Warnings []string // things to fill in by hand
}

// InitProviders creates providers.json v2: v1 migration or import from
// settings.json/env.json + templates. It never touches settings.json.
// An existing v1 file is migrated without --force (that is the upgrade path);
// refusal happens only if it is already v2.
func InitProviders(cfg *Config, force bool) (*InitResult, error) {
	path := cfg.ProvidersPath()
	res := &InitResult{Path: path}
	var providers *models.Providers

	if raw, err := os.ReadFile(path); err == nil && looksLikeV1(raw) { //nolint:gosec // path comes from the owning config
		providers, res.Warnings = migrateV1(raw)
		res.Migrated = true
	} else {
		if err == nil && !force {
			return nil, ErrProvidersExist
		}
		providers, res.Warnings = buildFromEnvironment(cfg)
	}

	providers.Version = 2
	providers.Daemon.Port = DefaultDaemonPort
	if cfg.DaemonPort != 0 {
		providers.Daemon.Port = cfg.DaemonPort
	}
	token, err := generateToken()
	if err != nil {
		return nil, fmt.Errorf("config: generate token: %w", err)
	}
	providers.Daemon.AuthToken = token

	if err := SaveProviders(path, providers); err != nil {
		return nil, err
	}
	res.Profiles = append(res.Profiles, providers.Profiles.Keys...)

	return res, nil
}

func generateToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}

	return hex.EncodeToString(buf), nil
}

// --- v1 (bash-ccx) ---

type v1File struct {
	Profiles models.OrderedMap[*v1Profile] `json:"profiles"`
}

type v1Profile struct {
	Name   string            `json:"name"`
	Env    map[string]string `json:"env"`
	Models []models.Model    `json:"models"`
	Proxy  *v1Proxy          `json:"proxy"`
}

type v1Proxy struct {
	Start      string `json:"start"`
	Health     string `json:"health"`
	Stop       string `json:"stop"`
	StopPattrn string `json:"stop_pattern"`
}

// looksLikeV1 checks for a file without "version": 2 (v1 never wrote it).
func looksLikeV1(raw []byte) bool {
	var probe struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return false
	}

	return probe.Version != 2
}

// migrateV1 converts a v1 file by the rules: proxy.start naming an external
// translator (ccr/copilot-api/claude-codex) → the matching protocol;
// no proxy → direct anthropic from env credentials.
func migrateV1(raw []byte) (*models.Providers, []string) {
	var v1 v1File
	if err := json.Unmarshal(raw, &v1); err != nil {
		return &models.Providers{}, []string{fmt.Sprintf("v1 file unreadable (%v), only templates created", err)}
	}

	var warnings []string
	providers := &models.Providers{}
	for _, key := range v1.Profiles.Keys {
		prof, warns := migrateProfile(key, v1.Profiles.Get(key))
		providers.Profiles.Set(key, prof)
		warnings = append(warnings, warns...)
	}

	return providers, warnings
}

func migrateProfile(key string, vp *v1Profile) (*models.Profile, []string) {
	var warnings []string
	prof := &models.Profile{
		Name:   vp.Name,
		Models: vp.Models,
		Env:    dropAnthropicKeys(vp.Env),
	}
	if prof.Env == nil {
		prof.Env = map[string]string{}
	}

	start := ""
	if vp.Proxy != nil {
		start = vp.Proxy.Start
	}

	switch {
	case strings.Contains(start, "ccr"):
		prof.Protocol = models.ProtocolOpenAI
		prof.OpenAI = &models.OpenAIConfig{MaxTokensFld: "max_tokens"}
		baseURL, apiKey, err := loadCCRProvider()
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: failed to load credentials from ~/.claude-code-router/config.json (%v) — fill in openai.base_url/api_key via ccx edit", key, err))
		} else {
			prof.OpenAI.BaseURL, prof.OpenAI.APIKey = baseURL, apiKey
		}
	case strings.Contains(start, "copilot"):
		prof.Protocol = models.ProtocolCopilot
		prof.Copilot = &CopilotDefaults
	case strings.Contains(start, "claude-codex"):
		prof.Protocol = models.ProtocolCodex
		prof.Codex = &CodexDefaults
	case start != "":
		// unrecognized proxy: direct anthropic + a warning
		prof.Protocol = models.ProtocolAnthropic
		prof.Anthropic = anthropicFromEnv(vp.Env)
		warnings = append(warnings, fmt.Sprintf("%s: unrecognized proxy.start %q — profile converted as anthropic, review it manually", key, start))
	default:
		prof.Protocol = models.ProtocolAnthropic
		prof.Anthropic = anthropicFromEnv(vp.Env)
		if isLocalhostURL(prof.Anthropic.BaseURL) {
			warnings = append(warnings, fmt.Sprintf("%s: base_url %s is a local proxy from the previous version; in v2 the daemon is its own proxy, point at the real upstream", key, prof.Anthropic.BaseURL))
		}
	}

	return prof, warnings
}

func anthropicFromEnv(env map[string]string) *models.AnthropicConfig {
	return &models.AnthropicConfig{
		BaseURL:   env["ANTHROPIC_BASE_URL"],
		AuthToken: env["ANTHROPIC_AUTH_TOKEN"],
		APIKey:    env["ANTHROPIC_API_KEY"],
	}
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

func isLocalhostURL(u string) bool {
	return strings.Contains(u, "127.0.0.1") || strings.Contains(u, "localhost")
}

// ccrConfig is the slice of claude-code-router's config.json we need (credentials only).
type ccrConfig struct {
	Providers []struct {
		Name       string `json:"name"`
		APIBaseURL string `json:"api_base_url"`
		APIKey     string `json:"api_key"`
	} `json:"Providers"`
}

// CCRConfigPath overrides the ccr config path (sandbox tests);
// empty — ~/.claude-code-router/config.json.
var CCRConfigPath string

// loadCCRProvider fetches the sole/first ccr provider and strips the
// /chat/completions suffix (in v2 base_url is the root; the translator
// builds the path itself).
func loadCCRProvider() (baseURL, apiKey string, err error) {
	path := CCRConfigPath
	if path == "" {
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			return "", "", homeErr
		}
		path = filepath.Join(home, ".claude-code-router", "config.json")
	}
	raw, err := os.ReadFile(path) //nolint:gosec // path comes from the owning config, not user input
	if err != nil {
		return "", "", err
	}
	var cfg ccrConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return "", "", err
	}
	if len(cfg.Providers) == 0 {
		return "", "", errors.New("no providers in ccr config")
	}
	p := cfg.Providers[0]
	baseURL = strings.TrimSuffix(p.APIBaseURL, "/chat/completions")
	baseURL = strings.TrimSuffix(baseURL, "/")

	return baseURL, p.APIKey, nil
}

// --- environment import (bash-init semantics) ---

// buildFromEnvironment mirrors bash-init: zai from the settings.json env
// block, x5 from env.json (carry-extras), plus the openai/copilot/codex
// templates.
func buildFromEnvironment(cfg *Config) (*models.Providers, []string) {
	var warnings []string
	providers := &models.Providers{}

	zai := profileFromSettings(cfg.SettingsPath())
	if zai != nil {
		providers.Profiles.Set("zai", zai)
	} else {
		providers.Profiles.Set("zai", &models.Profile{Name: zaiProfileName, Protocol: models.ProtocolAnthropic, Anthropic: &models.AnthropicConfig{}, Env: map[string]string{}})
		warnings = append(warnings, "zai: ANTHROPIC_BASE_URL not found in settings.json — profile is empty, fill it in via ccx edit")
	}

	if x5 := profileFromEnvJSON(filepath.Join(cfg.Home, "env.json")); x5 != nil {
		providers.Profiles.Set("x5", x5)
	}

	providers.Profiles.Set("openai", builtinOpenAIProfile())
	providers.Profiles.Set("copilot", builtinCopilotProfile())
	providers.Profiles.Set("codex", builtinCodexProfile())

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
	primary := firstNonEmpty(env["ANTHROPIC_DEFAULT_SONNET_MODEL"], env["ANTHROPIC_DEFAULT_OPUS_MODEL"], env["ANTHROPIC_DEFAULT_MODEL"])
	haiku := env["ANTHROPIC_DEFAULT_HAIKU_MODEL"]

	prof := &models.Profile{
		Name:      zaiProfileName,
		Protocol:  models.ProtocolAnthropic,
		Anthropic: anthropicFromEnv(env),
		Env:       map[string]string{},
	}
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

	return &models.Profile{
		Name:      "X5 Work Gateway (Copilot)",
		Protocol:  models.ProtocolAnthropic,
		Anthropic: anthropicFromEnv(env),
		Env:       dropAnthropicKeys(env),
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}

	return ""
}
