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

// SpawnDetached поднимает демон-ребёнка: своя сессия (Setsid — смерть
// терминала не убивает), stdout/stderr → daemon.log, stdin → /dev/null.
func SpawnDetached(cfg *config.Config) error {
	logPath := filepath.Join(cfg.LogDir(), "daemon.log")
	if err := os.MkdirAll(cfg.LogDir(), 0o750); err != nil {
		return fmt.Errorf("spawn: лог-каталог: %w", err)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) //nolint:gosec // фиксированные права
	if err != nil {
		return fmt.Errorf("spawn: лог-файл: %w", err)
	}
	defer func() { _ = logFile.Close() }()

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("spawn: executable: %w", err)
	}
	cmd := exec.Command(exe, "serve", "--daemon") //nolint:gosec // наш собственный бинарник
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // отцепиться от группы терминала
	cmd.Env = os.Environ()

	return cmd.Start()
}

// HealthCheck — жив ли демон (любой HTTP-ответ = жив; паритет bash-семантики).
func HealthCheck(baseURL string, timeout time.Duration) bool {
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(baseURL + "/health") //nolint:gosec // локальный адрес демона
	if err != nil {
		return false
	}
	_ = resp.Body.Close()

	return true
}

// WaitForHealth поллит /health до таймаута.
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
