// Package suite_provider — a test counterpart of the production composition
// (a port of the idp-port-forward pattern): a sandboxed CCX_HOME (t.TempDir),
// a silent logger, lazy services/mocks on top — a single assembly point of
// fixtures for all tests. The live ~/.claude is unreachable by construction
// (an invariant after the CCR incidents in bash-ccx: tests never touch real
// settings).
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

// Provider — the test environment: a config with safe defaults + a silent
// logger + lazy dependencies (added package by package as the project grows).
type Provider struct {
	ctx    context.Context
	Config *config.Config
	log    *slog.Logger

	mu       sync.Mutex
	cleanups []func()
}

// NewProvider builds the sandbox: a unique CCX_HOME per call (t.Setenv —
// tests must not be parallel); the return value is the provider itself.
func NewProvider(t *testing.T) *Provider {
	t.Helper()

	home := t.TempDir()
	t.Setenv("CCX_HOME", home)

	cfg, err := config.NewFromEnv()
	if err != nil {
		t.Fatalf("suite provider: config: %v", err)
	}
	// safe test defaults: short timeouts, silent log
	cfg.LogLevel = "error"
	cfg.LogFormat = "json"

	log, err := logger.New(os.Stderr, cfg.LogLevel, cfg.LogFormat)
	if err != nil {
		t.Fatalf("suite provider: logger: %v", err)
	}

	sp := &Provider{
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

// Logger returns the silent logger (error level).
func (p *Provider) Logger() *slog.Logger {
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
