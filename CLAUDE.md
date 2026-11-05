# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

Module: `github.com/ivangurin/ccx`, Go 1.27.

ccx — единый бинарник для переключения LLM-провайдеров Claude Code: CLI
переключает профили, встроенный демон (`ccx serve`, 127.0.0.1:4567) держит
`ANTHROPIC_BASE_URL` и транслирует Anthropic Messages API в протокол активного
провайдера (anthropic-passthrough / openai / copilot / codex). Переписывание
bash-инструмента `~/projects/ccx` (эталон поведения; его 78 тестов портируются).

## Commands

```bash
make run           # run daemon in foreground (dev)
make build         # build binary to ./bin/ccx
make install       # install to $(PREFIX)/bin/ccx (default /opt/homebrew)
make test          # run all tests with coverage
make lint          # golangci-lint --fix on changed files (new-from-rev=main)
make lint-full     # golangci-lint on all files
make hooks         # activate git pre-commit hook (core.hooksPath → .githooks)
make generate      # gen-mocks + go fix + gofumpt (run after interface changes)
make gen-mocks     # regenerate mockery mocks under */mocks/
make format        # gofumpt -l -w -extra .
make deadcode      # find dead code
```

Run a single test:

```bash
go test ./internal/services/switcher/... -run TestName -count=1
```

## Architecture

CLI (переключение профилей, правка `~/.claude/settings.json`) + демон
(маршрутизация Anthropic API на транслятор активного профиля). Данные:
CLI владеет `settings.json`, демон владеет `ccx-state.json`; креды апстримов —
в `~/.claude/providers.json` (v2, `protocol` на профиль).

### Layer structure

```
cmd/ccx/main.go                         entry point: run() → cli.Execute
internal/
  cli/                                  stdlib-диспетчер команд (use/list/status/init/serve/...)
  app/                                  app.Run(): serve-режим: config→logger→closer→provider→router→http
                                         spawn.go: detached-запуск демона (Setsid, pidfile)
  config/                               env-конфиг + пути CCX_* + providers.json v2 (load/save, 600)
                                         migrate.go: v1→v2 + импорт settings/env.json
  models/                               DTO: anthropic(+sse), openai, codex, profile, providers,
                                         settings, state, admin
  service_provider/                     DI: lazy singleton getters
  api/
    handlers/                           chi-роутер: /v1/* (messages, count_tokens, models),
                                         /admin/* (use, health, ready, status, stop), /health
    middleware/                         auth (токен демона), logging
  services/
    router/                             активный маршрут (atomic.Pointer), factory по protocol,
                                         activate: build → Ready() → swap → state
    switcher/                           ПОРТ apply_profile bash-ccx: flock, backup 600, atomic,
                                         managed keys, пиннинг модели, EnsureDaemon
    meta/                               health/version
  translators/
    translator.go                       interface Translator + ResponseSink
    prefill/                            prefill-фикс (порт copilot-bridge.py)
    anthropic/                          passthrough (подмена авторизации, SSE не парсим)
    openai/                             Anthropic ⇄ chat/completions (request/response/stream)
    copilot/                            реюз openai + prefill + маскировка VSCode
    codex/                              Anthropic ⇄ Responses API
  clients/
    openai/                             chat/completions, настраиваемый UA
    copilot/                            GitHub device flow + copilot-token + store
    codex/                              ~/.codex/auth.json (flock) + refresh
  pkg/
    closer/                             LIFO-гаситель ресурсов на shutdown
    logger/                             slog-обёртка (dev/json)
    httpserver/                         http.Server c graceful shutdown (WriteTimeout=0: SSE)
    flock/                              файловые локи (LOCK_EX|LOCK_NB)
    jsonfile/                           атомарная запись JSON (tmp+rename, права)
    sse/                                SSE-reader апстрима + AnthropicWriter
    tui/                                встроенное меню (стрелки/фильтр/fallback)
    tokenestimate/                      локальная оценка count_tokens
    suite/provider/                     тестовый аналог service_provider
```

### Ключевые инварианты (из bash-ccx, обязательны)

- **Сначала готовность, потом settings.json**: смена профиля сначала поднимает
  демон и готовит транслятор (`Ready()`); провал ⇒ `~/.claude/settings.json`
  не тронут, пользователь остаётся на прежнем профиле.
- **Managed keys**: правятся только `ANTHROPIC_*` и env-ключи профилей;
  пользовательские переменные (API_TIMEOUT_MS, MCP-токены) сохраняются.
- **Пиннинг модели**: `model`, `availableModels=[m]`, `enforceAvailableModels`,
  все `ANTHROPIC_DEFAULT_*_MODEL` = выбранная модель.
- **`ANTHROPIC_BASE_URL` всегда демон** — при переключении профилей не меняется.
- **`ccx-state.json`** `{profile, model, updated}` — формат 1:1 со старым ccx
  (читает statusline.sh).
- Атомарная запись: tmp в том же каталоге + rename; бэкапы и конфиги — 0600.

## Testing

Тесты на testify (`require`); suite-провайдер — `internal/pkg/suite/provider`
(temp CCX_HOME, безопасный конфиг, тихий логгер, ленивые моки, cleanup).
Апстримы — httptest-фейки; SSE — golden-последовательности в testdata.
**Покрытие тестами — не меньше 80%** (`make test`, `-coverpkg=./...`).

## Conventions

- **One file per service method / handler / translator step**: `switcher/apply.go`,
  `api/handlers/anthropic/messages.go`. Один тест-файл на метод (`foo_test.go`).
- **Комментарии к «why»-решениям** пишем в коде: фиксируют инварианты и прецеденты.
- Токены/секреты никогда не логируются; тела запросов — только при ошибке.
- В коммиты не добавлять трейлер `Co-Authored-By: Claude`.
