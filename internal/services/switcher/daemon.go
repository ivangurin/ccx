package switcher

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/ivangurin/ccms/internal/app"
	"github.com/ivangurin/ccms/internal/config"
)

// HTTPDaemon — the Daemon implementation on top of the daemon's admin API.
type HTTPDaemon struct {
	cfg *config.Config
}

// NewHTTPDaemon creates the daemon client.
func NewHTTPDaemon(cfg *config.Config) *HTTPDaemon {
	return &HTTPDaemon{cfg: cfg}
}

func (d *HTTPDaemon) baseURL() (string, error) {
	port := d.cfg.DaemonPort
	if port == 0 {
		providers, err := config.LoadProviders(d.cfg.ProvidersPath())
		if err != nil {
			return "", fmt.Errorf("daemon: providers.json: %w", err)
		}
		port = providers.Daemon.Port
	}

	return fmt.Sprintf("http://127.0.0.1:%d", port), nil
}

// Ensure — the daemon is alive; if not: spawn detached + wait for health.
func (d *HTTPDaemon) Ensure(ctx context.Context) error {
	base, err := d.baseURL()
	if err != nil {
		return err
	}
	if app.HealthCheck(base, time.Second) {
		return nil
	}
	if err := app.SpawnDetached(d.cfg); err != nil {
		return err
	}
	if !app.WaitForHealth(ctx, base, d.cfg.DaemonStartTimeout) {
		return fmt.Errorf("daemon did not respond within %s (log: %s)", d.cfg.DaemonStartTimeout, d.cfg.LogDir()+"/daemon.log")
	}

	return nil
}

// Stop — best-effort shutdown of a running daemon: a no-op success if it is
// not running; otherwise POST /admin/stop and wait for the port to close (a
// lingering listener would break the next Ensure's spawn — port conflict).
// Mirrors the ccms stop CLI.
func (d *HTTPDaemon) Stop(ctx context.Context) error {
	base, err := d.baseURL()
	if err != nil {
		return err
	}
	if !app.HealthCheck(base, time.Second) {
		return nil
	}
	token := ""
	if providers, perr := config.LoadProviders(d.cfg.ProvidersPath()); perr == nil {
		token = providers.Daemon.AuthToken
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/admin/stop", nil)
	if err != nil {
		return fmt.Errorf("daemon: stop: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("daemon: stop: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		var payload struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&payload)

		return fmt.Errorf("daemon: stop: %s", payload.Error)
	}

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if !app.HealthCheck(base, 250*time.Millisecond) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("the daemon is still responding (log: %s)", d.cfg.LogDir()+"/daemon.log")
		case <-ticker.C:
		}
	}
}

// Use — POST /admin/use with the daemon token.
func (d *HTTPDaemon) Use(ctx context.Context, profile, model string) error {
	base, err := d.baseURL()
	if err != nil {
		return err
	}
	providers, err := config.LoadProviders(d.cfg.ProvidersPath())
	if err != nil {
		return err
	}

	body, err := json.Marshal(map[string]string{"profile": profile, "model": model})
	if err != nil {
		return fmt.Errorf("daemon: use: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/admin/use", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("daemon: use: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+providers.Daemon.AuthToken)

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req) // Ready may hit an OAuth refresh
	if err != nil {
		return fmt.Errorf("daemon: use: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		var payload struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&payload)

		return fmt.Errorf("%s", payload.Error)
	}

	return nil
}
