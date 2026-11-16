package switcher_test

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// A switch must be invisible outside the env block and the model pin: blocks
// keep their order and formatting, the env block keeps its lines (a fresh key
// is appended at its end) — the file is the user's, not ccx's.
func TestApply_PreservesSettingsFormatting(t *testing.T) {
	sw, _, sp := newTestSwitcher(t)

	in := `{
  "permissions": {
    "allow": ["Bash(npm run test)"]
  },
  "env": {
    "API_TIMEOUT_MS": "3000000",
    "ANTHROPIC_STALE_KEY": "old"
  },
  "model": "old-model",
  "availableModels": ["old-model"],
  "enforceAvailableModels": true
}
`
	require.NoError(t, os.WriteFile(sp.Config.SettingsPath(), []byte(in), 0o600))

	_, err := sw.Apply(context.Background(), "acme", "")
	require.NoError(t, err)

	// the leftover enforceAvailableModels is wiped; availableModels carries
	// the FULL provider model list (the fixture's stale ["old-model"] list
	// is replaced in place)
	want := `{
  "permissions": {
    "allow": ["Bash(npm run test)"]
  },
  "env": {
    "API_TIMEOUT_MS": "3000000",
    "ANTHROPIC_AUTH_TOKEN": "k",
    "ANTHROPIC_BASE_URL": "http://127.0.0.1:4567",
    "ANTHROPIC_DEFAULT_MODEL": "copilot-code-large",
    "ANTHROPIC_MODEL": "copilot-code-large",
    "CLAUDE_CODE_AUTO_MODE_SERVER": "0",
    "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
    "CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS": "1"
  },
  "model": "copilot-code-large",
  "availableModels": ["copilot-code-large"]
}
`
	got, err := os.ReadFile(sp.Config.SettingsPath())
	require.NoError(t, err)
	require.Equal(t, want, string(got))
}

// A settings.json left over from the old multi-mapping pin (haiku/sonnet/
// opus/… all pointing at a model) is cleaned on the next switch: only
// ANTHROPIC_MODEL survives, the rest is wiped.
func TestApply_RemovesLegacyPinnedKeys(t *testing.T) {
	sw, _, sp := newTestSwitcher(t)
	require.NoError(t, os.WriteFile(sp.Config.SettingsPath(), []byte(`{
		"env": {
			"ANTHROPIC_MODEL": "old",
			"ANTHROPIC_DEFAULT_MODEL": "old",
			"ANTHROPIC_DEFAULT_HAIKU_MODEL": "old",
			"ANTHROPIC_DEFAULT_SONNET_MODEL": "old",
			"ANTHROPIC_DEFAULT_OPUS_MODEL": "old",
			"ANTHROPIC_DEFAULT_FABLE_MODEL": "old",
			"ANTHROPIC_SMALL_FAST_MODEL": "old"
		}
	}`), 0o600))

	_, err := sw.Apply(context.Background(), "acme", "")
	require.NoError(t, err)

	env := settingsEnv(t, sp.Config.SettingsPath())
	require.Equal(t, "copilot-code-large", env["ANTHROPIC_MODEL"])
	for _, key := range legacyPinnedKeys {
		require.NotContains(t, env, key, key)
	}
}

// Always-env: three CLAUDE_CODE_* flags are present in every block ccx
// writes — added at their defaults only when missing. A value the user set
// by hand survives untouched; a profile extra wins over the default; a wiped
// stale profile extra falls back to the default again.
func TestApply_AlwaysEnvAdded(t *testing.T) {
	sw, _, sp := newTestSwitcher(t)

	_, err := sw.Apply(context.Background(), "acme", "")
	require.NoError(t, err)

	env := settingsEnv(t, sp.Config.SettingsPath())
	require.Equal(t, "1", env["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"])
	require.Equal(t, "1", env["CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS"])
	require.Equal(t, "0", env["CLAUDE_CODE_AUTO_MODE_SERVER"])
}

func TestApply_AlwaysEnvNotOverwritten(t *testing.T) {
	sw, _, sp := newTestSwitcher(t)
	require.NoError(t, os.WriteFile(sp.Config.SettingsPath(), []byte(`{
		"env": {
			"CLAUDE_CODE_AUTO_MODE_SERVER": "1"
		}
	}`), 0o600))

	_, err := sw.Apply(context.Background(), "acme", "")
	require.NoError(t, err)

	env := settingsEnv(t, sp.Config.SettingsPath())
	require.Equal(t, "1", env["CLAUDE_CODE_AUTO_MODE_SERVER"], "the user's own value is not overwritten")
	require.Equal(t, "1", env["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"], "the missing ones are added")
}

func TestApply_OneTimeBackup(t *testing.T) {
	sw, _, sp := newTestSwitcher(t)
	original := readSettings(t, sp.Config.SettingsPath())

	_, err := sw.Apply(context.Background(), "zai", "")
	require.NoError(t, err)

	// the first switch freezes the original: fixed name, 0600, byte-for-byte
	info, err := os.Stat(sp.Config.SettingsBackupPath())
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	require.Equal(t, original, readSettings(t, sp.Config.SettingsBackupPath()))

	// a later switch finds the backup on disk and leaves the original frozen
	_, err = sw.Apply(context.Background(), "acme", "")
	require.NoError(t, err)
	require.Equal(t, original, readSettings(t, sp.Config.SettingsBackupPath()))
}
