package app

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/ivangurin/ccx/internal/config"
)

// SpawnDetached starts a child daemon: its own session (Setsid — terminal
// death doesn't kill it), stdout/stderr → daemon.log, stdin → /dev/null.
// args are appended after "serve --daemon" (launch parameters).
func SpawnDetached(cfg *config.Config, args ...string) error {
	logPath := filepath.Join(cfg.LogDir(), "daemon.log")
	if err := os.MkdirAll(cfg.LogDir(), 0o750); err != nil {
		return fmt.Errorf("spawn: log dir: %w", err)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) //nolint:gosec // fixed permissions
	if err != nil {
		return fmt.Errorf("spawn: log file: %w", err)
	}
	defer func() { _ = logFile.Close() }()

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("spawn: executable: %w", err)
	}
	serveArgs := append([]string{"serve", "--daemon"}, args...)
	cmd := exec.Command(exe, serveArgs...) //nolint:gosec // our own binary
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // detach from the terminal group
	cmd.Env = os.Environ()

	return cmd.Start()
}

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

// WaitForHealth polls /health until the timeout.
func WaitForHealth(ctx context.Context, baseURL string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if HealthCheck(baseURL, time.Second) {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(200 * time.Millisecond):
		}
	}

	return false
}
