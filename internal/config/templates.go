package config

import (
	"github.com/ivangurin/ccx/internal/models"
)

// The client masquerade (UA, editor versions, integration id, originator)
// is not config anymore: it lives as constants next to the translators and
// the copilot client. A gateway that changes its expectations needs a code
// change — not the same values copy-pasted into every profile.

func builtinOpenAIProfile() *models.Profile {
	return &models.Profile{
		Name:           "OpenAI (API)",
		Protocol:       models.ProtocolOpenAI,
		BaseURL:        "https://api.openai.com/v1",
		APIKey:         "<sk-...>",
		MaxTokensField: "max_completion_tokens",
		Env:            map[string]string{},
		Models: []models.Model{
			{ID: "gpt-6.1-sol", Label: "GPT-6.1 Sol · current workhorse model", Default: true},
			{ID: "gpt-6-astra", Label: "GPT-6 Astra · maximum intelligence"},
			{ID: "gpt-6.1-terra", Label: "GPT-6.1 Terra · Codex model"},
			{ID: "gpt-6-sol", Label: "GPT-6 Sol · previous generation"},
			{ID: "gpt-6-luna", Label: "GPT-6 Luna · fast and affordable"},
			{ID: "gpt-5.6-sol", Label: "GPT-5.6 Sol · older generation"},
			{ID: "gpt-5.6-terra", Label: "GPT-5.6 Terra · older balanced"},
			{ID: "gpt-5.6-luna", Label: "GPT-5.6 Luna · older fast"},
		},
	}
}

func builtinCopilotProfile() *models.Profile {
	return &models.Profile{
		Name:     "GitHub Copilot (personal)",
		Protocol: models.ProtocolCopilot,
		Env:      map[string]string{},
		Models: []models.Model{
			{ID: "claude-sonnet-5.5", Label: "Claude Sonnet 5.5", Default: true},
			{ID: "gpt-6-luna", Label: "GPT-6 Luna"},
			{ID: "gpt-6.1-terra", Label: "GPT-6.1 Terra"},
			{ID: "claude-haiku-5.5", Label: "Claude Haiku 5.5"},
			{ID: "grok-4.7", Label: "Grok 4.7"},
			{ID: "gemini-3.8-flash", Label: "Gemini 3.8 Flash"},
			{ID: "grok-4.6", Label: "Grok 4.6"},
			{ID: "mai-code-1.1-flash", Label: "MAI-Code-1.1-Flash"},
			{ID: "kimi-k3", Label: "Kimi K3"},
			{ID: "gpt-5.6-luna", Label: "GPT-5.6 Luna"},
			{ID: "gpt-5.6-terra", Label: "GPT-5.6 Terra"},
			{ID: "claude-sonnet-5", Label: "Claude Sonnet 5"},
		},
	}
}

func builtinCodexProfile() *models.Profile {
	return &models.Profile{
		Name:     "ChatGPT Codex (subscription)",
		Protocol: models.ProtocolCodex,
		AuthFile: "~/.codex/auth.json", // spelled out in the file: it documents where the login lives
		Env:      map[string]string{},
		Models: []models.Model{
			{ID: "gpt-6.1-codex", Label: "GPT-6.1 Codex", Default: true},
			{ID: "gpt-6.1-terra", Label: "GPT-6.1 Terra"},
			{ID: "gpt-5.6-sol", Label: "GPT-5.6 Sol"},
		},
	}
}
