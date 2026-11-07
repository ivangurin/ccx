package openai_test

import (
	"encoding/json"
	"testing"

	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/translators/openai"
	"github.com/stretchr/testify/require"
)

// Claude Code leads its system prompt with a local billing-accounting line;
// upstream it is noise at best (a cache-prefix difference) and a rejection at
// worst — the chat system message must not carry it.
func TestBuildChatRequest_StripsBillingLine(t *testing.T) {
	req := &models.MessagesRequest{
		Model:     "gpt",
		MaxTokens: 10,
		System:    json.RawMessage(`"x-anthropic-billing-header: tier=free\nYou are a helpful assistant."`),
		Messages:  []models.Message{{Role: "user", Content: json.RawMessage(`"hi"`)}},
	}
	chat := openai.BuildChatRequest(req, "max_tokens")
	require.Len(t, chat.Messages, 2)
	require.Equal(t, "system", chat.Messages[0].Role)
	require.Equal(t, "You are a helpful assistant.", chat.Messages[0].Content)
}
