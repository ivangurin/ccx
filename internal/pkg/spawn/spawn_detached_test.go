package spawn_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/pkg/spawn"
	"github.com/stretchr/testify/require"
)

// TestMain — the helper-process guard: SpawnDetached re-runs this very test
// binary as the child; without the guard the child would run the test suite
// again (and spawn its own children — a fork bomb). With the env set the
// child exits fast with a non-zero status, which is exactly the payload the
// reaper test needs.
func TestMain(m *testing.M) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") == "1" {
		os.Exit(3)
	}
	m.Run() // the runner propagates the exit code automatically (Go 1.15+)
}

// The production Child producer end-to-end: SpawnDetached launches a child
// (our own test binary in helper mode — it dies in milliseconds with status
// 3), the reaper goroutine fires Done with the wait status attached, and
// WaitForStartup fast-fails with that status and the log path. The log file
// materializes with the daemon-<launch time>.log naming (O_APPEND).
func TestSpawnDetached_ReaperFiresOnChildDeath(t *testing.T) {
	t.Setenv("GO_WANT_HELPER_PROCESS", "1")
	cfg := &config.Config{Home: t.TempDir()} // LogDir is pinned to <Home>/logs

	child, err := spawn.SpawnDetached(cfg, "--definitely-not-a-flag")
	require.NoError(t, err)
	require.Contains(t, filepath.Base(child.LogPath()), "daemon-")

	select {
	case <-child.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("the child died but Done never fired")
	}
	require.Error(t, child.ExitErr(), "the helper's exit status is the wait status")

	err = spawn.WaitForStartup(context.Background(), "http://127.0.0.1:1", child, cfg.LogDir(), 5*time.Second)
	require.ErrorContains(t, err, child.LogPath())
	require.ErrorContains(t, err, "exit status 3")
	require.ErrorContains(t, err, "daemon exited during startup")
}
