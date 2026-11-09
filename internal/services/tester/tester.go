// Package tester — the ccx test engine: a per-provider probe that validates
// providers.json structurally, then lifts a throwaway daemon on a free port
// (router.Build + the full HTTP layer) and fires one short "hi" probe per
// model. The result is a three-level report: provider → models → per-model
// errors. settings.json is never touched; the daemon is stopped after each
// provider.
package tester

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/ivangurin/ccx/internal/api/handlers"
	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/pkg/httpserver"
	"github.com/ivangurin/ccx/internal/services/router"
)

// probeTimeout bounds one model probe (the request includes the daemon
// startup budget too — the probe is the only thing that waits).
const probeTimeout = 30 * time.Second

// Options narrows the run: a non-empty Provider tests just that key.
// Version is the binary version string, reported by /admin/health (cli owns
// it; passing it here keeps the import cycle away).
type Options struct {
	Provider string
	Version  string
}

// Report is the aggregated result of one Run.
type Report struct {
	Providers []ProviderResult

	TotalProviders int
	ProvidersOK    int
	TotalModels    int
	ModelsOK       int
}

// ProviderResult — level 1 (the provider) + level 2 (its models or the
// provider-level error).
type ProviderResult struct {
	Key      string
	Protocol models.Protocol
	Err      error // non-nil: structural failure, not-ready, daemon startup — models are not probed
	Models   []ModelResult
}

// ModelResult — level 2 (the model) + level 3 (its probe error).
type ModelResult struct {
	ID  string
	Err error // non-nil: the probe failed (HTTP status, network, timeout)
}

// Run validates providers.json and probes every (or the selected) provider
// and its models through a temporary daemon. The providers file is loaded
// once here; every failure mode lands in the report, never panics Run.
func Run(ctx context.Context, cfg *config.Config, opts Options) *Report {
	report := &Report{}

	providers, err := config.LoadProviders(cfg.ProvidersPath())
	if err != nil {
		report.Providers = []ProviderResult{{
			Key: "providers.json",
			Err: fmt.Errorf("%w — run ccx init first", err),
		}}
		report.TotalProviders = 1

		return report
	}

	for _, key := range providers.Providers.Keys {
		if opts.Provider != "" && key != opts.Provider {
			continue
		}
		profile := providers.Providers.Get(key)
		res := testProvider(ctx, cfg, opts, key, profile)
		report.Providers = append(report.Providers, res)
		report.TotalProviders++
		report.TotalModels += len(profile.Models)
		if res.Err == nil {
			report.ProvidersOK++
		}
		for _, m := range res.Models {
			if m.Err == nil {
				report.ModelsOK++
			}
		}
	}

	// A --provider filter naming an unknown key must not print an empty
	// report: surface it the same way an unknown provider would fail.
	if opts.Provider != "" && report.TotalProviders == 0 {
		report.Providers = []ProviderResult{{
			Key: opts.Provider,
			Err: fmt.Errorf("unknown provider: %s", opts.Provider),
		}}
		report.TotalProviders = 1
	}

	return report
}

// testProvider — one provider: structural validation → daemon on a free
// port → model probes → stop. Any level-1 failure skips the probes.
func testProvider(ctx context.Context, cfg *config.Config, opts Options, key string, profile *models.Profile) ProviderResult {
	res := ProviderResult{Key: key, Protocol: profile.Protocol}
	if err := validateProfile(profile); err != nil {
		res.Err = err

		return res
	}

	svc, err := router.Build(ctx, cfg, slog.New(slog.DiscardHandler), key)
	if err != nil {
		res.Err = err

		return res
	}

	srv, err := httpserver.New("127.0.0.1:0", handlers.New(handlers.Deps{
		Router:  svc,
		Version: opts.Version,
		Port:    0,
		Stop:    func() {},
	}))
	if err != nil {
		res.Err = fmt.Errorf("daemon: %w", err)

		return res
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- srv.Start() }()
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Stop(stopCtx)
		// Start returns nil after Stop; drain so the goroutine never leaks
		// a value nobody reads (the error is meaningless after a forced
		// shutdown — the probes already recorded their outcomes)
		serveErr := <-serveDone
		_ = serveErr
	}()

	for _, m := range profile.Models {
		res.Models = append(res.Models, ModelResult{
			ID:  m.ID,
			Err: probeModel(ctx, srv.Addr(), profile, m.ID),
		})
	}

	return res
}

// validateProfile — the structural phase (no network): protocol known,
// base_url present for URL-based protocols, models listed and sane.
func validateProfile(p *models.Profile) error {
	switch p.Protocol {
	case models.ProtocolAnthropic, models.ProtocolOpenAI:
		if p.BaseURL == "" {
			return errors.New("base_url is empty")
		}
	case models.ProtocolCopilot, models.ProtocolCodex:
		// credentials live in the auth store — Ready() covers them
	default:
		return fmt.Errorf("unknown protocol %q", p.Protocol)
	}
	if len(p.Models) == 0 {
		return errors.New("no models listed")
	}
	seen := map[string]bool{}
	for _, m := range p.Models {
		if m.ID == "" {
			return errors.New("a model has no id")
		}
		if seen[m.ID] {
			return fmt.Errorf("duplicate model id: %s", m.ID)
		}
		seen[m.ID] = true
	}

	return nil
}

// probeModel fires one short non-stream messages request through the live
// daemon (the same path Claude Code uses, credential included) and returns
// nil on an HTTP 200.
func probeModel(ctx context.Context, addr string, profile *models.Profile, modelID string) error {
	pctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	body, err := json.Marshal(map[string]any{
		"model":      modelID,
		"max_tokens": 16,
		"messages":   []map[string]string{{"role": "user", "content": "hi"}},
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(pctx, http.MethodPost,
		"http://"+addr+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+credential(profile))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("probe failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("upstream %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	return nil
}

// credential — the client credential the daemon expects (daemonEnv
// precedence: auth_token > api_key > the "ccx" placeholder).
func credential(p *models.Profile) string {
	switch {
	case p.AuthToken != "":
		return p.AuthToken
	case p.APIKey != "":
		return p.APIKey
	}

	return "ccx"
}
