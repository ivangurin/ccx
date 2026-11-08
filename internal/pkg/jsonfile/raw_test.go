package jsonfile_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ivangurin/ccx/internal/pkg/jsonfile"
	"github.com/stretchr/testify/require"
)

// WriteAtomicRaw keeps the TARGET's existing perms (the WriteAtomicJSON
// policy): settings.json is Claude Code's file, and a switch must not
// silently tighten it — perm applies only when the target does not exist.
func TestWriteAtomicRaw_KeepsTargetPerms(t *testing.T) {
	path := filepath.Join(t.TempDir(), "target.json")
	require.NoError(t, os.WriteFile(path, []byte("old"), 0o644)) //nolint:gosec // group-readable target is the point of the test

	require.NoError(t, jsonfile.WriteAtomicRaw(path, []byte("new"), 0o600))

	data, err := os.ReadFile(path) //nolint:gosec // t.TempDir path
	require.NoError(t, err)
	require.Equal(t, "new", string(data))
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o644), info.Mode().Perm(), "existing perms survive the atomic rewrite")
}

func TestWriteAtomicRaw_NewFileGetsPerm(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fresh.json")

	require.NoError(t, jsonfile.WriteAtomicRaw(path, []byte("x"), 0o600))

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}
