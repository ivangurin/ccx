// Package router — the daemon's active route: profile → translator.
// Use(): reread providers.json → build a translator by protocol →
// Ready() → atomic swap (in-flight requests stay on the old one) → Close
// the old one. The daemon is stateless regarding user files: the route is
// restored from ccms-state.json (read-only) at startup.
package router

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/ivangurin/ccms/internal/clients/codex"
	copilot_client "github.com/ivangurin/ccms/internal/clients/copilot"
	"github.com/ivangurin/ccms/internal/config"
	"github.com/ivangurin/ccms/internal/models"
	"github.com/ivangurin/ccms/internal/pkg/jsonfile"
	"github.com/ivangurin/ccms/internal/translators"
	"github.com/ivangurin/ccms/internal/translators/anthropic"
	codex_tr "github.com/ivangurin/ccms/internal/translators/codex"
	"github.com/ivangurin/ccms/internal/translators/copilot"
	"github.com/ivangurin/ccms/internal/translators/openai"
)

// route — a profile+model+translator bundle; immutable once installed.
type route struct {
	profile    string
	model      string
	translator translators.Translator
}

// Service — the daemon's router.
type Service struct {
	cfg     *config.Config
	log     *slog.Logger
	active  atomic.Pointer[route]
	authTok atomic.Pointer[string]
}

// New creates the router service; the daemon's client token is loaded right
// away (the auth middleware works before the first Use).
func New(cfg *config.Config, log *slog.Logger) *Service {
	s := &Service{cfg: cfg, log: log}
	s.authTok.Store(new(string))
	if providers, err := config.LoadProviders(cfg.ProvidersPath()); err == nil {
		token := providers.Daemon.AuthToken
		s.authTok.Store(&token)
	} else {
		log.Warn("providers.json unavailable at daemon startup", "err", err)
	}

	return s
}

// Use switches the active profile (rereading providers.json).
func (s *Service) Use(ctx context.Context, profileKey, model string) error {
	providers, err := config.LoadProviders(s.cfg.ProvidersPath())
	if err != nil {
		return fmt.Errorf("router: providers.json: %w", err)
	}
	token := providers.Daemon.AuthToken
	s.authTok.Store(&token)

	profile := providers.Profiles.Get(profileKey)
	if profile == nil {
		return fmt.Errorf("router: unknown profile: %s", profileKey)
	}

	tr, err := buildTranslator(s.cfg, profile)
	if err != nil {
		return err
	}
	if err := tr.Ready(ctx); err != nil {
		_ = tr.Close(ctx)

		return fmt.Errorf("router: profile %s not ready: %w", profileKey, err)
	}

	old := s.active.Swap(&route{profile: profileKey, model: model, translator: tr})
	if old != nil {
		_ = old.translator.Close(ctx) // in-flight requests already hold their own reference
	}
	s.log.Info("route activated", "profile", profileKey, "model", model, "protocol", profile.Protocol)

	return nil
}

// buildTranslator — the protocol → translator registry.
func buildTranslator(cfg *config.Config, profile *models.Profile) (translators.Translator, error) {
	switch profile.Protocol {
	case models.ProtocolAnthropic:
		if profile.Anthropic == nil {
			return nil, errors.New("profile has no anthropic block (base_url) — fill it in via ccms edit")
		}

		return anthropic.New(profile), nil
	case models.ProtocolOpenAI:
		if profile.OpenAI == nil {
			return nil, errors.New("profile has no openai block (base_url) — fill it in via ccms edit")
		}

		return openai.New(profile), nil
	case models.ProtocolCopilot:
		store := filepath.Join(cfg.CCMSDir(), "copilot-auth.json")

		return copilot.New(profile, copilot_client.New(store)), nil
	case models.ProtocolCodex:
		authPath := codex.DefaultAuthPath()
		if profile.Codex != nil && profile.Codex.AuthFile != "" {
			authPath = expandHome(profile.Codex.AuthFile)
		}

		return codex_tr.New(profile, codex.New(authPath)), nil
	default:
		return nil, fmt.Errorf("router: unknown protocol %q", profile.Protocol)
	}
}

// Restore restores the route from ccms-state.json (daemon startup).
func (s *Service) Restore(ctx context.Context) {
	var state models.State
	if err := jsonfile.ReadJSON(s.cfg.StatePath(), &state); err != nil || state.Profile == "" {
		return
	}
	if err := s.Use(ctx, state.Profile, state.Model); err != nil {
		if !errors.Is(err, context.Canceled) {
			s.log.Warn("restore failed", "profile", state.Profile, "err", err)
		}
	}
}

// Active — (profile, model, whether a route exists).
func (s *Service) Active() (profile, model string, ok bool) {
	routeRef := s.active.Load()
	if routeRef == nil {
		return "", "", false
	}

	return routeRef.profile, routeRef.model, true
}

// Ready — the active translator is ready (a route exists).
func (s *Service) Ready() bool {
	return s.active.Load() != nil
}

// AuthToken — the daemon's client token from providers.json.
func (s *Service) AuthToken() string {
	return *s.authTok.Load()
}

// ServeMessages delegates /v1/messages to the active translator.
func (s *Service) ServeMessages(w http.ResponseWriter, r *http.Request) {
	routeRef := s.active.Load()
	if routeRef == nil {
		writeAnthropicError(w, http.StatusServiceUnavailable, "no active profile — run ccms use")

		return
	}
	routeRef.translator.ServeMessages(w, r)
}

// ServeCountTokens delegates /v1/messages/count_tokens.
func (s *Service) ServeCountTokens(w http.ResponseWriter, r *http.Request) {
	routeRef := s.active.Load()
	if routeRef == nil {
		writeAnthropicError(w, http.StatusServiceUnavailable, "no active profile — run ccms use")

		return
	}
	routeRef.translator.ServeCountTokens(w, r)
}

// Models — /v1/models from the active profile's models.
func (s *Service) Models() []models.Model {
	r := s.active.Load()
	if r == nil {
		return nil
	}
	providers, err := config.LoadProviders(s.cfg.ProvidersPath())
	if err != nil {
		return nil
	}
	if p := providers.Profiles.Get(r.profile); p != nil {
		return p.Models
	}

	return nil
}

// Close quenches the active translator.
func (s *Service) Close(ctx context.Context) error {
	if r := s.active.Load(); r != nil {
		return r.translator.Close(ctx)
	}

	return nil
}

func writeAnthropicError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `{"type":"error","error":{"type":"api_error","message":%q}}`, msg)
}

// expandHome — expand ~ in config paths (codex auth_file).
func expandHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(path, "~"), "/"))
		}
	}

	return path
}
