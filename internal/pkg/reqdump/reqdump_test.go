package reqdump

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDisabledByDefault(t *testing.T) {
	ResetForTest()
	require.False(t, Enabled())

	Write("anthropic-req", []byte(`{}`)) // must not panic nor create anything

	entries, err := os.ReadDir(t.TempDir())
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestEnabledByFlagWritesJSONL(t *testing.T) {
	ResetForTest()
	dir := filepath.Join(t.TempDir(), "nested", "dumps") // lazy MkdirAll
	launch := time.Date(2026, 11, 9, 21, 58, 30, 0, time.Local)
	Configure(Options{Enabled: true, Dir: dir, Now: func() time.Time { return launch }})
	require.True(t, Enabled())
	require.NoDirExists(t, dir) // nothing created until the first Write

	Write("anthropic-req", []byte(`{"model":"m"}`))
	Write("codex-sse", []byte(`{"event":"x"}`))

	path := filepath.Join(dir, "dump-"+launch.Format("20060102-150405")+".log")
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	data, err := os.ReadFile(path) //nolint:gosec // test sandbox dir
	require.NoError(t, err)
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	require.Len(t, lines, 2)

	var first, second dumpLine
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &first))
	require.NoError(t, json.Unmarshal([]byte(lines[1]), &second))
	require.Equal(t, "anthropic-req", first.Kind)
	require.Equal(t, int64(1), first.Seq)
	require.Equal(t, int64(2), second.Seq)
	require.JSONEq(t, `{"model":"m"}`, string(first.Body)) // verbatim, not a string
	require.JSONEq(t, `{"event":"x"}`, string(second.Body))
}

func TestDumpDirOverride(t *testing.T) {
	ResetForTest()
	def := filepath.Join(t.TempDir(), "default")
	other := filepath.Join(t.TempDir(), "override")
	t.Setenv("CCX_DUMP_DIR", other)
	Configure(Options{Enabled: true, Dir: def})

	Write("anthropic-req", []byte(`{}`))

	entries, err := os.ReadDir(other)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.NoDirExists(t, def)
	require.NoFileExists(t, filepath.Join(def, "dump-anything.log"))
}

func TestInvalidBodyAsString(t *testing.T) {
	ResetForTest()
	dir := t.TempDir()
	Configure(Options{Enabled: true, Dir: dir})

	Write("codex-resp-400", []byte("not json"))

	data, err := os.ReadFile(filepath.Join(dir, dumpFilePrefix+launchTS+".log")) //nolint:gosec // test sandbox dir
	require.NoError(t, err)
	var line dumpLine
	require.NoError(t, json.Unmarshal(data, &line))
	require.Equal(t, `"not json"`, string(line.Body))
}

func TestEnvWithoutEnabledWritesNothing(t *testing.T) {
	ResetForTest()
	dir := t.TempDir()
	t.Setenv("CCX_DUMP_DIR", dir) // env alone must not enable dumping
	Configure(Options{Enabled: false, Dir: dir})
	require.False(t, Enabled())

	Write("anthropic-req", []byte(`{"model":"m"}`))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestAppendAcrossWritesAndReconfigure(t *testing.T) {
	ResetForTest()
	dir := t.TempDir()
	Configure(Options{Enabled: true, Dir: dir})
	Write("a", []byte(`{"n":1}`))
	// a re-Configure re-opens the handle (tests only; production calls it once)
	Configure(Options{Enabled: true, Dir: dir})
	Write("b", []byte(`{"n":2}`))

	data, err := os.ReadFile(filepath.Join(dir, dumpFilePrefix+launchTS+".log")) //nolint:gosec // test sandbox dir
	require.NoError(t, err)
	require.Equal(t, 2, strings.Count(string(data), "\n"))
	require.GreaterOrEqual(t, seqForTest(), int64(2))
}
