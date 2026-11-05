package switcher

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/ivangurin/ccx/internal/app"
	"github.com/ivangurin/ccx/internal/config"
)

// HTTPDaemon — реализация Daemon поверх admin-API демона.
type HTTPDaemon struct {
	cfg *config.Config
}

// NewHTTPDaemon создаёт клиент демона.
func NewHTTPDaemon(cfg *config.Config) *HTTPDaemon {
	return &HTTPDaemon{cfg: cfg}
}

func (d *HTTPDaemon) baseURL() (string, error) {
	port := d.cfg.DaemonPort
	if port == 0 {
		providers, err := config.LoadProviders(d.cfg.ProvidersPath())
		if err != nil {
			return "", fmt.Errorf("демон: providers.json: %w", err)
		}
		port = providers.Daemon.Port
	}

	return fmt.Sprintf("http://127.0.0.1:%d", port), nil
}

// Ensure — демон жив; если нет: spawn detached + ожидание health.
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
		return fmt.Errorf("демон не ответил за %s (лог: %s)", d.cfg.DaemonStartTimeout, d.cfg.LogDir()+"/daemon.log")
	}

	return nil
}

// Use — POST /admin/use с токеном демона.
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
		return fmt.Errorf("демон: use: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/admin/use", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("демон: use: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+providers.Daemon.AuthToken)

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req) // Ready может ходить в OAuth-рефреш
	if err != nil {
		return fmt.Errorf("демон: use: %w", err)
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
