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
// it; passing it here keeps the import cycle away). OnUpdate, when set,
// fires after every visible state change so the caller can repaint a live
// view (the run is sequential — no locking needed around the state).
type Options struct {
	Provider string
	Version  string
	OnUpdate func(*Live)
}

// Report is the aggregated result of one Run.
type Report struct {
	Providers []ProviderResult

	TotalProviders int
	ProvidersOK    int
	TotalModels    int
	ModelsOK       int
}

// Status is the probe state of a provider or model: running (the text
// glyph), passed or failed.
type Status int

const (
	StatusRunning Status = iota
	StatusOK
	StatusFail
)

// ProviderResult — level 1 (the provider) + level 2 (its models or the
// provider-level error).
type ProviderResult struct {
	Key      string
	Protocol models.Protocol
	Status   Status
	Err      error // non-nil when Status == StatusFail: structural failure, not-ready, daemon startup
	Models   []ModelResult
}

// ModelResult — level 2 (the model) + level 3 (its probe error).
type ModelResult struct {
	ID     string
	Status Status
	Err    error // non-nil when Status == StatusFail: HTTP status, network, timeout
}

// Live is the mutable state the OnUpdate callback observes: the growing
// list of provider results (statuses mutate in place as probes complete).
type Live struct {
	Providers []*ProviderResult
	Done      int // providers finished (ok or failed) — feeds the progress bar
	Total     int
}

// Run validates providers.json and probes every (or the selected) provider
// and its models through a temporary daemon. The providers file is loaded
// once here; every failure mode lands in the report, never panics Run.
func Run(ctx context.Context, cfg *config.Config, opts Options) *Report {
	report := &Report{}
	live := &Live{}
	notify := func() {
		if opts.OnUpdate != nil {
			opts.OnUpdate(live)
		}
	}

	providers, err := config.LoadProviders(cfg.ProvidersPath())
	if err != nil {
		report.Providers = []ProviderResult{{
			Key:    "providers.json",
			Status: StatusFail,
			Err:    fmt.Errorf("%w — run ccx init first", err),
		}}
		report.TotalProviders = 1
		live.Providers = []*ProviderResult{&report.Providers[0]}
		live.Total, live.Done = 1, 1
		notify()

		return report
	}

	keys := []string{}
	profiles := map[string]*models.Profile{}
	for _, key := range providers.Providers.Keys {
		if opts.Provider != "" && key != opts.Provider {
			continue
		}
		keys = append(keys, key)
		profiles[key] = providers.Providers.Get(key)
	}

	// A --provider filter naming an unknown key must not print an empty
	// report: surface it the same way an unknown provider would fail.
	if len(keys) == 0 && opts.Provider != "" {
		keys = []string{opts.Provider}
	}

	live.Total = len(keys)
	for _, key := range keys {
		profile := profiles[key]
		res := ProviderResult{Key: key}
		if profile != nil {
			res.Protocol = profile.Protocol
		}
		live.Providers = append(live.Providers, &res)
		notify()
		finish := testProvider(ctx, cfg, opts, key, profile, &res, notify)
		report.Providers = append(report.Providers, *finish)
		report.TotalProviders++
		report.TotalModels += len(finish.Models)
		if finish.Err == nil {
			report.ProvidersOK++
		}
		for _, m := range finish.Models {
			if m.Err == nil {
				report.ModelsOK++
			}
		}
		live.Done++
		notify()
	}

	return report
}

// testProvider — one provider: structural validation → daemon on a free
// port → model probes → stop. Any level-1 failure skips the probes. The
// result mutates res in place (the live view sees statuses as they land)
// and is returned complete.
func testProvider(
	ctx context.Context, cfg *config.Config, opts Options,
	key string, profile *models.Profile, res *ProviderResult, notify func(),
) *ProviderResult {
	if profile == nil {
		res.Status = StatusFail
		res.Err = fmt.Errorf("unknown provider: %s", key)
		notify()

		return res
	}
	// env references in the credential fields resolve before anything else:
	// a missing variable is a provider-level failure (no daemon lift, no
	// probe firing — the fa8099f structural-check semantics), and every
	// later phase (validateProfile, the probe's Bearer) sees the expanded
	// secret, never a literal "$NAME".
	if err := config.ExpandCredentials(profile); err != nil {
		res.Status = StatusFail
		res.Err = err
		notify()

		return res
	}
	if err := validateProfile(profile); err != nil {
		res.Status = StatusFail
		res.Err = err
		notify()

		return res
	}

	svc, err := router.Build(ctx, cfg, slog.New(slog.DiscardHandler), key)
	if err != nil {
		res.Status = StatusFail
		res.Err = err
		notify()

		return res
	}

	srv, err := httpserver.New("127.0.0.1:0", handlers.New(handlers.Deps{
		Router:  svc,
		Version: opts.Version,
		Port:    0,
		Stop:    func() {},
	}))
	if err != nil {
		res.Status = StatusFail
		res.Err = fmt.Errorf("daemon: %w", err)
		notify()

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

	// the provider cleared its checks — its models become visible (each
	// starts as running) before the first probe fires
	for _, m := range profile.Models {
		res.Models = append(res.Models, ModelResult{ID: m.ID, Status: StatusRunning})
	}
	notify()

	for i := range res.Models {
		res.Models[i].Err = probeModel(ctx, srv.Addr(), profile, probeID(profile.Protocol, res.Models[i].ID))
		if res.Models[i].Err != nil {
			res.Models[i].Status = StatusFail
		} else {
			res.Models[i].Status = StatusOK
		}
		notify()
	}
	res.Status = StatusOK

	return res
}

// validateProfile — the structural phase (no network): protocol known,
// base_url present for URL-based protocols, a credential where the daemon
// holds none of its own (openai is pass-through — a probe without a token
// is guaranteed 401, testing it only burns a round-trip), models listed
// and sane.
func validateProfile(p *models.Profile) error {
	switch p.Protocol {
	case models.ProtocolAnthropic, models.ProtocolOpenAI:
		if p.BaseURL == "" {
			return errors.New("base_url is empty")
		}
		if p.AuthToken == "" && p.APIKey == "" {
			return errors.New("auth_token is empty — no credential to probe with")
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
		return fmt.Errorf("upstream %d: %s", resp.StatusCode, extractMessage(raw))
	}

	return nil
}

// extractMessage pulls the human-readable message out of an Anthropic-style
// error body ({...,"error":{"message":"..."}}) or an OpenAI-style one
// ({...,"error":{"message"...}} — same key): the raw JSON on the failure
// line is noise (request ids, codes the user cannot act on). Falls back to
// the trimmed body when it is not JSON or carries no message.
func extractMessage(raw []byte) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return "empty error body"
	}
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(trimmed), &body); err == nil && body.Error.Message != "" {
		return body.Error.Message
	}

	return trimmed
}

// probeID adapts the configured model id to what the upstream accepts:
// a trailing "[…]" bracket suffix (Claude Code's [1m] 1M-context marker)
// is a CLIENT-side convention — z.ai rejects "glm-5.3[1m]" with 1211
// Unknown Model (verified live 2026-11-09: the same id passed earlier that
// day, the catalog lists only bare ids). Only anthropic profiles are
// stripped: other protocols never carried bracket ids.
func probeID(protocol models.Protocol, id string) string {
	if protocol != models.ProtocolAnthropic {
		return id
	}
	if i := strings.IndexByte(id, '['); i > 0 {
		return id[:i]
	}

	return id
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
