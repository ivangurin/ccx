// Package models holds ccx domain DTOs. Profile v3: upstream connection
// fields (base_url, api_key, …) live directly in the profile — Protocol
// determines which of them are meaningful; env holds only user extras for
// settings.json (not ANTHROPIC_*).
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

// Profile is a single provider. The connection fields sit at the top level
// (v3: no protocol-named block); which of them are meaningful is determined
// by Protocol. The client masquerade (UA, editor versions, originator) is
// NOT configurable: it lives as constants next to the translators — a
// gateway that changes its expectations needs a code change, not config
// noise in every profile.
type Profile struct {
	Name     string   `json:"name"`
	Protocol Protocol `json:"protocol"`

	// Connection (anthropic, openai).
	BaseURL string `json:"base_url,omitempty"`
	// anthropic: Authorization: Bearer — wins over api_key (proxy.go precedence).
	AuthToken string `json:"auth_token,omitempty"`
	// anthropic: x-api-key (native Anthropic); openai: Authorization: Bearer.
	APIKey string `json:"api_key,omitempty"`
	// anthropic/openai: extra upstream headers.
	Headers map[string]string `json:"extra_headers,omitempty"`

	// openai: max_tokens vs max_completion_tokens request field.
	MaxTokensField string `json:"max_tokens_field,omitempty"`
	// openai: map reasoning_content deltas to thinking blocks (reserved).
	ReasoningToThinking bool `json:"reasoning_to_thinking,omitempty"`

	Env    map[string]string `json:"env,omitempty"`
	Models []Model           `json:"models"`
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
