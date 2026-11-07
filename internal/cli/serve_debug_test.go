package cli_test

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ivangurin/ccx/internal/app"
	"github.com/ivangurin/ccx/internal/cli"
	suite_provider "github.com/ivangurin/ccx/internal/pkg/suite/provider"
	"github.com/stretchr/testify/require"
)

// debugLaunchTime — fixed launch time: the file name in the test must be
// deterministic, in production it is time.Now().
var debugLaunchTime = time.Date(2026, 11, 7, 15, 4, 5, 0, time.Local)

func debugLogPath(sp *suite_provider.Provider) string {
	return filepath.Join(sp.Config.LogDir(), "debug-"+debugLaunchTime.Format("20060102-150405")+".log")
}

func TestBuildServeLogger_DebugTeesToFile(t *testing.T) {
	sp := suite_provider.NewProvider(t)

	var stderr bytes.Buffer
	log, closeLog, err := cli.BuildServeLoggerForTest(sp.Config, app.ServeOptions{Debug: true}, &stderr, debugLaunchTime)
	require.NoError(t, err)

	log.Info("debug-hello")
	closeLog()

	raw, err := os.ReadFile(debugLogPath(sp))
	require.NoError(t, err)
	require.Contains(t, string(raw), "msg=debug-hello")     // dev format in the file
	require.Contains(t, stderr.String(), "msg=debug-hello") // tee: stderr keeps working
}

func TestBuildServeLogger_DebugOverridesEnvLevelAndFormat(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	sp.Config.LogLevel = "error" // suite default — the flag must beat it
	sp.Config.LogFormat = "json"

	log, closeLog, err := cli.BuildServeLoggerForTest(sp.Config, app.ServeOptions{Debug: true}, io.Discard, debugLaunchTime)
	require.NoError(t, err)

	log.Debug("debug-level-line") // level=error would swallow this
	closeLog()

	raw, err := os.ReadFile(debugLogPath(sp))
	require.NoError(t, err)
	require.Contains(t, string(raw), "level=DEBUG") // level from the flag, dev format (not json)
}

func TestBuildServeLogger_DebugFilePermissions(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	require.NoDirExists(t, sp.Config.LogDir()) // created on demand

	_, closeLog, err := cli.BuildServeLoggerForTest(sp.Config, app.ServeOptions{Debug: true}, io.Discard, debugLaunchTime)
	require.NoError(t, err)
	closeLog()

	info, err := os.Stat(debugLogPath(sp))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestBuildServeLogger_NoDebugKeepsEnvConfig(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	sp.Config.LogLevel = "info"
	sp.Config.LogFormat = "json"

	var stderr bytes.Buffer
	log, closeLog, err := cli.BuildServeLoggerForTest(sp.Config, app.ServeOptions{}, &stderr, debugLaunchTime)
	require.NoError(t, err)

	log.Info("info-line")
	log.Debug("swallowed")
	closeLog()

	require.Contains(t, stderr.String(), `"msg":"info-line"`) // env json format
	require.NotContains(t, stderr.String(), "swallowed")      // env info level
	_, err = os.Stat(sp.Config.LogDir())
	require.ErrorIs(t, err, os.ErrNotExist) // no log dir or file without --debug
}
