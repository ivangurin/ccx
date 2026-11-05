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

// DefaultDaemonPort — порт демона по умолчанию (фиксированный: settings.json
// указывает на него один раз; конфликт портов — понятная ошибка + CCX_PORT).
const DefaultDaemonPort = 4567

// zaiProfileName — отображаемое имя профиля импорта из settings.json.
const zaiProfileName = "z.ai (GLM)"

// ErrProvidersExist — providers.json уже есть, init без --force отказывает
// (паритет с bash-ccx: не затираем пользовательские креды молча).
var ErrProvidersExist = errors.New("config: providers.json already exists (use --force)")

// InitResult — итог ccx init для вывода пользователю.
type InitResult struct {
	Path     string
	Migrated bool     // конвертировали v1 (proxy{}) → v2 (protocol)
	Profiles []string // ключи в порядке создания
	Warnings []string // что заполнить руками
}

// InitProviders создаёт providers.json v2: миграция v1 либо импорт из
// settings.json/env.json + шаблоны. settings.json не трогает никогда.
// Существующий v1-файл мигрируется без --force (это и есть путь обновления);
// отказ — только если там уже v2.
func InitProviders(cfg *Config, force bool) (*InitResult, error) {
	path := cfg.ProvidersPath()
	res := &InitResult{Path: path}
	var providers *models.Providers

	if raw, err := os.ReadFile(path); err == nil && looksLikeV1(raw) { //nolint:gosec // путь — из конфига владельца
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

// looksLikeV1 — файл без "version": 2 (v1 его не писала).
func looksLikeV1(raw []byte) bool {
	var probe struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return false
	}

	return probe.Version != 2
}

// migrateV1 конвертирует v1-файл по правилам: proxy.start называет внешний
// транслятор (ccr/copilot-api/claude-codex) → соответствующий protocol;
// без proxy → прямой anthropic из env-кредов.
func migrateV1(raw []byte) (*models.Providers, []string) {
	var v1 v1File
	if err := json.Unmarshal(raw, &v1); err != nil {
		return &models.Providers{}, []string{fmt.Sprintf("v1-файл не читается (%v), созданы только шаблоны", err)}
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
			warnings = append(warnings, fmt.Sprintf("%s: не удалось взять креды из ~/.claude-code-router/config.json (%v) — заполните openai.base_url/api_key в ccx edit", key, err))
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
		// нераспознанный прокси: прямой anthropic + предупреждение
		prof.Protocol = models.ProtocolAnthropic
		prof.Anthropic = anthropicFromEnv(vp.Env)
		warnings = append(warnings, fmt.Sprintf("%s: нераспознанный proxy.start %q — профиль переведён как anthropic, проверьте вручную", key, start))
	default:
		prof.Protocol = models.ProtocolAnthropic
		prof.Anthropic = anthropicFromEnv(vp.Env)
		if isLocalhostURL(prof.Anthropic.BaseURL) {
			warnings = append(warnings, fmt.Sprintf("%s: base_url %s — локальный прокси прошлой версии; в v2 демон сам себе прокси, укажите реальный апстрим", key, prof.Anthropic.BaseURL))
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

// ccrConfig — вырезка config.json claude-code-router (нужны только креды).
type ccrConfig struct {
	Providers []struct {
		Name       string `json:"name"`
		APIBaseURL string `json:"api_base_url"`
		APIKey     string `json:"api_key"`
	} `json:"Providers"`
}

// CCRConfigPath — переопределяет путь ccr-конфига (тесты песочницы);
// пусто — ~/.claude-code-router/config.json.
var CCRConfigPath string

// loadCCRProvider достаёт единственного/первого провайдера ccr и отрезает
// суффикс /chat/completions (в v2 base_url — корень, транслятор сам строит путь).
func loadCCRProvider() (baseURL, apiKey string, err error) {
	path := CCRConfigPath
	if path == "" {
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			return "", "", homeErr
		}
		path = filepath.Join(home, ".claude-code-router", "config.json")
	}
	raw, err := os.ReadFile(path) //nolint:gosec // путь — из конфига владельца, не пользовательский ввод
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

// --- импорт окружения (bash-init семантика) ---

// buildFromEnvironment повторяет bash-init: zai из settings.json env-блока,
// x5 из env.json (carry-extras), плюс шаблоны openai/copilot/codex.
func buildFromEnvironment(cfg *Config) (*models.Providers, []string) {
	var warnings []string
	providers := &models.Providers{}

	zai := profileFromSettings(cfg.SettingsPath())
	if zai != nil {
		providers.Profiles.Set("zai", zai)
	} else {
		providers.Profiles.Set("zai", &models.Profile{Name: zaiProfileName, Protocol: models.ProtocolAnthropic, Anthropic: &models.AnthropicConfig{}, Env: map[string]string{}})
		warnings = append(warnings, "zai: в settings.json не найден ANTHROPIC_BASE_URL — профиль пустой, заполните ccx edit")
	}

	if x5 := profileFromEnvJSON(filepath.Join(cfg.Home, "env.json")); x5 != nil {
		providers.Profiles.Set("x5", x5)
	}

	providers.Profiles.Set("openai", builtinOpenAIProfile())
	providers.Profiles.Set("copilot", builtinCopilotProfile())
	providers.Profiles.Set("codex", builtinCodexProfile())

	return providers, warnings
}

// profileFromSettings — прямой профиль из текущего env-блока settings.json;
// юзерские ключи (API_TIMEOUT_MS и пр.) НЕ переносятся — они остаются в
// settings.json навсегда (bash-семантика).
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

// envJSONTail — чинит «обрезанный» env.json без обёртывающих скобок
// (устаревшая копипаста; паритет try_read_env_block bash-версии).
var envJSONTail = regexp.MustCompile(`,\s*}`)

func profileFromEnvJSON(path string) *models.Profile {
	raw, err := os.ReadFile(path) //nolint:gosec // путь — из конфига владельца, не пользовательский ввод
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
		Name:      "Рабочий шлюз X5 (Copilot)",
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
