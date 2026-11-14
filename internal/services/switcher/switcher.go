// Package switcher — profile switching: a port of bash-ccx apply_profile.
// Invariant: first readiness of everything the profile needs, only then the
// settings.json edit; on failure ⇒ files are untouched, the user stays on
// the previous profile. For translated protocols (openai/copilot/codex) that
// means a daemon restart with launch params (stop → spawn --provider/--port
// → health wait — /health answering means Ready() passed); an anthropic
// passthrough goes direct — the upstream URL and credential land in
// settings.json (bash-ccx semantics), the unused daemon is stopped
// best-effort.
//
// One operation per file: apply.go (the flock discipline), apply_locked.go
// (the locked orchestration), rollback_daemon.go, apply_direct.go,
// pin_model.go, write_settings.go, write_state.go; this file keeps the type
// and its constructor.
package switcher

import (
	"context"

	"github.com/ivangurin/ccx/internal/config"
)

// Daemon — the daemon contract for switching (the implementation is an
// HTTP client + detached spawn; tests substitute a mock).
type Daemon interface {
	// Restart — stop any running daemon, spawn a fresh one with the launch
	// params and wait for readiness (health == translator Ready()). The
	// params carry the providers.json daemon block (--port/--debug): the
	// daemon never reads that block itself.
	Restart(ctx context.Context, providerKey string, port int, debug bool) error
	// Stop — best-effort shutdown of a running daemon (a direct anthropic
	// profile doesn't need it); a no-op success if it is not running.
	Stop(ctx context.Context) error
}

// Switcher — applies a profile to settings.json under a lock.
type Switcher struct {
	cfg    *config.Config
	daemon Daemon
}

// New creates a Switcher.
func New(cfg *config.Config, daemon Daemon) *Switcher {
	return &Switcher{cfg: cfg, daemon: daemon}
}
