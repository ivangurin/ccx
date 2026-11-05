// Package models holds ccms domain DTOs. Profile v2: upstream credentials live
// in the protocol block (selected by Protocol), env holds only user extras
// for settings.json (not ANTHROPIC_*).
package models

// Protocol is the provider's upstream protocol; it determines the daemon's translator.
type Protocol string

const (
	// ProtocolAnthropic is a direct Anthropic-compatible API (passthrough).
	ProtocolAnthropic Protocol = "anthropic"
	// ProtocolOpenAI is an OpenAI-compatible chat/completions API.
	ProtocolOpenAI Protocol = "openai"
	// ProtocolCopilot is GitHub Copilot (OAuth device flow).
	ProtocolCopilot Protocol = "copilot"
	// ProtocolCodex is the ChatGPT Codex Responses API (subscription).
	ProtocolCodex Protocol = "codex"
)

// Model is a provider model: id is the real identifier (written to
// settings.json), name is an alias without special suffixes for the CLI,
// label is for the menu.
type Model struct {
	ID      string `json:"id"`
	Name    string `json:"name,omitempty"`
	Label   string `json:"label,omitempty"`
	Default bool   `json:"default,omitempty"`
}

// AnthropicConfig is a direct Anthropic-compatible upstream.
type AnthropicConfig struct {
	BaseURL   string            `json:"base_url"`
	AuthToken string            `json:"auth_token,omitempty"` // Authorization: Bearer
	APIKey    string            `json:"api_key,omitempty"`    // x-api-key (native Anthropic)
	Headers   map[string]string `json:"extra_headers,omitempty"`
}

// OpenAIConfig is an OpenAI-compatible upstream (api.openai.com, corporate gateways).
type OpenAIConfig struct {
	BaseURL      string            `json:"base_url"`
	APIKey       string            `json:"api_key,omitempty"`
	UserAgent    string            `json:"user_agent,omitempty"` // gateway UA filters (x5)
	MaxTokensFld string            `json:"max_tokens_field,omitempty"`
	PrefillFix   bool              `json:"prefill_fix,omitempty"`
	Reasoning    bool              `json:"reasoning_to_thinking,omitempty"`
	Headers      map[string]string `json:"extra_headers,omitempty"`
}

// CopilotConfig is GitHub Copilot: masquerading as a VSCode client is
// mandatory (the gateway filters others out), the prefill fix is on by
// default (the gateway rejects a final assistant message — the
// copilot-bridge precedent).
type CopilotConfig struct {
	DeviceClientID string `json:"device_client_id,omitempty"`
	IntegrationID  string `json:"integration_id,omitempty"`
	EditorVersion  string `json:"editor_version,omitempty"`
	EditorPlugin   string `json:"editor_plugin_version,omitempty"`
	UserAgent      string `json:"user_agent,omitempty"`
	// PrefillFix: nil = enabled (default — the Copilot gateway rejects
	// prefill without the fix); explicit false disables it.
	PrefillFix *bool `json:"prefill_fix,omitempty"`
}

// CodexConfig is a ChatGPT subscription via the Responses API; login reuses
// the codex CLI (auth.json), no separate authentication is needed.
type CodexConfig struct {
	AuthFile   string `json:"auth_file,omitempty"`
	Originator string `json:"originator,omitempty"`
	UserAgent  string `json:"user_agent,omitempty"`
}

// Profile is a single provider.
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

// ResolveModel finds a model by name or id and returns the real id
// (bash-ccx semantics: the alias without brackets is what the user types,
// the id is what gets applied).
// model="" with exactly one default model → default; empty and no default → "".
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

// DefaultModel returns the id of the model with default:true (first one if several).
func (p *Profile) DefaultModel() (string, bool) {
	return p.ResolveModel("")
}
