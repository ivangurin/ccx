package config_test

import (
	"testing"

	"github.com/ivangurin/ccms/internal/config"
	"github.com/ivangurin/ccms/internal/models"
	"github.com/stretchr/testify/require"
)

// ResolveCopilot fills only the empty masquerade fields — the stored values
// win (a hand-tuned editor_version survives), and the input profile is not
// mutated.
func TestResolveCopilot_FillsOnlyEmptyFields(t *testing.T) {
	in := &models.Profile{
		Protocol:      models.ProtocolCopilot,
		EditorVersion: "vscode/2.0.0", // hand-tuned
	}

	out := config.ResolveCopilot(in)

	require.Equal(t, "vscode/2.0.0", out.EditorVersion, "a set field must survive")
	require.Equal(t, "copilot-chat", out.IntegrationID)
	require.Equal(t, config.CopilotDefaults.DeviceClientID, out.DeviceClientID)
	require.Equal(t, config.CopilotDefaults.EditorPluginVersion, out.EditorPluginVersion)
	require.Equal(t, config.CopilotDefaults.UserAgent, out.UserAgent)
	require.NotNil(t, out.PrefillFix)
	require.True(t, *out.PrefillFix)

	// the source profile is untouched (defaults never leak into the stored file)
	require.Empty(t, in.IntegrationID)
	require.Nil(t, in.PrefillFix)
}

func TestResolveCopilot_ExplicitFalseStaysFalse(t *testing.T) {
	disabled := false
	out := config.ResolveCopilot(&models.Profile{Protocol: models.ProtocolCopilot, PrefillFix: &disabled})

	require.NotNil(t, out.PrefillFix)
	require.False(t, *out.PrefillFix)
}

// ResolveCodex fills the CLI mimicry only (auth_file has its own runtime
// default in clients/codex).
func TestResolveCodex_FillsOnlyEmptyFields(t *testing.T) {
	in := &models.Profile{Protocol: models.ProtocolCodex, Originator: "custom-origin"}

	out := config.ResolveCodex(in)

	require.Equal(t, "custom-origin", out.Originator)
	require.Equal(t, config.CodexDefaults.UserAgent, out.UserAgent)
	require.Empty(t, in.UserAgent, "the source profile is untouched")
}
