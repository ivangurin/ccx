// Package router — активный маршрут демона: профиль → транслятор.
// Use(): перечитать providers.json → построить транслятор по protocol →
// Ready() → атомарный swap (in-flight остаются на старом) → Close старого.
// Демон stateless по пользовательским файлам: маршрут восстанавливает из
// ccx-state.json (read-only) при старте.
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

	"github.com/ivangurin/ccx/internal/clients/codex"
	copilot_client "github.com/ivangurin/ccx/internal/clients/copilot"
	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/pkg/jsonfile"
	"github.com/ivangurin/ccx/internal/translators"
	"github.com/ivangurin/ccx/internal/translators/anthropic"
	codex_tr "github.com/ivangurin/ccx/internal/translators/codex"
	"github.com/ivangurin/ccx/internal/translators/copilot"
	"github.com/ivangurin/ccx/internal/translators/openai"
)

// route — связка профиль+модель+транслятор; immutable после установки.
type route struct {
	profile    string
	model      string
	translator translators.Translator
}

// Service — маршрутизатор демона.
type Service struct {
	cfg     *config.Config
	log     *slog.Logger
	active  atomic.Pointer[route]
	authTok atomic.Pointer[string]
}

// New создаёт сервис роутера; клиентский токен демона грузится сразу
// (middleware авторизации работает до первого Use).
func New(cfg *config.Config, log *slog.Logger) *Service {
	s := &Service{cfg: cfg, log: log}
	s.authTok.Store(new(string))
	if providers, err := config.LoadProviders(cfg.ProvidersPath()); err == nil {
		token := providers.Daemon.AuthToken
		s.authTok.Store(&token)
	} else {
		log.Warn("providers.json недоступен на старте демона", "err", err)
	}

	return s
}

// Use переключает активный профиль (перечитывая providers.json).
func (s *Service) Use(ctx context.Context, profileKey, model string) error {
	providers, err := config.LoadProviders(s.cfg.ProvidersPath())
	if err != nil {
		return fmt.Errorf("router: providers.json: %w", err)
	}
	token := providers.Daemon.AuthToken
	s.authTok.Store(&token)

	profile := providers.Profiles.Get(profileKey)
	if profile == nil {
		return fmt.Errorf("router: неизвестный профиль: %s", profileKey)
	}

	tr, err := buildTranslator(s.cfg, profile)
	if err != nil {
		return err
	}
	if err := tr.Ready(ctx); err != nil {
		_ = tr.Close(ctx)

		return fmt.Errorf("router: профиль %s не готов: %w", profileKey, err)
	}

	old := s.active.Swap(&route{profile: profileKey, model: model, translator: tr})
	if old != nil {
		_ = old.translator.Close(ctx) // in-flight уже держат свою ссылку
	}
	s.log.Info("route activated", "profile", profileKey, "model", model, "protocol", profile.Protocol)

	return nil
}

// buildTranslator — реестр protocol → транслятор.
func buildTranslator(cfg *config.Config, profile *models.Profile) (translators.Translator, error) {
	switch profile.Protocol {
	case models.ProtocolAnthropic:
		return anthropic.New(profile), nil
	case models.ProtocolOpenAI:
		return openai.New(profile), nil
	case models.ProtocolCopilot:
		store := filepath.Join(cfg.CCXDir(), "copilot-auth.json")

		return copilot.New(profile, copilot_client.New(store)), nil
	case models.ProtocolCodex:
		authPath := codex.DefaultAuthPath()
		if profile.Codex != nil && profile.Codex.AuthFile != "" {
			authPath = expandHome(profile.Codex.AuthFile)
		}

		return codex_tr.New(profile, codex.New(authPath)), nil
	default:
		return nil, fmt.Errorf("router: неизвестный protocol %q", profile.Protocol)
	}
}

// Restore восстанавливает маршрут из ccx-state.json (старт демона).
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

// Active — (профиль, модель, есть ли маршрут).
func (s *Service) Active() (profile, model string, ok bool) {
	routeRef := s.active.Load()
	if routeRef == nil {
		return "", "", false
	}

	return routeRef.profile, routeRef.model, true
}

// Ready — активный транслятор готов (маршрут есть).
func (s *Service) Ready() bool {
	return s.active.Load() != nil
}

// AuthToken — клиентский токен демона из providers.json.
func (s *Service) AuthToken() string {
	return *s.authTok.Load()
}

// ServeMessages делегирует /v1/messages активному транслятору.
func (s *Service) ServeMessages(w http.ResponseWriter, r *http.Request) {
	routeRef := s.active.Load()
	if routeRef == nil {
		writeAnthropicError(w, http.StatusServiceUnavailable, "нет активного профиля — выполните ccx use")

		return
	}
	routeRef.translator.ServeMessages(w, r)
}

// ServeCountTokens делегирует /v1/messages/count_tokens.
func (s *Service) ServeCountTokens(w http.ResponseWriter, r *http.Request) {
	routeRef := s.active.Load()
	if routeRef == nil {
		writeAnthropicError(w, http.StatusServiceUnavailable, "нет активного профиля — выполните ccx use")

		return
	}
	routeRef.translator.ServeCountTokens(w, r)
}

// Models — /v1/models из models активного профиля.
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

// Close гасит активный транслятор.
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

// expandHome — раскрыть ~ в путях конфига (auth_file codex).
func expandHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(path, "~"), "/"))
		}
	}

	return path
}
