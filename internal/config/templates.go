package config

import "github.com/ivangurin/ccx/internal/models"

// CopilotDefaults — маскировка под VSCode-клиент (шлюз Copilot фильтрует
// посторонних); значения — публичные из расширения GitHub Copilot Chat.
var CopilotDefaults = models.CopilotConfig{
	DeviceClientID: "Iv1.b507a08c87ecfe98",
	IntegrationID:  "copilot-chat",
	EditorVersion:  "vscode/1.99.0",
	EditorPlugin:   "copilot-chat/0.28.0",
	UserAgent:      "GitHubCopilotChat/0.28.0",
	PrefillFix:     true,
}

// CodexDefaults — мимикрия под codex CLI (Responses API подписки ChatGPT).
var CodexDefaults = models.CodexConfig{
	AuthFile:   "~/.codex/auth.json",
	Originator: "codex_cli_rs",
	UserAgent:  "codex_cli_rs/1.0.0",
}

func builtinOpenAIProfile() *models.Profile {
	return &models.Profile{
		Name:     "OpenAI (API)",
		Protocol: models.ProtocolOpenAI,
		OpenAI: &models.OpenAIConfig{
			BaseURL:      "https://api.openai.com/v1",
			APIKey:       "<sk-...>",
			MaxTokensFld: "max_completion_tokens",
		},
		Env: map[string]string{},
		Models: []models.Model{
			{ID: "gpt-6.1-sol", Label: "GPT-6.1 Sol · текущая рабочая модель", Default: true},
			{ID: "gpt-6-astra", Label: "GPT-6 Astra · максимум интеллекта"},
			{ID: "gpt-6.1-terra", Label: "GPT-6.1 Terra · Codex-модель"},
			{ID: "gpt-6-sol", Label: "GPT-6 Sol · прошлое поколение"},
			{ID: "gpt-6-luna", Label: "GPT-6 Luna · быстрая и доступная"},
			{ID: "gpt-5.6-sol", Label: "GPT-5.6 Sol · старшее поколение"},
			{ID: "gpt-5.6-terra", Label: "GPT-5.6 Terra · старшая сбалансированная"},
			{ID: "gpt-5.6-luna", Label: "GPT-5.6 Luna · старшая быстрая"},
		},
	}
}

func builtinCopilotProfile() *models.Profile {
	copilot := CopilotDefaults

	return &models.Profile{
		Name:     "GitHub Copilot (личная)",
		Protocol: models.ProtocolCopilot,
		Copilot:  &copilot,
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
	codex := CodexDefaults

	return &models.Profile{
		Name:     "ChatGPT Codex (подписка)",
		Protocol: models.ProtocolCodex,
		Codex:    &codex,
		Env:      map[string]string{},
		Models: []models.Model{
			{ID: "gpt-6.1-codex", Label: "GPT-6.1 Codex", Default: true},
			{ID: "gpt-6.1-terra", Label: "GPT-6.1 Terra"},
			{ID: "gpt-5.6-sol", Label: "GPT-5.6 Sol"},
		},
	}
}
