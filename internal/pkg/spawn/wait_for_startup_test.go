package spawn_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ivangurin/ccx/internal/pkg/spawn"
	"github.com/stretchr/testify/require"
)

const daemonStartBudget = 2 * time.Second

func writeDaemonLog(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "daemon-20261116-231743.log")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	return path
}

// A child that answers /health within the budget → nil, the done channel
// never matters.
func TestWaitForStartup_Healthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	done := make(chan struct{}) // never closes — the child lives
	child := spawn.NewChild(filepath.Join(t.TempDir(), "daemon.log"), done, nil)

	require.NoError(t, spawn.WaitForStartup(context.Background(), srv.URL, child, t.TempDir(), daemonStartBudget))
}

// A child that dies mid-wait fails FAST — well within the budget — with the
// reason lifted from its log ("daemon exited with error"), not the generic
// timeout: the switch message must name why the daemon did not come up.
func TestWaitForStartup_DeadChildFailsFastWithReason(t *testing.T) {
	dir := t.TempDir()
	logPath := writeDaemonLog(t, dir, `{"time":"2026-11-16T23:17:43.067413+03:00","level":"INFO","msg":"daemon launch","pid":47158}
{"time":"2026-11-16T23:17:43.500318+03:00","level":"ERROR","msg":"`+spawn.DaemonExitedWithErrorMsg+`","err":"app: router: provider copilot1 not ready: copilot: token exchange: status 403: At this time, Copilot is not available in your location."}
`)

	done := make(chan struct{})
	go func() { time.Sleep(20 * time.Millisecond); close(done) }()
	child := spawn.NewChild(logPath, done, errors.New("exit status 1"))

	start := time.Now()
	err := spawn.WaitForStartup(context.Background(), "http://127.0.0.1:1", child, dir, 10*time.Second)
	elapsed := time.Since(start)

	require.ErrorContains(t, err, "copilot: token exchange: status 403")
	require.ErrorContains(t, err, logPath)
	require.NotContains(t, err.Error(), "daemon-*.log", "the dead-child error names the exact file, not the glob")
	require.Less(t, elapsed, 5*time.Second, "a dead child must fail fast, not burn the budget")
}

// The log is opened in APPEND mode and shared by same-second relaunches: the
// reason must be the child's LAST word (the newest line), not the file's
// first error line from a previous launch.
func TestWaitForStartup_DeadChildTakesTheLastReason(t *testing.T) {
	dir := t.TempDir()
	logPath := writeDaemonLog(t, dir, `{"level":"ERROR","msg":"`+spawn.DaemonExitedWithErrorMsg+`","err":"stale reason from the previous launch"}
{"level":"ERROR","msg":"`+spawn.DaemonExitedWithErrorMsg+`","err":"the actual fresh reason"}
`)

	done := make(chan struct{})
	close(done)
	child := spawn.NewChild(logPath, done, errors.New("exit status 1"))

	err := spawn.WaitForStartup(context.Background(), "http://127.0.0.1:1", child, dir, daemonStartBudget)

	require.ErrorContains(t, err, "the actual fresh reason")
	require.NotContains(t, err.Error(), "stale reason")
}

// A dead child whose log carries no "daemon exited with error" line falls
// back to the process wait status.
func TestWaitForStartup_DeadChildWithoutReasonLine(t *testing.T) {
	dir := t.TempDir()
	logPath := writeDaemonLog(t, dir, "{\"level\":\"INFO\",\"msg\":\"daemon launch\"}\n")

	done := make(chan struct{})
	close(done)
	child := spawn.NewChild(logPath, done, errors.New("exit status 1"))

	err := spawn.WaitForStartup(context.Background(), "http://127.0.0.1:1", child, dir, daemonStartBudget)

	require.ErrorContains(t, err, "exit status 1")
	require.ErrorContains(t, err, logPath)
}

// A child that lives but never answers — the budget runs out with the
// generic message plus the log-glob hint (the dead-child error already
// names its exact file, so the glob belongs only here).
func TestWaitForStartup_LiveChildNeverAnswers(t *testing.T) {
	done := make(chan struct{}) // never closes
	child := spawn.NewChild(filepath.Join(t.TempDir(), "daemon.log"), done, nil)

	err := spawn.WaitForStartup(context.Background(), "http://127.0.0.1:1", child, t.TempDir(), 300*time.Millisecond)

	require.ErrorContains(t, err, "did not respond within")
	require.ErrorContains(t, err, "daemon-*.log")
}

// Health answered by SOMEONE ELSE on the machine-global port while our
// child is already dead must NOT read as success — the switch would commit
// settings.json against a foreign daemon. The Done check wins.
func TestWaitForStartup_DeadChildWithHealthyForeignServerIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	logPath := writeDaemonLog(t, dir, `{"level":"ERROR","msg":"`+spawn.DaemonExitedWithErrorMsg+`","err":"bind: address already in use"}
`)
	done := make(chan struct{})
	close(done)
	child := spawn.NewChild(logPath, done, errors.New("exit status 1"))

	err := spawn.WaitForStartup(context.Background(), srv.URL, child, dir, daemonStartBudget)

	require.ErrorContains(t, err, "bind: address already in use", "a dead child must fail even when /health answers")
}
