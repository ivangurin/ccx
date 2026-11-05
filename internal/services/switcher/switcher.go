// Package switcher — переключение профилей: порт apply_profile bash-ccx.
// Инвариант: сначала готовность демона (Ensure + Use), только потом правка
// settings.json; провал ⇒ файлы не тронуты, пользователь остаётся на прежнем
// профиле.
package switcher

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/pkg/flock"
)

// Daemon — контракт демона для переключения (реализация — HTTP-клиент к
// /admin/*, тесты подменяют моком).
type Daemon interface {
	// Ensure — демон жив (поднять при необходимости, дождаться health).
	Ensure(ctx context.Context) error
	// Use — переключить транслятор демона на профиль (включая Ready-проверку).
	Use(ctx context.Context, profile, model string) error
}

// Switcher — применение профиля к settings.json под локом.
type Switcher struct {
	cfg    *config.Config
	daemon Daemon
}

// New создаёт Switcher.
func New(cfg *config.Config, daemon Daemon) *Switcher {
	return &Switcher{cfg: cfg, daemon: daemon}
}

// Apply переключает профиль; modelArg — name или id (пусто = default).
// Возвращает строку итога для пользователя.
func (s *Switcher) Apply(ctx context.Context, profileKey, modelArg string) (string, error) {
	lock, err := flock.LockWait(lockPath(s.cfg), s.cfg.LockTimeout)
	if err != nil {
		return "", fmt.Errorf("ccx: не удалось получить лок переключателя: %w", err)
	}
	defer func() { _ = lock.Unlock() }()

	return s.applyLocked(ctx, profileKey, modelArg)
}

func lockPath(cfg *config.Config) string {
	return cfg.Home + "/.ccx.lock"
}

func (s *Switcher) applyLocked(ctx context.Context, profileKey, modelArg string) (string, error) {
	providers, err := config.LoadProviders(s.cfg.ProvidersPath())
	if err != nil {
		return "", fmt.Errorf("ccx: providers.json недоступен (%v) — сначала ccx init", err)
	}
	profile := providers.Profiles.Get(profileKey)
	if profile == nil {
		return "", fmt.Errorf("ccx: неизвестный профиль: %s (см. ccx list)", profileKey)
	}

	model, ok := profile.ResolveModel(modelArg)
	if !ok {
		if modelArg == "" {
			return "", fmt.Errorf("ccx: в профиле %s нет default-модели — укажите модель: ccx %s <model>", profileKey, profileKey)
		}

		return "", fmt.Errorf("ccx: модель %s не найдена в профиле %s (см. ccx list)", modelArg, profileKey)
	}

	// Инвариант: демон готов и маршрут переключен ДО правки settings.json.
	if err := s.daemon.Ensure(ctx); err != nil {
		return "", fmt.Errorf("ccx: отмена: демон не поднялся (%v) — settings.json не тронут, остаётесь на прежнем профиле", err)
	}
	if err := s.daemon.Use(ctx, profileKey, model); err != nil {
		return "", fmt.Errorf("ccx: отмена: транслятор %s не готов (%v) — settings.json не тронут, остаётесь на прежнем профиле", profileKey, err)
	}

	if err := s.writeSettings(providers, profile, model); err != nil {
		return "", err
	}
	if err := s.writeState(profileKey, model); err != nil {
		return "", err
	}

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", providers.Daemon.Port)

	return fmt.Sprintf("Переключено: %s · модель %s · %s", profile.Name, model, baseURL), nil
}

// daemonEnv — env-блок для settings.json: URL/токен демона + extras профиля +
// пиннинг модели на все маппинги (haiku-класс ведёт на ту же модель — фоновые
// задачи Claude Code остаются на выбранной).
func daemonEnv(providers *models.Providers, profile *models.Profile, model string) map[string]string {
	env := map[string]string{}
	for k, v := range profile.Env {
		env[k] = v
	}
	env["ANTHROPIC_BASE_URL"] = fmt.Sprintf("http://127.0.0.1:%d", providers.Daemon.Port)
	env["ANTHROPIC_AUTH_TOKEN"] = providers.Daemon.AuthToken
	for _, key := range pinnedKeys {
		env[key] = model
	}

	return env
}

var pinnedKeys = []string{
	"ANTHROPIC_MODEL",
	"ANTHROPIC_DEFAULT_MODEL",
	"ANTHROPIC_DEFAULT_HAIKU_MODEL",
	"ANTHROPIC_DEFAULT_SONNET_MODEL",
	"ANTHROPIC_DEFAULT_OPUS_MODEL",
	"ANTHROPIC_DEFAULT_FABLE_MODEL",
	"ANTHROPIC_SMALL_FAST_MODEL",
}

// writeSettings: бэкап → атомарная правка env (managed keys) → пиннинг модели.
func (s *Switcher) writeSettings(providers *models.Providers, profile *models.Profile, model string) error {
	settings, err := models.LoadSettings(s.cfg.SettingsPath())
	if err != nil {
		return fmt.Errorf("ccx: нет %s — он создаётся первым запуском Claude Code", s.cfg.SettingsPath())
	}

	if err := backupSettings(s.cfg, settings); err != nil {
		return err
	}

	// managed: env-ключи всех профилей (прошлые extras уходят) + ANTHROPIC_*
	managed := map[string]bool{}
	for _, key := range providers.Profiles.Keys {
		for k := range providers.Profiles.Get(key).Env {
			managed[k] = true
		}
	}
	env := settings.Env()
	fresh := map[string]string{}
	for k, v := range env {
		// все ANTHROPIC_* стёрты (не только managed): прошлые ключи профилей
		// и ручные значения не должны переживать свитч (паритет bash with_entries)
		if managed[k] || strings.HasPrefix(k, "ANTHROPIC_") {
			continue
		}
		fresh[k] = v
	}
	for k, v := range daemonEnv(providers, profile, model) {
		fresh[k] = v
	}
	settings.SetEnv(fresh)
	settings.SetModelPin(model)

	data, err := settings.Marshal()
	if err != nil {
		return fmt.Errorf("ccx: не удалось преобразовать settings.json: %w", err)
	}
	if err := writeFileAtomic(s.cfg.SettingsPath(), data); err != nil {
		return fmt.Errorf("ccx: запись settings.json: %w", err)
	}

	return nil
}

func (s *Switcher) writeState(profileKey, model string) error {
	state := models.State{
		Profile: profileKey,
		Model:   model,
		Updated: time.Now().UTC().Format(time.RFC3339),
	}

	return writeStateFile(s.cfg.StatePath(), state)
}
