package switcher_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/pkg/jsonfile"
	suite_provider "github.com/ivangurin/ccx/internal/pkg/suite/provider"
	"github.com/ivangurin/ccx/internal/services/switcher"
	"github.com/stretchr/testify/require"
)

// fakeDaemon — a daemon mock: remembers Restart calls, can fail on demand.
type fakeDaemon struct {
	mu        sync.Mutex
	restartOK bool
	restarts  []string // "profile:port" per call
	stopN     int
	stopErr   error
}

func (f *fakeDaemon) Restart(_ context.Context, providerKey string, port int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restarts = append(f.restarts, providerKey+":"+itoa(port))
	if f.restartOK {
		return nil
	}

	return errors.New("daemon did not come up")
}

func (f *fakeDaemon) Stop(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopN++

	return f.stopErr
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}

	return string(b)
}

func newTestSwitcher(t *testing.T) (*switcher.Switcher, *fakeDaemon, *suite_provider.Provider) {
	t.Helper()
	sp := suite_provider.NewProvider(t)

	// providers.json: zai (anthropic, no extras), acme (openai + an extra key)
	p := &models.Providers{
		Daemon: &models.Daemon{Port: 4567},
	}
	p.Providers.Set("zai", &models.Profile{
		Name:      "z.ai (GLM)",
		Protocol:  models.ProtocolAnthropic,
		BaseURL:   "https://api.z.ai/api/anthropic",
		AuthToken: "sk-z",
		Env:       map[string]string{},
		Models: []models.Model{
			{ID: "glm-5.3[1m]", Name: "glm-5.3", Label: "GLM 5.3", Default: true},
			{ID: "glm-flash", Label: "Flash"},
		},
	})
	p.Providers.Set("acme", &models.Profile{
		Name:     "Acme",
		Protocol: models.ProtocolOpenAI,
		BaseURL:  "https://gw",
		APIKey:   "k",
		Env:      map[string]string{"CLAUDE_CODE_DISABLE_EXTENDED_THINKING": "true"},
		Models:   []models.Model{{ID: "copilot-code-large", Default: true}},
	})
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))

	// settings.json with user keys (left untouched) and a stale ANTHROPIC_*
	require.NoError(t, os.WriteFile(sp.Config.SettingsPath(), []byte(`{
		"env": {
			"API_TIMEOUT_MS": "3000000",
			"MCP_TOKEN": "user-token",
			"ANTHROPIC_STALE_KEY": "old"
		},
		"model": "old-model",
		"permissions": {"allow": ["Bash"]}
	}`), 0o600))

	d := &fakeDaemon{restartOK: true}
	sw := switcher.New(sp.Config, d)

	return sw, d, sp
}

func settingsEnv(t *testing.T, path string) map[string]string {
	t.Helper()
	var s struct {
		Env map[string]string `json:"env"`
	}
	require.NoError(t, jsonfile.ReadJSON(path, &s))

	return s.Env
}

func readState(t *testing.T, path string) models.State {
	t.Helper()
	var st models.State
	require.NoError(t, jsonfile.ReadJSON(path, &st))

	return st
}

func TestApply_HappyPath(t *testing.T) {
	sw, d, sp := newTestSwitcher(t)

	// acme (openai) — the daemon path: BASE_URL is the daemon, Restart got the
	// provider key and the resolved port (daemon.port 4567)
	out, err := sw.Apply(context.Background(), "acme", "")
	require.NoError(t, err)
	require.Contains(t, out, "Switched")
	require.Contains(t, out, "Acme")
	require.Contains(t, out, "http://127.0.0.1:4567")

	require.Equal(t, []string{"acme:4567"}, d.restarts)

	env := settingsEnv(t, sp.Config.SettingsPath())
	require.Equal(t, "http://127.0.0.1:4567", env["ANTHROPIC_BASE_URL"])
	// acme has only api_key → ANTHROPIC_API_KEY (direct-path precedence)
	require.Equal(t, "k", env["ANTHROPIC_API_KEY"])
	require.NotContains(t, env, "ANTHROPIC_AUTH_TOKEN")
	// pinning: all mappings point to the selected model
	for _, key := range pinnedKeysList {
		require.Equal(t, "copilot-code-large", env[key], key)
	}
	// user keys kept, the stale ANTHROPIC_* wiped
	require.Equal(t, "3000000", env["API_TIMEOUT_MS"])
	require.Equal(t, "user-token", env["MCP_TOKEN"])
	require.NotContains(t, env, "ANTHROPIC_STALE_KEY")
	// profile extras land in the env
	require.Equal(t, "true", env["CLAUDE_CODE_DISABLE_EXTENDED_THINKING"])

	// the model is pinned
	var s struct {
		Model   string         `json:"model"`
		Avail   []string       `json:"availableModels"`
		Enforce bool           `json:"enforceAvailableModels"`
		Perms   map[string]any `json:"permissions"`
	}
	require.NoError(t, jsonfile.ReadJSON(sp.Config.SettingsPath(), &s))
	require.Equal(t, "copilot-code-large", s.Model)
	require.Equal(t, []string{"copilot-code-large"}, s.Avail)
	require.True(t, s.Enforce)
	// other file fields are untouched
	require.Equal(t, map[string]any{"allow": []any{"Bash"}}, s.Perms)

	// state
	st := readState(t, sp.Config.StatePath())
	require.Equal(t, "acme", st.Profile)
	require.Equal(t, "copilot-code-large", st.Model)
	require.NotEmpty(t, st.Updated)

	// the daemon stays up on the daemon path (no stop)
	require.Equal(t, 0, d.stopN)
}

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
  "model": "old-model"
}
`
	require.NoError(t, os.WriteFile(sp.Config.SettingsPath(), []byte(in), 0o600))

	_, err := sw.Apply(context.Background(), "acme", "")
	require.NoError(t, err)

	want := `{
  "permissions": {
    "allow": ["Bash(npm run test)"]
  },
  "env": {
    "API_TIMEOUT_MS": "3000000",
    "ANTHROPIC_API_KEY": "k",
    "ANTHROPIC_BASE_URL": "http://127.0.0.1:4567",
    "ANTHROPIC_DEFAULT_FABLE_MODEL": "copilot-code-large",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "copilot-code-large",
    "ANTHROPIC_DEFAULT_MODEL": "copilot-code-large",
    "ANTHROPIC_DEFAULT_OPUS_MODEL": "copilot-code-large",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "copilot-code-large",
    "ANTHROPIC_MODEL": "copilot-code-large",
    "ANTHROPIC_SMALL_FAST_MODEL": "copilot-code-large",
    "CLAUDE_CODE_DISABLE_EXTENDED_THINKING": "true"
  },
  "model": "copilot-code-large",
  "availableModels": ["copilot-code-large"],
  "enforceAvailableModels": true
}
`
	got, err := os.ReadFile(sp.Config.SettingsPath())
	require.NoError(t, err)
	require.Equal(t, want, string(got))
}

// auth_token wins over api_key (direct-path precedence) for the daemon path too.
func TestApply_OpenAI_AuthTokenWinsOverAPIKey(t *testing.T) {
	sw, _, sp := newTestSwitcher(t)
	p, err := config.LoadProviders(sp.Config.ProvidersPath())
	require.NoError(t, err)
	p.Providers.Get("acme").AuthToken = "sk-acme"
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))

	_, err = sw.Apply(context.Background(), "acme", "")
	require.NoError(t, err)

	env := settingsEnv(t, sp.Config.SettingsPath())
	require.Equal(t, "sk-acme", env["ANTHROPIC_AUTH_TOKEN"])
	require.NotContains(t, env, "ANTHROPIC_API_KEY")
}

// no credential in the profile → placeholder token (Claude Code expects one;
// the daemon ignores it for non-pass-through protocols).
func TestApply_NoCredentials_PlaceholderToken(t *testing.T) {
	sw, _, sp := newTestSwitcher(t)
	p, err := config.LoadProviders(sp.Config.ProvidersPath())
	require.NoError(t, err)
	p.Providers.Get("acme").APIKey = ""
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))

	_, err = sw.Apply(context.Background(), "acme", "")
	require.NoError(t, err)

	env := settingsEnv(t, sp.Config.SettingsPath())
	require.Equal(t, "ccx", env["ANTHROPIC_AUTH_TOKEN"])
}

// A failed restart leaves settings.json byte-identical — the invariant.
func TestApply_RestartFailureLeavesSettingsUntouched(t *testing.T) {
	sw, d, sp := newTestSwitcher(t)
	d.restartOK = false
	before := readSettings(t, sp.Config.SettingsPath())

	_, err := sw.Apply(context.Background(), "acme", "") // daemon path
	require.ErrorContains(t, err, "untouched")
	require.Equal(t, before, readSettings(t, sp.Config.SettingsPath()))
	require.NoFileExists(t, sp.Config.StatePath())
}

func TestApply_ModelByNameAlias(t *testing.T) {
	sw, _, sp := newTestSwitcher(t)

	out, err := sw.Apply(context.Background(), "zai", "glm-5.3") // name without the suffix
	require.NoError(t, err)
	require.Contains(t, out, "glm-5.3[1m]") // the real id was applied

	st := readState(t, sp.Config.StatePath())
	require.Equal(t, "glm-5.3[1m]", st.Model)
}

func TestApply_UnknownProfile(t *testing.T) {
	sw, _, sp := newTestSwitcher(t)
	before := readSettings(t, sp.Config.SettingsPath())

	_, err := sw.Apply(context.Background(), "nope", "")
	require.ErrorContains(t, err, "unknown provider")
	require.Equal(t, before, readSettings(t, sp.Config.SettingsPath()))
}

func TestApply_UnknownModel(t *testing.T) {
	sw, _, sp := newTestSwitcher(t)

	_, err := sw.Apply(context.Background(), "zai", "no-model")
	require.ErrorContains(t, err, "not found")
	require.NoFileExists(t, sp.Config.StatePath())
}

func TestApply_SwitchRemovesPrevExtras(t *testing.T) {
	sw, _, sp := newTestSwitcher(t)

	_, err := sw.Apply(context.Background(), "acme", "")
	require.NoError(t, err)
	env := settingsEnv(t, sp.Config.SettingsPath())
	require.Equal(t, "true", env["CLAUDE_CODE_DISABLE_EXTENDED_THINKING"])

	_, err = sw.Apply(context.Background(), "zai", "")
	require.NoError(t, err)
	env = settingsEnv(t, sp.Config.SettingsPath())
	require.NotContains(t, env, "CLAUDE_CODE_DISABLE_EXTENDED_THINKING")
}

func TestApply_CreatesBackup600(t *testing.T) {
	sw, _, sp := newTestSwitcher(t)

	_, err := sw.Apply(context.Background(), "zai", "")
	require.NoError(t, err)

	entries, err := os.ReadDir(sp.Config.BackupDir())
	require.NoError(t, err)
	require.Len(t, entries, 1)
	info, err := entries[0].Info()
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	require.Contains(t, entries[0].Name(), "settings-")
}

func TestApply_LockSerializes(t *testing.T) {
	sw, _, sp := newTestSwitcher(t)

	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			_, _ = sw.Apply(context.Background(), "zai", "glm-flash")
		})
	}
	wg.Wait()

	st := readState(t, sp.Config.StatePath())
	require.Equal(t, "glm-flash", st.Model)
}

func TestApply_MissingSettingsFails(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	p := &models.Providers{}
	p.Providers.Set("zai", &models.Profile{Protocol: models.ProtocolAnthropic, Models: []models.Model{{ID: "m", Default: true}}})
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))

	sw := switcher.New(sp.Config, &fakeDaemon{restartOK: true})
	_, err := sw.Apply(context.Background(), "zai", "")
	require.ErrorContains(t, err, "settings.json")
}

func TestApply_Idempotent(t *testing.T) {
	sw, _, _ := newTestSwitcher(t)

	for range 2 {
		_, err := sw.Apply(context.Background(), "zai", "")
		require.NoError(t, err)
	}
}

func readSettings(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(filepath.Dir(path), filepath.Base(path))) //nolint:gosec // sandbox path
	require.NoError(t, err)

	return string(data)
}

func TestState_UpdatedFormat(t *testing.T) {
	sw, _, sp := newTestSwitcher(t)

	_, err := sw.Apply(context.Background(), "zai", "")
	require.NoError(t, err)

	st := readState(t, sp.Config.StatePath())
	_, err = time.Parse(time.RFC3339, st.Updated)
	require.NoError(t, err)
}
