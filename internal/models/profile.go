// Package models holds ccx domain DTOs. Profile v3: upstream connection
// fields (base_url, auth_token) live directly in the profile — Protocol
// determines which of them are meaningful. auth_token is the only credential
// field (the api_key/extra_headers/env/max_tokens_field/reasoning_to_thinking
// fields are gone: a legacy field in providers.json is ignored on load and
// dropped on the next save).
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
// settings.json and matched exactly — the historical "name" alias is gone,
// a legacy name field in providers.json is ignored on load and dropped on
// the next save), label is the menu text. MaxContextTokens (optional) caps
// the context window Claude Code believes it has: the switcher derives the
// CLAUDE_CODE_* context env from it; zero means unset.
type Model struct {
	ID      string `json:"id"`
	Label   string `json:"label,omitempty"`
	Default bool   `json:"default,omitempty"`
	// MaxContextTokens → CLAUDE_CODE_MAX_CONTEXT_TOKENS and friends on switch.
	MaxContextTokens int `json:"max_context_tokens,omitempty"`
	// Effort → CLAUDE_CODE_EFFORT_LEVEL on switch (written verbatim, no
	// validation); empty means unset — the key is wiped (ccx-owned).
	Effort string `json:"effort,omitempty"`
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

	// Connection (anthropic, openai). auth_token is the only credential
	// field — sent as Authorization: Bearer upstream (anthropic passthrough)
	// and written to settings.json on switch (the "ccx" placeholder when
	// empty on the daemon path).
	BaseURL   string `json:"base_url,omitempty"`
	AuthToken string `json:"auth_token,omitempty"`

	Models []Model `json:"models"`
}

// ResolveModelEntry finds a model by id (exact match) and returns the whole
// entry (the switcher needs max_context_tokens alongside the id).
// model="" with exactly one default model → default; empty and no default → nil.
func (p *Profile) ResolveModelEntry(model string) (*Model, bool) {
	if model == "" {
		for i := range p.Models {
			if p.Models[i].Default {
				return &p.Models[i], true
			}
		}

		return nil, false
	}
	for i := range p.Models {
		m := &p.Models[i]
		if m.ID == model {
			return m, true
		}
	}

	return nil, false
}

// ResolveModel finds a model by id and returns it (the id is both what the
// user types and what gets applied).
func (p *Profile) ResolveModel(model string) (string, bool) {
	m, ok := p.ResolveModelEntry(model)
	if !ok {
		return "", false
	}

	return m.ID, true
}

// DefaultModel returns the id of the model with default:true (first one if several).
func (p *Profile) DefaultModel() (string, bool) {
	return p.ResolveModel("")
}
