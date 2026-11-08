// Package router — the daemon's single route: the provider named at launch
// (--provider) is resolved from providers.json once at startup; the built
// translator serves for the daemon's whole life (switching = daemon restart
// with new launch params, owned by the CLI switcher).
package router

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/ivangurin/ccx/internal/clients/codex"
	copilot_client "github.com/ivangurin/ccx/internal/clients/copilot"
	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/pkg/reqdump"
	"github.com/ivangurin/ccx/internal/translators"
	"github.com/ivangurin/ccx/internal/translators/anthropic"
	codex_tr "github.com/ivangurin/ccx/internal/translators/codex"
	"github.com/ivangurin/ccx/internal/translators/copilot"
	"github.com/ivangurin/ccx/internal/translators/openai"
)

// Service — the daemon's fixed route. Built once by Build before listening;
// immutable afterwards (the daemon serves exactly one provider per process).
type Service struct {
	profile       string
	protocol      models.Protocol
	translator    translators.Translator
	providersPath string // Models() rereads providers.json on demand
}

// Build loads providers.json, resolves the profile by key, builds the
// translator and runs its readiness check. Any failure must abort the daemon
// startup BEFORE listening — /health answering then means "route ready".
func Build(ctx context.Context, cfg *config.Config, log *slog.Logger, providerKey string) (*Service, error) {
	providers, err := config.LoadProviders(cfg.ProvidersPath())
	if err != nil {
		return nil, fmt.Errorf("router: providers.json: %w", err)
	}

	profile := providers.Providers.Get(providerKey)
	if profile == nil {
		return nil, fmt.Errorf("router: unknown provider: %s", providerKey)
	}

	tr, err := buildTranslator(cfg, providerKey, profile)
	if err != nil {
		return nil, err
	}
	if err := tr.Ready(ctx); err != nil {
		_ = tr.Close(ctx)

		return nil, fmt.Errorf("router: provider %s not ready: %w", providerKey, err)
	}
	log.Info("route built", "provider", providerKey, "protocol", profile.Protocol)

	return &Service{
		profile:       providerKey,
		protocol:      profile.Protocol,
		translator:    tr,
		providersPath: cfg.ProvidersPath(),
	}, nil
}

// buildTranslator — the protocol → translator registry. OAuth protocols
// (copilot, codex) draw their credentials from the provider's own store:
// <CCX_HOME>/auth/<key>.json (written by ccx auth <key>).
func buildTranslator(cfg *config.Config, providerKey string, profile *models.Profile) (translators.Translator, error) {
	switch profile.Protocol {
	case models.ProtocolAnthropic:
		if profile.BaseURL == "" {
			return nil, errors.New("profile has no base_url — fill it in via ccx edit")
		}

		return anthropic.New(profile), nil
	case models.ProtocolOpenAI:
		if profile.BaseURL == "" {
			return nil, errors.New("profile has no base_url — fill it in via ccx edit")
		}

		return openai.New(profile), nil
	case models.ProtocolCopilot:
		return copilot.New(profile, copilot_client.New(cfg.AuthStorePath(providerKey))), nil
	case models.ProtocolCodex:
		return codex_tr.New(profile, codex.New(cfg.AuthStorePath(providerKey))), nil
	default:
		return nil, fmt.Errorf("router: unknown protocol %q", profile.Protocol)
	}
}

// Active — (provider key, protocol, whether a route exists; always true for a
// Service returned by Build).
func (s *Service) Active() (provider string, protocol models.Protocol, ok bool) {
	if s == nil || s.translator == nil {
		return "", "", false
	}

	return s.profile, s.protocol, true
}

// ServeMessages delegates /v1/messages to the route's translator. The raw
// client body is peeked for an opt-in CCX_DUMP_DIR dump before delegation:
// the exact payload a real client sends is the only reproduction for some
// upstream bugs (see internal/pkg/reqdump).
func (s *Service) ServeMessages(w http.ResponseWriter, r *http.Request) {
	if reqdump.Enabled() {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeReadErr(w)

			return
		}
		reqdump.Write("anthropic-req", body)
		r.Body = io.NopCloser(bytes.NewReader(body))
	}
	s.translator.ServeMessages(w, r)
}

// ServeCountTokens delegates /v1/messages/count_tokens (same dump treatment:
// its payload mirrors /v1/messages).
func (s *Service) ServeCountTokens(w http.ResponseWriter, r *http.Request) {
	if reqdump.Enabled() {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeReadErr(w)

			return
		}
		reqdump.Write("anthropic-count-tokens", body)
		r.Body = io.NopCloser(bytes.NewReader(body))
	}
	s.translator.ServeCountTokens(w, r)
}

// writeReadErr — a half-read body cannot be re-delivered; fail loudly rather
// than hand the translator a truncated stream.
func writeReadErr(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"read request body"}}`))
}

// Models — /v1/models from the launched profile's models.
func (s *Service) Models() []models.Model {
	providers, err := config.LoadProviders(s.providersPath)
	if err != nil {
		return nil
	}
	if p := providers.Providers.Get(s.profile); p != nil {
		return p.Models
	}

	return nil
}

// Close quenches the translator.
func (s *Service) Close(ctx context.Context) error {
	return s.translator.Close(ctx)
}
