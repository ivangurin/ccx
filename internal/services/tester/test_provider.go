package tester

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/ivangurin/ccx/internal/api/handlers"
	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/pkg/httpserver"
	"github.com/ivangurin/ccx/internal/services/router"
)

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
