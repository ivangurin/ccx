// Package models — доменные DTO ccx. Профиль v2: креды апстрима живут в
// protocol-блоке (выбирается по Protocol), env — только пользовательские
// extras для settings.json (не ANTHROPIC_*).
package models

// Protocol — протокол апстрима провайдера; определяет транслятор демона.
type Protocol string

const (
	// ProtocolAnthropic — прямой Anthropic-совместимый API (passthrough).
	ProtocolAnthropic Protocol = "anthropic"
	// ProtocolOpenAI — OpenAI-совместимый chat/completions.
	ProtocolOpenAI Protocol = "openai"
	// ProtocolCopilot — GitHub Copilot (OAuth device flow).
	ProtocolCopilot Protocol = "copilot"
	// ProtocolCodex — ChatGPT Codex Responses API (подписка).
	ProtocolCodex Protocol = "codex"
)

// Model — модель провайдера: id — реальный идентификатор (пишется в
// settings.json), name — алиас без спец-суффиксов для CLI, label — для меню.
type Model struct {
	ID      string `json:"id"`
	Name    string `json:"name,omitempty"`
	Label   string `json:"label,omitempty"`
	Default bool   `json:"default,omitempty"`
}

// AnthropicConfig — прямой Anthropic-совместимый апстрим.
type AnthropicConfig struct {
	BaseURL   string            `json:"base_url"`
	AuthToken string            `json:"auth_token,omitempty"` // Authorization: Bearer
	APIKey    string            `json:"api_key,omitempty"`    // x-api-key (нативные Anthropic)
	Headers   map[string]string `json:"extra_headers,omitempty"`
}

// OpenAIConfig — OpenAI-совместимый апстрим (api.openai.com, корп. шлюзы).
type OpenAIConfig struct {
	BaseURL      string            `json:"base_url"`
	APIKey       string            `json:"api_key,omitempty"`
	UserAgent    string            `json:"user_agent,omitempty"` // UA-фильтры шлюзов (x5)
	MaxTokensFld string            `json:"max_tokens_field,omitempty"`
	PrefillFix   bool              `json:"prefill_fix,omitempty"`
	Reasoning    bool              `json:"reasoning_to_thinking,omitempty"`
	Headers      map[string]string `json:"extra_headers,omitempty"`
}

// CopilotConfig — GitHub Copilot: маскировка под VSCode-клиент обязательна
// (шлюз фильтрует), prefill-фикс включён по умолчанию (шлюз отвергает
// финальное assistant-сообщение — прецедент copilot-bridge).
type CopilotConfig struct {
	DeviceClientID string `json:"device_client_id,omitempty"`
	IntegrationID  string `json:"integration_id,omitempty"`
	EditorVersion  string `json:"editor_version,omitempty"`
	EditorPlugin   string `json:"editor_plugin_version,omitempty"`
	UserAgent      string `json:"user_agent,omitempty"`
	// PrefillFix: nil = включён (дефолт — шлюз Copilot отвергает prefill без
	// фикса); явный false — выключить.
	PrefillFix *bool `json:"prefill_fix,omitempty"`
}

// CodexConfig — ChatGPT подписка через Responses API; логин переиспользует
// codex CLI (auth.json), собственная авторизация не нужна.
type CodexConfig struct {
	AuthFile   string `json:"auth_file,omitempty"`
	Originator string `json:"originator,omitempty"`
	UserAgent  string `json:"user_agent,omitempty"`
}

// Profile — один провайдер.
type Profile struct {
	Name      string            `json:"name"`
	Protocol  Protocol          `json:"protocol"`
	Anthropic *AnthropicConfig  `json:"anthropic,omitempty"`
	OpenAI    *OpenAIConfig     `json:"openai,omitempty"`
	Copilot   *CopilotConfig    `json:"copilot,omitempty"`
	Codex     *CodexConfig      `json:"codex,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Models    []Model           `json:"models"`
}

// ResolveModel находит модель по name или id; возвращает реальный id
// (семантика bash-ccx: алиас без скобок вводится, id применяется).
// model="" и ровно одна default-модель → default; пусто и нет default — "".
func (p *Profile) ResolveModel(model string) (string, bool) {
	if model == "" {
		for _, m := range p.Models {
			if m.Default {
				return m.ID, true
			}
		}

		return "", false
	}
	for _, m := range p.Models {
		if m.ID == model || (m.Name != "" && m.Name == model) {
			return m.ID, true
		}
	}

	return "", false
}

// DefaultModel — id модели с default:true (первая, если несколько).
func (p *Profile) DefaultModel() (string, bool) {
	return p.ResolveModel("")
}
