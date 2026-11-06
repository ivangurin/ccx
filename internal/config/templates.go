package config

import (
	"github.com/ivangurin/ccms/internal/models"
)

// prefillFixDefault is PrefillFix=true by default for the copilot template.
var prefillFixDefault = true

// CopilotDefaults is the VSCode client masquerade (the Copilot gateway
// filters out outsiders); values are public ones from the GitHub Copilot
// Chat extension. Used as a partial Profile: templates copy it wholesale,
// ResolveCopilot fills empty fields from it.
var CopilotDefaults = models.Profile{
	DeviceClientID:      "Iv1.b507a08c87ecfe98",
	IntegrationID:       "copilot-chat",
	EditorVersion:       "vscode/1.99.0",
	EditorPluginVersion: "copilot-chat/0.28.0",
	UserAgent:           "GitHubCopilotChat/0.28.0",
	PrefillFix:          &prefillFixDefault,
}

// CodexDefaults mimics the codex CLI (ChatGPT subscription Responses API).
var CodexDefaults = models.Profile{
	AuthFile:   "~/.codex/auth.json",
	Originator: "codex_cli_rs",
	UserAgent:  "codex_cli_rs/1.0.0",
}

// ResolveCopilot returns a copy of the profile with empty masquerade fields
// filled from CopilotDefaults (a profile hand-written without them still
// impersonates VSCode — v2 nil-block parity). The stored file is not touched.
func ResolveCopilot(p *models.Profile) *models.Profile {
	out := *p
	if out.DeviceClientID == "" {
		out.DeviceClientID = CopilotDefaults.DeviceClientID
	}
	if out.IntegrationID == "" {
		out.IntegrationID = CopilotDefaults.IntegrationID
	}
	if out.EditorVersion == "" {
		out.EditorVersion = CopilotDefaults.EditorVersion
	}
	if out.EditorPluginVersion == "" {
		out.EditorPluginVersion = CopilotDefaults.EditorPluginVersion
	}
	if out.UserAgent == "" {
		out.UserAgent = CopilotDefaults.UserAgent
	}
	if out.PrefillFix == nil {
		out.PrefillFix = CopilotDefaults.PrefillFix
	}

	return &out
}

// ResolveCodex returns a copy of the profile with empty CLI-mimicry fields
// filled from CodexDefaults (auth_file has its own default in clients/codex).
func ResolveCodex(p *models.Profile) *models.Profile {
	out := *p
	if out.Originator == "" {
		out.Originator = CodexDefaults.Originator
	}
	if out.UserAgent == "" {
		out.UserAgent = CodexDefaults.UserAgent
	}

	return &out
}

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
	p := &models.Profile{
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

	return ResolveCopilot(p)
}

func builtinCodexProfile() *models.Profile {
	p := &models.Profile{
		Name:     "ChatGPT Codex (subscription)",
		Protocol: models.ProtocolCodex,
		AuthFile: CodexDefaults.AuthFile, // spelled out in the file: it documents where the login lives
		Env:      map[string]string{},
		Models: []models.Model{
			{ID: "gpt-6.1-codex", Label: "GPT-6.1 Codex", Default: true},
			{ID: "gpt-6.1-terra", Label: "GPT-6.1 Terra"},
			{ID: "gpt-5.6-sol", Label: "GPT-5.6 Sol"},
		},
	}

	return ResolveCodex(p)
}
