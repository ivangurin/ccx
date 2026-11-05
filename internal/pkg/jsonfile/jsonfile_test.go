package jsonfile_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ivangurin/ccx/internal/pkg/jsonfile"
	"github.com/stretchr/testify/require"
)

type sample struct {
	Name  string            `json:"name"`
	Items map[string]string `json:"items,omitempty"`
}

func TestWriteAtomicJSON_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")
	in := sample{Name: "zai", Items: map[string]string{"a": "b"}}

	require.NoError(t, jsonfile.WriteAtomicJSON(path, in, 0o600))

	var out sample
	require.NoError(t, jsonfile.ReadJSON(path, &out))
	require.Equal(t, in, out)
}

func TestWriteAtomicJSON_SetsPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")

	require.NoError(t, jsonfile.WriteAtomicJSON(path, sample{Name: "x"}, 0o600))

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestWriteAtomicJSON_PreservesExistingPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")
	require.NoError(t, os.WriteFile(path, []byte(`{}`), 0o644)) //nolint:gosec // намеренные права для теста сохранения

	require.NoError(t, jsonfile.WriteAtomicJSON(path, sample{Name: "x"}, 0o600))

	// Инвариант bash-ccx: атомарная подмена возвращает исходные права файла
	// (chmod tmp под права цели ДО rename).
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o644), info.Mode().Perm())
}

func TestWriteAtomicJSON_NoTmpLeftovers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.json")

	require.NoError(t, jsonfile.WriteAtomicJSON(path, sample{Name: "x"}, 0o600))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "data.json", entries[0].Name())
}

func TestWriteAtomicJSON_CreatesMissingDir(t *testing.T) {
	// mkdir -p семантика: каталоги бэкапов/логов создаются на записи (как в bash-ccx)
	path := filepath.Join(t.TempDir(), "no-such-dir", "data.json")

	require.NoError(t, jsonfile.WriteAtomicJSON(path, sample{Name: "x"}, 0o600))

	var out sample
	require.NoError(t, jsonfile.ReadJSON(path, &out))
	require.Equal(t, "x", out.Name)
}

func TestWriteAtomicJSON_OverwritesContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")
	require.NoError(t, jsonfile.WriteAtomicJSON(path, sample{Name: "first"}, 0o600))

	require.NoError(t, jsonfile.WriteAtomicJSON(path, sample{Name: "second"}, 0o600))

	var out sample
	require.NoError(t, jsonfile.ReadJSON(path, &out))
	require.Equal(t, "second", out.Name)
}

func TestReadJSON_MissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.json")

	err := jsonfile.ReadJSON(path, &sample{})
	require.Error(t, err)
}
