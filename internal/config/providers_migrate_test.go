package config_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/models"
	suite_provider "github.com/ivangurin/ccx/internal/pkg/suite/provider"
	"github.com/stretchr/testify/require"
)

// legacyUserFile is the pre-rename layout as it sits on a real machine:
// version + daemon.auth_token residue + the map under "profiles".
const (
	legacyUserFile = `{
  "version": 3,
  "daemon": {"port": 4567, "auth_token": "d7c9d22adeadbeef"},
  "profiles": {
    "zai": {"name": "z.ai", "protocol": "anthropic", "base_url": "https://z.example", "auth_token": "z-secret"},
    "acme": {"name": "Acme", "protocol": "openai", "base_url": "https://acme.example", "auth_token": "acme-secret"}
  }
}`
	acmeKey = "acme"
	zaiKey  = "zai"
)

func writeLegacyFile(t *testing.T, sp *suite_provider.Provider, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(sp.Config.ProvidersPath(), []byte(content), 0o600))
}

func TestLoadProviders_MigratesLegacyFileOnDisk(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	writeLegacyFile(t, sp, legacyUserFile)

	loaded, err := config.LoadProviders(sp.Config.ProvidersPath())
	require.NoError(t, err)
	require.Equal(t, 4567, loaded.DaemonPort())
	require.Equal(t, []string{zaiKey, acmeKey}, loaded.Providers.Keys)
	require.Equal(t, "acme-secret", loaded.Providers.Get(acmeKey).AuthToken)

	raw, err := os.ReadFile(sp.Config.ProvidersPath())
	require.NoError(t, err)
	clean := string(raw)
	require.Contains(t, clean, `"providers"`)
	require.NotContains(t, clean, `"version"`)
	require.NotContains(t, clean, `"profiles"`)
	require.NotContains(t, clean, `"auth_token": "d7c9d22adeadbeef"`) // the dead daemon secret is gone
	// profile order survives the rewrite (menu/list order is file order)
	require.Less(t, strings.Index(clean, `"`+zaiKey+`"`), strings.Index(clean, `"`+acmeKey+`"`))
	require.Contains(t, clean, `"port": 4567`)

	info, err := os.Stat(sp.Config.ProvidersPath())
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestLoadProviders_MigratesOnProfilesKeyAlone(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	writeLegacyFile(t, sp, `{"daemon": {"port": 4600}, "profiles": {"zai": {"name": "z.ai", "protocol": "anthropic"}}}`)

	loaded, err := config.LoadProviders(sp.Config.ProvidersPath())
	require.NoError(t, err)
	require.Equal(t, 4600, loaded.DaemonPort())

	raw, err := os.ReadFile(sp.Config.ProvidersPath())
	require.NoError(t, err)
	require.Contains(t, string(raw), `"providers"`)
	require.NotContains(t, string(raw), `"profiles"`)
}

func TestLoadProviders_CleanFileNotRewritten(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	p := &models.Providers{}
	p.Providers.Set(zaiKey, &models.Profile{Name: "z.ai", Protocol: models.ProtocolAnthropic})
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))

	path := sp.Config.ProvidersPath()
	before, err := os.ReadFile(path) //nolint:gosec // sandbox path owned by the test
	require.NoError(t, err)
	past := time.Now().Add(-time.Hour)
	require.NoError(t, os.Chtimes(path, past, past))

	_, err = config.LoadProviders(path)
	require.NoError(t, err)

	after, err := os.ReadFile(path) //nolint:gosec // sandbox path owned by the test
	require.NoError(t, err)
	require.Equal(t, string(before), string(after)) // byte-identical: no churn
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, past.Unix(), info.ModTime().Unix())
}

func TestLoadProviders_MigrationWriteFailureTolerated(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	writeLegacyFile(t, sp, legacyUserFile)

	dir := sp.HomeDir()
	require.NoError(t, os.Chmod(dir, 0o500)) //nolint:gosec // deliberate perms for the failure-tolerance test
	// register the restore BEFORE anything can fail, so it runs before
	// t.TempDir removal (LIFO)
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) }) //nolint:gosec // restoring test dir perms

	loaded, err := config.LoadProviders(sp.Config.ProvidersPath())
	require.NoError(t, err) // best-effort by design: a failed rewrite must not break loading
	require.Equal(t, []string{zaiKey, acmeKey}, loaded.Providers.Keys)

	raw, err := os.ReadFile(sp.Config.ProvidersPath())
	require.NoError(t, err)
	require.Contains(t, string(raw), `"profiles"`) // file stays legacy, retries next load
}
