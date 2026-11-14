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
	debugs    []bool   // the debug launch flag per call
	stopN     int
	stopErr   error
}

func (f *fakeDaemon) Restart(_ context.Context, providerKey string, port int, debug bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restarts = append(f.restarts, providerKey+":"+itoa(port))
	f.debugs = append(f.debugs, debug)
	if f.restartOK {
		return nil
	}

	return errors.New("daemon did not come up")
}

// lastDebugs snapshots the recorded debug flags (nil-safe on no calls).
func (f *fakeDaemon) lastDebugs() []bool {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]bool(nil), f.debugs...)
}

// snapshotRestarts copies the recorded Restart calls.
func (f *fakeDaemon) snapshotRestarts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]string(nil), f.restarts...)
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
	// pinning: ONLY ANTHROPIC_MODEL carries the selected model; the legacy
	// multi-mapping keys are neither written nor kept
	for _, key := range pinnedKeysList {
		require.Equal(t, "copilot-code-large", env[key], key)
	}
	for _, key := range legacyPinnedKeys {
		require.NotContains(t, env, key, key)
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
	require.Equal(t, "acme", st.Provider)
	require.Equal(t, "copilot-code-large", st.Model)
	require.NotEmpty(t, st.Updated)

	// the switch lock is taken at <Home>/switch.lock (a visible sibling of
	// daemon.lock, not the hidden bash-ccx .ccx.lock)
	require.FileExists(t, filepath.Join(sp.HomeDir(), "switch.lock"))

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
    "ANTHROPIC_MODEL": "copilot-code-large",
    "CLAUDE_CODE_AUTO_MODE_SERVER": "0",
    "CLAUDE_CODE_DISABLE_EXTENDED_THINKING": "true",
    "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
    "CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS": "1"
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

// max_context_tokens on the selected model caps the window Claude Code
// believes it has: the 1M context is disabled, the window pinned, and the
// auto-compact threshold set to −10%; a model WITHOUT the parameter wipes all
// three on the next switch (they are ccx-owned, not user keys).
func TestApply_MaxContextTokens(t *testing.T) {
	sw, _, sp := newTestSwitcher(t)
	p, err := config.LoadProviders(sp.Config.ProvidersPath())
	require.NoError(t, err)
	p.Providers.Get("acme").Models[0].MaxContextTokens = 200000
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))

	_, err = sw.Apply(context.Background(), "acme", "")
	require.NoError(t, err)

	env := settingsEnv(t, sp.Config.SettingsPath())
	require.Equal(t, "1", env["CLAUDE_CODE_DISABLE_1M_CONTEXT"])
	require.Equal(t, "200000", env["CLAUDE_CODE_MAX_CONTEXT_TOKENS"])
	require.Equal(t, "180000", env["CLAUDE_CODE_AUTO_COMPACT_WINDOW"], "auto-compact window = max − 10%%")

	// zai's model has no max_context_tokens → the trio is wiped
	_, err = sw.Apply(context.Background(), "zai", "")
	require.NoError(t, err)
	env = settingsEnv(t, sp.Config.SettingsPath())
	require.NotContains(t, env, "CLAUDE_CODE_DISABLE_1M_CONTEXT")
	require.NotContains(t, env, "CLAUDE_CODE_MAX_CONTEXT_TOKENS")
	require.NotContains(t, env, "CLAUDE_CODE_AUTO_COMPACT_WINDOW")
}

// The per-model effort field pins CLAUDE_CODE_EFFORT_LEVEL verbatim (no
// validation — the value passes through as written): written when the model
// carries it, wiped on the next switch when it does not. The key is
// ccx-owned: a manual value in settings.json dies too (stale-managed
// semantics, same as the context trio).
func TestApply_EffortLevel(t *testing.T) {
	sw, _, sp := newTestSwitcher(t)

	// acme's model has no effort → a pre-existing manual value is wiped
	require.NoError(t, os.WriteFile(sp.Config.SettingsPath(), []byte(`{
		"env": {
			"CLAUDE_CODE_EFFORT_LEVEL": "low"
		}
	}`), 0o600))
	_, err := sw.Apply(context.Background(), "acme", "")
	require.NoError(t, err)
	require.NotContains(t, settingsEnv(t, sp.Config.SettingsPath()), "CLAUDE_CODE_EFFORT_LEVEL")

	// a model with the effort field → written verbatim
	p, err := config.LoadProviders(sp.Config.ProvidersPath())
	require.NoError(t, err)
	p.Providers.Get("acme").Models[0].Effort = "high"
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))
	_, err = sw.Apply(context.Background(), "acme", "")
	require.NoError(t, err)
	require.Equal(t, "high", settingsEnv(t, sp.Config.SettingsPath())["CLAUDE_CODE_EFFORT_LEVEL"])

	// back to a model without effort → wiped again
	_, err = sw.Apply(context.Background(), "zai", "")
	require.NoError(t, err)
	require.NotContains(t, settingsEnv(t, sp.Config.SettingsPath()), "CLAUDE_CODE_EFFORT_LEVEL")
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

func TestApply_AlwaysEnvProfileExtraWins(t *testing.T) {
	sw, _, sp := newTestSwitcher(t)
	p, err := config.LoadProviders(sp.Config.ProvidersPath())
	require.NoError(t, err)
	acme := p.Providers.Get("acme")
	acme.Env["CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS"] = "0"
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))

	_, err = sw.Apply(context.Background(), "acme", "")
	require.NoError(t, err)
	require.Equal(t, "0", settingsEnv(t, sp.Config.SettingsPath())["CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS"], "the profile extra wins")

	// zai has no extras: the stale sweep wipes acme's "0" and the always-env
	// default is back
	_, err = sw.Apply(context.Background(), "zai", "")
	require.NoError(t, err)
	require.Equal(t, "1", settingsEnv(t, sp.Config.SettingsPath())["CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS"])
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

// daemon.debug is switcher-owned launch config: the daemon block is read
// HERE (from the same providers.json snapshot the route comes from) and
// rides the spawn as --debug — the daemon itself never reads the block.
func TestApply_DaemonDebugPassedToRestart(t *testing.T) {
	sw, d, sp := newTestSwitcher(t)
	p, err := config.LoadProviders(sp.Config.ProvidersPath())
	require.NoError(t, err)
	p.Daemon.Debug = true
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))

	_, err = sw.Apply(context.Background(), "acme", "")
	require.NoError(t, err)

	require.Equal(t, []bool{true}, d.lastDebugs())
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

func TestRestoreOriginal_NoBackup(t *testing.T) {
	sw, _, sp := newTestSwitcher(t)
	before := readSettings(t, sp.Config.SettingsPath())

	_, err := sw.RestoreOriginal(context.Background())

	require.ErrorContains(t, err, "nothing to restore")
	require.Equal(t, before, readSettings(t, sp.Config.SettingsPath()))
}

func TestRestoreOriginal_RestoresAndStopsDaemon(t *testing.T) {
	sw, d, sp := newTestSwitcher(t)

	_, err := sw.Apply(context.Background(), "zai", "")
	require.NoError(t, err)
	require.NotEqual(t, readSettings(t, sp.Config.SettingsBackupPath()), readSettings(t, sp.Config.SettingsPath()))
	stopsBefore := d.stopN // applyDirect already stopped the idle daemon once

	out, err := sw.RestoreOriginal(context.Background())

	require.NoError(t, err)
	require.Contains(t, out, sp.Config.SettingsPath())
	require.Equal(t, readSettings(t, sp.Config.SettingsBackupPath()), readSettings(t, sp.Config.SettingsPath()))
	require.Equal(t, stopsBefore+1, d.stopN)
}

func TestRestoreOriginal_DaemonStopFailureIsNote(t *testing.T) {
	sw, d, sp := newTestSwitcher(t)
	require.NoError(t, os.WriteFile(sp.Config.SettingsBackupPath(), []byte(`{"env": {}}`), 0o600)) //nolint:gosec // test sandbox
	d.stopErr = errors.New("refused")

	out, err := sw.RestoreOriginal(context.Background())

	require.NoError(t, err)
	require.Contains(t, out, "note:")
	require.JSONEq(t, `{"env": {}}`, readSettings(t, sp.Config.SettingsPath()))
}

func TestRestoreOriginal_KeepsTargetPerms(t *testing.T) {
	sw, _, sp := newTestSwitcher(t)
	// the fixture created settings.json at 0600; os.WriteFile ignores perm on
	// an existing file — retighten via Chmod (umask-independent)
	require.NoError(t, os.WriteFile(sp.Config.SettingsPath(), []byte(`{"env": {}}`), 0o600))               //nolint:gosec // test sandbox
	require.NoError(t, os.Chmod(sp.Config.SettingsPath(), 0o644))                                          //nolint:gosec // a looser target is the point: restore must keep it
	require.NoError(t, os.WriteFile(sp.Config.SettingsBackupPath(), []byte(`{"env": {"X": "1"}}`), 0o600)) //nolint:gosec // test sandbox

	_, err := sw.RestoreOriginal(context.Background())

	require.NoError(t, err)
	info, err := os.Stat(sp.Config.SettingsPath())
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o644), info.Mode().Perm())
}

func TestRestoreOriginal_CorruptBackupRefused(t *testing.T) {
	sw, _, sp := newTestSwitcher(t)
	before := readSettings(t, sp.Config.SettingsPath())
	require.NoError(t, os.WriteFile(sp.Config.SettingsBackupPath(), []byte(`{not json`), 0o600)) //nolint:gosec // test sandbox

	_, err := sw.RestoreOriginal(context.Background())

	require.ErrorContains(t, err, "not valid JSON")
	require.Equal(t, before, readSettings(t, sp.Config.SettingsPath()))
}

func TestRestoreOriginal_ResetsState(t *testing.T) {
	sw, _, sp := newTestSwitcher(t)

	_, err := sw.Apply(context.Background(), "zai", "")
	require.NoError(t, err)
	require.FileExists(t, sp.Config.StatePath())

	_, err = sw.RestoreOriginal(context.Background())
	require.NoError(t, err)

	// no provider is on record after a restore: rollbackDaemon must have
	// nothing to resurrect on a later failed switch, ccx status nothing to
	// report as active
	var state models.State
	require.NoError(t, jsonfile.ReadJSON(sp.Config.StatePath(), &state))
	require.Empty(t, state.Provider)
	require.Empty(t, state.Model)
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
