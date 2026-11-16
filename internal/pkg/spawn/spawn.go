// Package spawn — daemon process utilities shared by the CLI side: the
// detached launch (Setsid, stdout/stderr → daemon-<launch time>.log) and the
// health probes the spawner waits on. A leaf package deliberately: the
// switcher and the service provider consume it, and the daemon runtime
// (internal/app) must be free to consume the provider without an import
// cycle (app → provider → switcher → spawn).
package spawn

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/ivangurin/ccx/internal/config"
)

// Child is a launched daemon child. The startup wait watches Done: a child
// that dies mid-wait fails the launch FAST, with the reason read from its
// log (the child logs "daemon exited with error" before exiting), instead of
// burning the whole budget on a dead process.
type Child struct {
	logPath string
	done    <-chan struct{}
	exitErr error // the process wait status; valid once Done has fired
}

// NewChild builds a handle from raw parts — the test seam for the startup
// wait; SpawnDetached is the production producer.
func NewChild(logPath string, done <-chan struct{}, exitErr error) *Child {
	return &Child{logPath: logPath, done: done, exitErr: exitErr}
}

// Done closes when the child process has exited.
func (c *Child) Done() <-chan struct{} { return c.done }

// ExitErr is the process wait status — call only after Done has fired.
func (c *Child) ExitErr() error { return c.exitErr }

// LogPath is the child's daemon-<launch time>.log.
func (c *Child) LogPath() string { return c.logPath }

// SpawnDetached starts a child daemon: its own session (Setsid — terminal
// death doesn't kill it), stdout/stderr → daemon-<launch time>.log (append —
// a same-second relaunch appends to the same file), stdin → /dev/null.
// args are appended after "serve --daemon" (launch parameters). The returned
// Child carries the death signal: a reaper goroutine waits the process and
// closes Done with the exit status attached.
func SpawnDetached(cfg *config.Config, args ...string) (*Child, error) {
	logPath := filepath.Join(cfg.LogDir(), "daemon-"+time.Now().Format("20060102-150405")+".log")
	if err := os.MkdirAll(cfg.LogDir(), 0o750); err != nil {
		return nil, fmt.Errorf("spawn: log dir: %w", err)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) //nolint:gosec // fixed permissions
	if err != nil {
		return nil, fmt.Errorf("spawn: log file: %w", err)
	}
	defer func() { _ = logFile.Close() }()

	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("spawn: executable: %w", err)
	}
	serveArgs := append([]string{"serve", "--daemon"}, args...)
	cmd := exec.Command(exe, serveArgs...) //nolint:gosec // our own binary
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // detach from the terminal group
	cmd.Env = os.Environ()

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("spawn: start: %w", err)
	}

	done := make(chan struct{})
	child := &Child{logPath: logPath, done: done}
	go func() {
		child.exitErr = cmd.Wait()
		close(done)
	}()

	return child, nil
}

// DaemonExitedWithErrorMsg — the child's last word on a startup failure:
// the message the daemon logs (pinned JSONL) before exiting, and the marker
// the startup-wait reader scans its log for. One constant on both sides —
// rewording one silently disables the reason lift.
const DaemonExitedWithErrorMsg = "daemon exited with error"

// HealthCheck — whether the daemon is alive (any HTTP response = alive; bash semantics parity).
func HealthCheck(baseURL string, timeout time.Duration) bool {
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(baseURL + "/health") //nolint:gosec // local daemon address
	if err != nil {
		return false
	}
	_ = resp.Body.Close()

	return true
}
