package reqdump

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWriteDisabledWithoutEnv(t *testing.T) {
	t.Setenv("CCMS_DUMP_DIR", "")
	require.False(t, Enabled())

	Write("anthropic-req", []byte(`{}`))

	entries, err := os.ReadDir(t.TempDir())
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestWriteCreatesFileWithBody(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "dumps") // lazy MkdirAll
	t.Setenv("CCMS_DUMP_DIR", dir)
	require.True(t, Enabled())

	Write("anthropic-req", []byte(`{"model":"m"}`))
	Write("codex-resp-400", []byte(`{"detail":"boom"}`))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 2)

	byBody := map[string]string{} // body → file name
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(dir, e.Name())) //nolint:gosec // test sandbox dir
		require.NoError(t, err)
		info, err := e.Info()
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
		byBody[string(data)] = e.Name()
	}
	require.Contains(t, byBody[`{"model":"m"}`], "anthropic-req")
	require.Contains(t, byBody[`{"detail":"boom"}`], "codex-resp-400")
}
