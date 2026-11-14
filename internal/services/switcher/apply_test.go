package switcher_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/pkg/jsonfile"
	suite_provider "github.com/ivangurin/ccx/internal/pkg/suite/provider"
	"github.com/ivangurin/ccx/internal/services/switcher"
	switcher_mocks "github.com/ivangurin/ccx/internal/services/switcher/mocks"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// daemonRec — the daemon mock's call recorder: the mockery mock carries the
// wiring, the recorder carries the assertions (call sequences, debug flags,
// stop counts, the configurable outcomes).
type daemonRec struct {
	mu         sync.Mutex
	calls      []string // "provider:port" per Restart
	debugs     []bool   // the debug launch flag per Restart
	stops      int
	restartErr error
	stopErr    error
}

func (r *daemonRec) snapshotCalls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]string(nil), r.calls...)
}

func (r *daemonRec) snapshotDebugs() []bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]bool(nil), r.debugs...)
}

// wireDaemon arms the suite's daemon mock with recording defaults: Restart
// records the call and returns restartErr (read at call time — tests flip it
// mid-test), Stop counts and returns stopErr. Maybe(): call counts vary per
// scenario (rollback restarts twice, direct paths never restart).
func wireDaemon(d *switcher_mocks.Daemon) *daemonRec {
	rec := &daemonRec{}
	d.EXPECT().Restart(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, providerKey string, port int, debug bool) error {
			rec.mu.Lock()
			defer rec.mu.Unlock()
			rec.calls = append(rec.calls, providerKey+":"+itoa(port))
			rec.debugs = append(rec.debugs, debug)

			return rec.restartErr
		}).Maybe()
	d.EXPECT().Stop(mock.Anything).
		RunAndReturn(func(_ context.Context) error {
			rec.mu.Lock()
			defer rec.mu.Unlock()
			rec.stops++

			return rec.stopErr
		}).Maybe()

	return rec
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

func newTestSwitcher(t *testing.T) (*switcher.Service, *daemonRec, *suite_provider.Provider) {
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
		Models: []models.Model{
			{ID: "glm-5.3[1m]", Label: "GLM 5.3", Default: true},
			{ID: "glm-flash", Label: "Flash"},
		},
	})
	p.Providers.Set("acme", &models.Profile{
		Name:      "Acme",
		Protocol:  models.ProtocolOpenAI,
		BaseURL:   "https://gw",
		AuthToken: "k",
		Models:    []models.Model{{ID: "copilot-code-large", Default: true}},
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

	rec := wireDaemon(sp.GetDaemonMock())

	return sp.GetSwitcherService(), rec, sp
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

func readSettings(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(filepath.Dir(path), filepath.Base(path))) //nolint:gosec // sandbox path
	require.NoError(t, err)

	return string(data)
}

func TestApply_HappyPath(t *testing.T) {
	sw, rec, sp := newTestSwitcher(t)

	// acme (openai) — the daemon path: BASE_URL is the daemon, Restart got the
	// provider key and the resolved port (daemon.port 4567)
	out, err := sw.Apply(context.Background(), "acme", "")
	require.NoError(t, err)
	require.Contains(t, out, "Switched")
	require.Contains(t, out, "Acme")
	require.Contains(t, out, "http://127.0.0.1:4567")

	require.Equal(t, []string{"acme:4567"}, rec.calls)

	env := settingsEnv(t, sp.Config.SettingsPath())
	require.Equal(t, "http://127.0.0.1:4567", env["ANTHROPIC_BASE_URL"])
	// acme's auth_token travels to settings.json as ANTHROPIC_AUTH_TOKEN
	// (auth_token is the only credential field)
	require.Equal(t, "k", env["ANTHROPIC_AUTH_TOKEN"])
	require.NotContains(t, env, "ANTHROPIC_API_KEY")
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
	require.Equal(t, 0, rec.stops)
}

// Models resolve by exact id (the name alias is gone): a bare id without
// the [1m] suffix must not match the bracketed one, the full id applies.
func TestApply_ModelMatchesIDOnly(t *testing.T) {
	sw, rec, sp := newTestSwitcher(t)

	_, err := sw.Apply(context.Background(), "zai", "glm-5.3") // no [1m] suffix
	require.ErrorContains(t, err, "not found")
	require.NoFileExists(t, sp.Config.StatePath())

	out, err := sw.Apply(context.Background(), "zai", "glm-5.3[1m]")
	require.NoError(t, err)
	require.Contains(t, out, "glm-5.3[1m]")

	st := readState(t, sp.Config.StatePath())
	require.Equal(t, "glm-5.3[1m]", st.Model)
	require.Empty(t, rec.calls) // zai is a direct profile — no daemon round-trip
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

// A failed restart leaves settings.json byte-identical — the invariant.
func TestApply_RestartFailureLeavesSettingsUntouched(t *testing.T) {
	sw, rec, sp := newTestSwitcher(t)
	rec.restartErr = errors.New("daemon did not come up")
	before := readSettings(t, sp.Config.SettingsPath())

	_, err := sw.Apply(context.Background(), "acme", "") // daemon path
	require.ErrorContains(t, err, "untouched")
	require.Equal(t, before, readSettings(t, sp.Config.SettingsPath()))
	require.NoFileExists(t, sp.Config.StatePath())
}

// daemon.debug is switcher-owned launch config: the daemon block is read
// HERE (from the same providers.json snapshot the route comes from) and
// rides the spawn as --debug — the daemon itself never reads the block.
func TestApply_DaemonDebugPassedToRestart(t *testing.T) {
	sw, rec, sp := newTestSwitcher(t)
	p, err := config.LoadProviders(sp.Config.ProvidersPath())
	require.NoError(t, err)
	p.Daemon.Debug = true
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))

	_, err = sw.Apply(context.Background(), "acme", "")
	require.NoError(t, err)

	require.Equal(t, []bool{true}, rec.snapshotDebugs())
}

func TestApply_LockSerializes(t *testing.T) {
	sw, rec, sp := newTestSwitcher(t)

	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			_, _ = sw.Apply(context.Background(), "zai", "glm-flash")
		})
	}
	wg.Wait()

	st := readState(t, sp.Config.StatePath())
	require.Equal(t, "glm-flash", st.Model)
	require.Empty(t, rec.calls) // direct profile: only the flock serializes
}

func TestApply_MissingSettingsFails(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	p := &models.Providers{}
	p.Providers.Set("zai", &models.Profile{Protocol: models.ProtocolAnthropic, Models: []models.Model{{ID: "m", Default: true}}})
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))

	_, err := sp.GetSwitcherService().Apply(context.Background(), "zai", "")
	require.ErrorContains(t, err, "settings.json")
}

func TestApply_Idempotent(t *testing.T) {
	sw, _, _ := newTestSwitcher(t)

	for range 2 {
		_, err := sw.Apply(context.Background(), "zai", "")
		require.NoError(t, err)
	}
}
