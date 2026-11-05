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

// fakeDaemon — мок демона: помнит вызовы, умеет падать.
type fakeDaemon struct {
	mu       sync.Mutex
	ensureOK bool
	useOK    bool
	ensureN  int
	useCalls []string
}

func (f *fakeDaemon) Ensure(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensureN++
	if f.ensureOK {
		return nil
	}

	return errors.New("daemon down")
}

func (f *fakeDaemon) Use(_ context.Context, profile, model string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.useCalls = append(f.useCalls, profile+"/"+model)
	if f.useOK {
		return nil
	}

	return errors.New("translator not ready")
}

func newTestSwitcher(t *testing.T) (*switcher.Switcher, *fakeDaemon, *suite_provider.Provider) {
	t.Helper()
	sp := suite_provider.NewProvider(t)

	// providers.json: zai (anthropic, extras нет), x5 (openai + extra-ключ)
	p := &models.Providers{
		Version: 2,
		Daemon:  models.Daemon{Port: 4567, AuthToken: "daemon-token"},
	}
	p.Profiles.Set("zai", &models.Profile{
		Name:      "z.ai (GLM)",
		Protocol:  models.ProtocolAnthropic,
		Anthropic: &models.AnthropicConfig{BaseURL: "https://api.z.ai/api/anthropic", AuthToken: "sk-z"},
		Env:       map[string]string{},
		Models: []models.Model{
			{ID: "glm-5.3[1m]", Name: "glm-5.3", Label: "GLM 5.3", Default: true},
			{ID: "glm-flash", Label: "Flash"},
		},
	})
	p.Profiles.Set("x5", &models.Profile{
		Name:     "X5",
		Protocol: models.ProtocolOpenAI,
		OpenAI:   &models.OpenAIConfig{BaseURL: "https://gw", APIKey: "k"},
		Env:      map[string]string{"CLAUDE_CODE_DISABLE_EXTENDED_THINKING": "true"},
		Models:   []models.Model{{ID: "copilot-code-large", Default: true}},
	})
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))

	// settings.json с юзерскими ключами (не трогаются) и мусорным ANTHROPIC_*
	require.NoError(t, os.WriteFile(sp.Config.SettingsPath(), []byte(`{
		"env": {
			"API_TIMEOUT_MS": "3000000",
			"IDP_TOKEN": "user-token",
			"ANTHROPIC_STALE_KEY": "old"
		},
		"model": "old-model",
		"permissions": {"allow": ["Bash"]}
	}`), 0o600))

	d := &fakeDaemon{ensureOK: true, useOK: true}
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

	out, err := sw.Apply(context.Background(), "zai", "")
	require.NoError(t, err)
	require.Contains(t, out, "Переключено")
	require.Contains(t, out, "z.ai (GLM)")
	require.Contains(t, out, "glm-5.3[1m]")

	env := settingsEnv(t, sp.Config.SettingsPath())
	require.Equal(t, "http://127.0.0.1:4567", env["ANTHROPIC_BASE_URL"])
	require.Equal(t, "daemon-token", env["ANTHROPIC_AUTH_TOKEN"])
	// пиннинг: все маппинги на выбранную модель
	for _, key := range []string{
		"ANTHROPIC_MODEL", "ANTHROPIC_DEFAULT_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL",
		"ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL",
		"ANTHROPIC_DEFAULT_FABLE_MODEL", "ANTHROPIC_SMALL_FAST_MODEL",
	} {
		require.Equal(t, "glm-5.3[1m]", env[key], key)
	}
	// юзерские сохранены, устаревший ANTHROPIC_* стёрт
	require.Equal(t, "3000000", env["API_TIMEOUT_MS"])
	require.Equal(t, "user-token", env["IDP_TOKEN"])
	require.NotContains(t, env, "ANTHROPIC_STALE_KEY")

	// модель запинена
	var s struct {
		Model   string         `json:"model"`
		Avail   []string       `json:"availableModels"`
		Enforce bool           `json:"enforceAvailableModels"`
		Perms   map[string]any `json:"permissions"`
	}
	require.NoError(t, jsonfile.ReadJSON(sp.Config.SettingsPath(), &s))
	require.Equal(t, "glm-5.3[1m]", s.Model)
	require.Equal(t, []string{"glm-5.3[1m]"}, s.Avail)
	require.True(t, s.Enforce)
	// прочие поля файла не тронуты
	require.Equal(t, map[string]any{"allow": []any{"Bash"}}, s.Perms)

	// state
	st := readState(t, sp.Config.StatePath())
	require.Equal(t, "zai", st.Profile)
	require.Equal(t, "glm-5.3[1m]", st.Model)
	require.NotEmpty(t, st.Updated)

	// демон получил use
	require.Equal(t, []string{"zai/glm-5.3[1m]"}, d.useCalls)
}

func TestApply_ModelByNameAlias(t *testing.T) {
	sw, _, sp := newTestSwitcher(t)

	out, err := sw.Apply(context.Background(), "zai", "glm-5.3") // name без суффикса
	require.NoError(t, err)
	require.Contains(t, out, "glm-5.3[1m]") // применился реальный id

	st := readState(t, sp.Config.StatePath())
	require.Equal(t, "glm-5.3[1m]", st.Model)
}

func TestApply_UnknownProfile(t *testing.T) {
	sw, _, sp := newTestSwitcher(t)
	before := readSettings(t, sp.Config.SettingsPath())

	_, err := sw.Apply(context.Background(), "nope", "")
	require.ErrorContains(t, err, "неизвестный профиль")
	require.Equal(t, before, readSettings(t, sp.Config.SettingsPath()))
}

func TestApply_UnknownModel(t *testing.T) {
	sw, _, sp := newTestSwitcher(t)

	_, err := sw.Apply(context.Background(), "zai", "no-model")
	require.ErrorContains(t, err, "не найдена")
	require.NoFileExists(t, sp.Config.StatePath())
}

func TestApply_EnsureFails_Untouched(t *testing.T) {
	sw, d, sp := newTestSwitcher(t)
	d.ensureOK = false

	_, err := sw.Apply(context.Background(), "zai", "")
	require.ErrorContains(t, err, "не тронут")
	require.NoFileExists(t, sp.Config.StatePath())
	require.NotContains(t, settingsEnv(t, sp.Config.SettingsPath()), "ANTHROPIC_BASE_URL")
}

func TestApply_UseFails_Untouched(t *testing.T) {
	sw, d, sp := newTestSwitcher(t)
	d.useOK = false

	_, err := sw.Apply(context.Background(), "zai", "")
	require.ErrorContains(t, err, "не тронут")
	require.NoFileExists(t, sp.Config.StatePath())
}

func TestApply_SwitchRemovesPrevExtras(t *testing.T) {
	sw, _, sp := newTestSwitcher(t)

	_, err := sw.Apply(context.Background(), "x5", "")
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
	p := &models.Providers{Version: 2}
	p.Profiles.Set("zai", &models.Profile{Protocol: models.ProtocolAnthropic, Models: []models.Model{{ID: "m", Default: true}}})
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))

	sw := switcher.New(sp.Config, &fakeDaemon{ensureOK: true, useOK: true})
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
	data, err := os.ReadFile(filepath.Join(filepath.Dir(path), filepath.Base(path))) //nolint:gosec // песочница
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
