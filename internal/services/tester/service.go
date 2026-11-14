// Package tester — the ccx test engine: a per-provider probe that validates
// providers.json structurally, then lifts a throwaway daemon on a free port
// (router.Build + the full HTTP layer) and fires one short "hi" probe per
// model. The result is a three-level report: provider → models → per-model
// errors. settings.json is never touched; the daemon is stopped after each
// provider.
//
// One operation per file: tester.go (Run + report types + its selection and
// tally helpers), test_provider.go, validate_profile.go, probe_model.go,
// extract_message.go, probe_id.go, credential.go; render.go is the live-view
// renderer.
package tester

import (
	"context"
	"fmt"

	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/models"
)

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

// Service — the test engine over one config (the sandbox in tests, the env
// in production): Run probes providers through a temporary daemon.
type Service struct {
	cfg *config.Config
}

// NewService builds the tester.
func NewService(cfg *config.Config) *Service {
	return &Service{cfg: cfg}
}

// Run validates providers.json and probes every (or the selected) provider
// and its models through a temporary daemon. The providers file is loaded
// once here; every failure mode lands in the report, never panics Run.
func (s *Service) Run(ctx context.Context, opts Options) *Report {
	report := &Report{}
	live := &Live{}
	notify := func() {
		if opts.OnUpdate != nil {
			opts.OnUpdate(live)
		}
	}

	providers, err := config.LoadProviders(s.cfg.ProvidersPath())
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

	keys, profiles := selectKeys(providers, opts.Provider)

	live.Total = len(keys)
	for _, key := range keys {
		profile := profiles[key]
		res := ProviderResult{Key: key}
		if profile != nil {
			res.Protocol = profile.Protocol
		}
		live.Providers = append(live.Providers, &res)
		notify()
		finish := testProvider(ctx, s.cfg, opts, key, profile, &res, notify)
		tally(report, finish)
		live.Done++
		notify()
	}

	return report
}

// selectKeys — the provider keys Run covers: all of them, or just the
// --provider filter's key. A filter naming an unknown key must not produce
// an empty selection: it is surfaced the same way an unknown provider fails.
func selectKeys(providers *models.Providers, filter string) ([]string, map[string]*models.Profile) {
	keys := []string{}
	profiles := map[string]*models.Profile{}
	for _, key := range providers.Providers.Keys {
		if filter != "" && key != filter {
			continue
		}
		keys = append(keys, key)
		profiles[key] = providers.Providers.Get(key)
	}
	if len(keys) == 0 && filter != "" {
		keys = []string{filter}
	}

	return keys, profiles
}

// tally — fold one finished provider result into the report counters.
func tally(report *Report, res *ProviderResult) {
	report.Providers = append(report.Providers, *res)
	report.TotalProviders++
	report.TotalModels += len(res.Models)
	if res.Err == nil {
		report.ProvidersOK++
	}
	for _, m := range res.Models {
		if m.Err == nil {
			report.ModelsOK++
		}
	}
}
