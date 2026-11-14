// Package suite_provider — the test composition root, the mirror of the
// production internal/provider over a sandboxed CCX_HOME (the reference
// suite_provider shape): lazy GetX getters grouped per concern
// (clients.go, services.go), real production objects built over mocks at
// the leaves — tests wire expectations on the leaf mock and drive the real
// service (sp.GetDaemonMock().EXPECT()... → sp.GetSwitcherService()).
// The live ~/.claude is unreachable by construction (an invariant after the
// CCR incidents in bash-ccx: tests never touch real settings).
//
// Deliberate difference from the reference: NewProvider takes *testing.T —
// the sandbox (t.TempDir + t.Setenv + t.Cleanup) is what keeps the live
// settings unreachable; the reference's bare NewProvider() has no
// environment to sandbox away.
package suite_provider

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/pkg/logger"
)

// Provider — the test environment: a sandboxed config + a silent logger +
// lazy client/service seams (clients.go, services.go).
type Provider struct {
	t      *testing.T
	ctx    context.Context
	Config *config.Config
	log    *slog.Logger

	mu       sync.Mutex
	clients  clients
	services services

	cleanups []func()
}

// NewProvider builds the sandbox: a unique CCX_HOME per call (t.Setenv —
// tests must not be parallel); the return value is the provider itself
// (cleanup is registered with t.Cleanup).
func NewProvider(t *testing.T) *Provider {
	t.Helper()

	home := t.TempDir()
	t.Setenv("CCX_HOME", home)
	// settings.json is sandboxed separately: its production default lives
	// in the real ~/.claude (outside CCX_HOME) — without this pin the live
	// settings would leak into tests
	t.Setenv("CCX_SETTINGS", filepath.Join(home, "settings.json"))

	cfg, err := InitConfig()
	if err != nil {
		t.Fatalf("suite provider: config: %v", err)
	}

	log, err := logger.New(os.Stderr, cfg.LogLevel, cfg.LogFormat)
	if err != nil {
		t.Fatalf("suite provider: logger: %v", err)
	}

	sp := &Provider{
		t:      t,
		ctx:    context.Background(),
		Config: cfg,
		log:    log,
	}
	t.Cleanup(sp.Cleaner)

	return sp
}

// Context returns the base test context.
func (p *Provider) Context() context.Context {
	return p.ctx
}

// GetLogger returns the silent logger (error level).
func (p *Provider) GetLogger() *slog.Logger {
	return p.log
}

// HomeDir — the sandbox root (CCX_HOME).
func (p *Provider) HomeDir() string {
	return p.Config.Home
}

// FilePath — the path to a file inside the sandbox.
func (p *Provider) FilePath(parts ...string) string {
	return filepath.Join(append([]string{p.Config.Home}, parts...)...)
}

// Cleaner runs the registered cleanups in reverse order.
func (p *Provider) Cleaner() {
	p.mu.Lock()
	fns := p.cleanups
	p.cleanups = nil
	p.mu.Unlock()
	for i := len(fns) - 1; i >= 0; i-- {
		fns[i]()
	}
}
