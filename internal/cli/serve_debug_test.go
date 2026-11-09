package cli_test

import (
	"bytes"
	"os"
	"testing"
	"time"

	"github.com/ivangurin/ccx/internal/app"
	"github.com/ivangurin/ccx/internal/cli"
	suite_provider "github.com/ivangurin/ccx/internal/pkg/suite/provider"
	"github.com/stretchr/testify/require"
)

// debugLaunchTime — a fixed launch time: buildServeLogger ignores it (the
// sink file is the parent's redirect job), but the seam keeps its shape.
var debugLaunchTime = time.Date(2026, 11, 7, 15, 4, 5, 0, time.Local)

func TestBuildServeLogger_DebugOverridesLevelOnly(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	sp.Config.LogLevel = "info" // suite default — the flag must beat it
	sp.Config.LogFormat = "json"

	var stderr bytes.Buffer
	log, closeLog, err := cli.BuildServeLoggerForTest(sp.Config, app.ServeOptions{Debug: true}, &stderr, debugLaunchTime)
	require.NoError(t, err)

	log.Debug("debug-level-line") // level=info would swallow this
	closeLog()

	require.Contains(t, stderr.String(), `"level":"DEBUG"`) // level from the flag, json format (not dev)
	// anti-tee regression: the logger must not create any file — a spawned
	// daemon's log lands in the parent's redirect, a foreground run stays on
	// stderr only
	_, err = os.Stat(sp.Config.LogDir())
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestBuildServeLogger_DebugKeepsEnvFormat(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	sp.Config.LogLevel = "info"
	sp.Config.LogFormat = "dev"

	var stderr bytes.Buffer
	log, closeLog, err := cli.BuildServeLoggerForTest(sp.Config, app.ServeOptions{Debug: true}, &stderr, debugLaunchTime)
	require.NoError(t, err)

	log.Debug("debug-text-line")
	closeLog()

	require.Contains(t, stderr.String(), "level=DEBUG") // dev format honored under --debug
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
	require.ErrorIs(t, err, os.ErrNotExist) // no log dir without --debug
}
