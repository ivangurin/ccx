// Package suite_provider — тестовый аналог production-компоновки (порт
// паттерна idp-port-forward): песочница CCX_HOME (t.TempDir), тихий логгер,
// ленивые сервисы/моки поверх — единая точка сборки фикстур для всех тестов.
// Живой ~/.claude недостижим by construction (инвариант после инцидентов
// с CCR в bash-ccx: тесты никогда не трогают реальные настройки).
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

// Provider — тестовая среда: конфиг с безопасными дефолтами + тихий логгер +
// ленивые зависимости (добавляются пакетами по мере роста проекта).
type Provider struct {
	ctx    context.Context
	Config *config.Config
	log    *slog.Logger

	mu       sync.Mutex
	cleanups []func()
}

// NewProvider строит песочницу: уникальный CCX_HOME на вызов (t.Setenv —
// тесты обязаны быть непараллельными), возврат — сам провайдер.
func NewProvider(t *testing.T) *Provider {
	t.Helper()

	home := t.TempDir()
	t.Setenv("CCX_HOME", home)

	cfg, err := config.NewFromEnv()
	if err != nil {
		t.Fatalf("suite provider: config: %v", err)
	}
	// безопасные дефолты тестов: короткие таймауты, тихий лог
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

// Context возвращает базовый контекст теста.
func (p *Provider) Context() context.Context {
	return p.ctx
}

// Logger возвращает тихий логгер (error-уровень).
func (p *Provider) Logger() *slog.Logger {
	return p.log
}

// HomeDir — корень песочницы (CCX_HOME).
func (p *Provider) HomeDir() string {
	return p.Config.Home
}

// FilePath — путь к файлу внутри песочницы.
func (p *Provider) FilePath(parts ...string) string {
	return filepath.Join(append([]string{p.Config.Home}, parts...)...)
}

// Cleaner гасит зарегистрированные cleanups в обратном порядке.
func (p *Provider) Cleaner() {
	p.mu.Lock()
	fns := p.cleanups
	p.cleanups = nil
	p.mu.Unlock()
	for i := len(fns) - 1; i >= 0; i-- {
		fns[i]()
	}
}
