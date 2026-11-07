package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeUserHome pins $HOME to a temp dir: config defaults (~/.ccx, the
// legacy ~/.claude) then resolve inside it — the test is hermetic.
func fakeUserHome(t *testing.T) string {
	t.Helper()

	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("CCX_HOME", "")

	return fakeHome
}

func TestMigrateLegacy_CopiesPreRenameData(t *testing.T) {
	fakeHome := fakeUserHome(t)

	legacy := filepath.Join(fakeHome, ".claude")
	require.NoError(t, os.MkdirAll(legacy, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(legacy, "providers.json"),
		[]byte(`{"providers":{}}`), 0o600))
	// nothing in the new world yet
	require.NoDirExists(t, filepath.Join(fakeHome, ".ccx"))

	migrateLegacy()

	require.FileExists(t, filepath.Join(fakeHome, ".ccx", "providers.json"))
}

func TestMigrateLegacy_CustomHomeOptsOut(t *testing.T) {
	fakeHome := fakeUserHome(t)
	// a custom CCX_HOME (tests, sandboxes) must never pull the live
	// ~/.claude data into itself
	t.Setenv("CCX_HOME", filepath.Join(fakeHome, "custom"))

	legacy := filepath.Join(fakeHome, ".claude")
	require.NoError(t, os.MkdirAll(legacy, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(legacy, "providers.json"),
		[]byte(`{"providers":{}}`), 0o600))

	migrateLegacy()

	require.NoDirExists(t, filepath.Join(fakeHome, ".ccx"))
	require.NoFileExists(t, filepath.Join(fakeHome, "custom", "providers.json"))
}
