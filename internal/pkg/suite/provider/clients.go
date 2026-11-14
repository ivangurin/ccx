package suite_provider

import (
	"github.com/ivangurin/ccx/internal/services/switcher"
	switcher_mocks "github.com/ivangurin/ccx/internal/services/switcher/mocks"
	codex_tr "github.com/ivangurin/ccx/internal/translators/codex"
	codex_mocks "github.com/ivangurin/ccx/internal/translators/codex/mocks"
	copilot_tr "github.com/ivangurin/ccx/internal/translators/copilot"
	copilot_mocks "github.com/ivangurin/ccx/internal/translators/copilot/mocks"
)

// clients — the lazy client seams: mocks at the leaves. A real HTTPDaemon
// spawns processes and the OAuth clients hit real upstreams — the unit tier
// substitutes the mocks (idp-kube's GetKubeClientMock/GetKubeClient pair);
// the real implementations are built by hand in the daemon/cluster-tier
// tests that own them.
type clients struct {
	daemon           switcher.Daemon
	daemonMock       *switcher_mocks.Daemon
	codexAuth        codex_tr.AuthSource
	codexAuthMock    *codex_mocks.AuthSource
	copilotToken     copilot_tr.TokenSource
	copilotTokenMock *copilot_mocks.TokenSource
}

// GetDaemonMock — the daemon mock (lazy singleton), bare by default: tests
// wire their own EXPECT()s (RunAndReturn closures for call recording —
// mock.MatchBy does not compile with this testify, the idp-kube recipe).
func (p *Provider) GetDaemonMock() *switcher_mocks.Daemon {
	if p.clients.daemonMock == nil {
		p.clients.daemonMock = switcher_mocks.NewDaemon(p.t)
	}

	return p.clients.daemonMock
}

// GetDaemon — the daemon seam; the mock by default.
func (p *Provider) GetDaemon() switcher.Daemon {
	if p.clients.daemon == nil {
		p.clients.daemon = p.GetDaemonMock()
	}

	return p.clients.daemon
}

// GetCodexAuthMock — the codex client seam (the AuthSource the codex
// translator consumes; the real implementation is clients/codex.Client).
func (p *Provider) GetCodexAuthMock() *codex_mocks.AuthSource {
	if p.clients.codexAuthMock == nil {
		p.clients.codexAuthMock = codex_mocks.NewAuthSource(p.t)
	}

	return p.clients.codexAuthMock
}

// GetCodexAuth — the codex client seam; the mock by default.
func (p *Provider) GetCodexAuth() codex_tr.AuthSource {
	if p.clients.codexAuth == nil {
		p.clients.codexAuth = p.GetCodexAuthMock()
	}

	return p.clients.codexAuth
}

// GetCopilotTokenMock — the copilot client seam (the TokenSource the
// copilot translator consumes; the real implementation is
// clients/copilot.Client).
func (p *Provider) GetCopilotTokenMock() *copilot_mocks.TokenSource {
	if p.clients.copilotTokenMock == nil {
		p.clients.copilotTokenMock = copilot_mocks.NewTokenSource(p.t)
	}

	return p.clients.copilotTokenMock
}

// GetCopilotToken — the copilot client seam; the mock by default.
func (p *Provider) GetCopilotToken() copilot_tr.TokenSource {
	if p.clients.copilotToken == nil {
		p.clients.copilotToken = p.GetCopilotTokenMock()
	}

	return p.clients.copilotToken
}
