package models_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ivangurin/ccx/internal/models"
	"github.com/stretchr/testify/require"
)

func writeSettings(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.json")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	return path
}

func TestSettings_PatchEnvPreservesFileByteForByte(t *testing.T) {
	const testModelID = "m1"
	in := `{
  "permissions": {
    "allow": ["Bash(npm run lint)"]
  },
  "env": {
    "API_TIMEOUT_MS": "3000000",
    "MCP_TOKEN": "user-token",
    "ANTHROPIC_STALE": "old",
    "ANTHROPIC_BASE_URL": "http://old"
  },
  "statusLine": {"type": "command"}
}
`
	// untouched blocks, stale key gone, existing key replaced in place,
	// new keys appended at the end of env in a stable order, model pin at the end
	want := `{
  "permissions": {
    "allow": ["Bash(npm run lint)"]
  },
  "env": {
    "API_TIMEOUT_MS": "3000000",
    "MCP_TOKEN": "user-token",
    "ANTHROPIC_BASE_URL": "http://127.0.0.1:4567",
    "ANTHROPIC_AUTH_TOKEN": "tok",
    "ANTHROPIC_MODEL": "m1"
  },
  "statusLine": {"type": "command"},
  "model": "m1",
  "availableModels": ["m1"]
}
`

	s, err := models.LoadSettings(writeSettings(t, in))
	require.NoError(t, err)
	require.NoError(t, s.PatchEnv(
		[]string{"ANTHROPIC_STALE"},
		map[string]string{
			"ANTHROPIC_BASE_URL":   "http://127.0.0.1:4567",
			"ANTHROPIC_AUTH_TOKEN": "tok",
			"ANTHROPIC_MODEL":      testModelID,
		},
	))
	require.NoError(t, s.SetModelPin(testModelID, []string{testModelID}))
	require.Equal(t, want, string(s.Bytes()))
}

func TestSettings_PatchEnvCreatesMissingBlock(t *testing.T) {
	in := "{\n  \"permissions\": {\"allow\": [\"Bash\"]}\n}\n"
	want := `{
  "permissions": {"allow": ["Bash"]},
  "env": {
    "ANTHROPIC_BASE_URL": "http://127.0.0.1:4567",
    "ANTHROPIC_MODEL": "m1"
  }
}
`

	s, err := models.LoadSettings(writeSettings(t, in))
	require.NoError(t, err)
	require.NoError(t, s.PatchEnv(nil, map[string]string{
		"ANTHROPIC_MODEL":    "m1",
		"ANTHROPIC_BASE_URL": "http://127.0.0.1:4567",
	}))
	require.Equal(t, want, string(s.Bytes()))
}

func TestSettings_PatchEnvEmptyObjectFillsPretty(t *testing.T) {
	in := "{\n  \"env\": {},\n  \"a\": 1\n}\n"
	want := "{\n  \"env\": {\n    \"K\": \"v\"\n  },\n  \"a\": 1\n}\n"

	s, err := models.LoadSettings(writeSettings(t, in))
	require.NoError(t, err)
	require.NoError(t, s.PatchEnv(nil, map[string]string{"K": "v"}))
	require.Equal(t, want, string(s.Bytes()))
}

func TestSettings_PatchEnvCompactFile(t *testing.T) {
	in := `{"env":{"A":"1","OLD":"x"},"model":"old"}`
	want := `{"env":{"A":"1","B":"2"},"model":"old"}`

	s, err := models.LoadSettings(writeSettings(t, in))
	require.NoError(t, err)
	require.NoError(t, s.PatchEnv([]string{"OLD"}, map[string]string{"A": "1", "B": "2"}))
	require.Equal(t, want, string(s.Bytes()))
}

func TestSettings_PatchEnvNotAnObject(t *testing.T) {
	s, err := models.LoadSettings(writeSettings(t, `{"env":"oops"}`))
	require.NoError(t, err)

	err = s.PatchEnv(nil, map[string]string{"K": "v"})
	require.ErrorContains(t, err, "env")
	require.ErrorContains(t, err, "not an object")
}

// SetModelPin writes the model key and the FULL provider allowlist: the
// picker shows every provider model and hides the built-in families none of
// them map to. availableModels is replaced in place; enforceAvailableModels
// is deliberately not written and a leftover one is wiped — with enforce on
// Claude Code ignores ANTHROPIC_DEFAULT_MODEL, breaking the Default row.
func TestSettings_SetModelPinWritesModelAndAllowlist(t *testing.T) {
	in := "{\n  \"model\": \"old\",\n  \"availableModels\": [\"old\"],\n  \"enforceAvailableModels\": true,\n  \"env\": {\"A\": \"1\"}\n}\n"
	want := "{\n  \"model\": \"m1\",\n  \"availableModels\": [\"m1\",\"m2\"],\n  \"env\": {\"A\": \"1\"}\n}\n"

	s, err := models.LoadSettings(writeSettings(t, in))
	require.NoError(t, err)
	require.NoError(t, s.SetModelPin("m1", []string{"m1", "m2"}))
	require.Equal(t, want, string(s.Bytes()))
}

func TestSettings_PatchEnvPreservesBlankLinesAndInlineBlocks(t *testing.T) {
	// a realistic file: a blank line between top-level entries, inline blocks
	in := `{
  "permissions": {
    "allow": ["Bash(npm run test)", "WebFetch(domain:example.com)"],
    "deny": []
  },
  "env": {
    "API_TIMEOUT_MS": "3000000",
    "MCP_TOKEN": "user-secret",
    "ANTHROPIC_OLD_TOKEN": "stale"
  },
  "includeCoAuthoredBy": false,

  "statusLine": {
    "type": "command",
    "command": "ccx statusline"
  }
}
`
	want := `{
  "permissions": {
    "allow": ["Bash(npm run test)", "WebFetch(domain:example.com)"],
    "deny": []
  },
  "env": {
    "API_TIMEOUT_MS": "3000000",
    "MCP_TOKEN": "user-secret",
    "ANTHROPIC_AUTH_TOKEN": "daemon-token",
    "ANTHROPIC_BASE_URL": "http://127.0.0.1:4567",
    "ANTHROPIC_MODEL": "glm-5.3[1m]",
    "EXTRA_FROM_PROFILE": "yes"
  },
  "includeCoAuthoredBy": false,

  "statusLine": {
    "type": "command",
    "command": "ccx statusline"
  },
  "model": "glm-5.3[1m]",
  "availableModels": ["glm-5.3[1m]","glm-flash"]
}
`

	s, err := models.LoadSettings(writeSettings(t, in))
	require.NoError(t, err)
	require.NoError(t, s.PatchEnv(
		[]string{"ANTHROPIC_OLD_TOKEN"},
		map[string]string{
			"ANTHROPIC_BASE_URL":   "http://127.0.0.1:4567",
			"ANTHROPIC_AUTH_TOKEN": "daemon-token",
			"ANTHROPIC_MODEL":      "glm-5.3[1m]",
			"EXTRA_FROM_PROFILE":   "yes",
		},
	))
	require.NoError(t, s.SetModelPin("glm-5.3[1m]", []string{"glm-5.3[1m]", "glm-flash"}))
	require.Equal(t, want, string(s.Bytes()))
}

func TestSettings_EnvReturnsCurrentValues(t *testing.T) {
	s, err := models.LoadSettings(writeSettings(t, `{"env":{"A":"1","B":"2"},"model":"x"}`))
	require.NoError(t, err)
	require.Equal(t, map[string]string{"A": "1", "B": "2"}, s.Env())
}

func TestSettings_LoadErrors(t *testing.T) {
	_, err := models.LoadSettings(writeSettings(t, "not json"))
	require.Error(t, err)

	_, err = models.LoadSettings(filepath.Join(t.TempDir(), "missing.json"))
	require.Error(t, err)
}
