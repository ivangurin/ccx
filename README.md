# ccx

Единый Go-бинарник для переключения LLM-провайдеров Claude Code: CLI
переключает профили, встроенный демон транслирует Anthropic Messages API в
протокол активного провайдера. Переписывание bash-инструмента
[`ccx`](../ccx) (эталон поведения) без внешних прокси: ccr, copilot-api и
copilot-bridge заменены встроенными трансляторами, claude-codex — встроенным
codex-клиентом.

## Протоколы (поле `protocol` профиля)

| protocol | Апстрим | Заменяет |
|---|---|---|
| `anthropic` | прямой Anthropic-совместимый API (z.ai, …) | — (passthrough) |
| `openai` | OpenAI chat/completions (api.openai.com, корп. шлюзы) | claude-code-router |
| `copilot` | GitHub Copilot (OAuth device flow + prefill-фикс) | copilot-api + copilot-bridge |
| `codex` | ChatGPT Responses API (подписка) | claude-codex |

## Быстрый старт

```bash
make install                      # сборка + /opt/homebrew/bin/ccx (PREFIX=…)
ccx init                          # миграция v1-конфига или импорт + шаблоны
ccx list                          # профили и модели
ccx auth copilot                  # OAuth GitHub (для copilot-профиля)
ccx use zai                       # переключить (демон поднимется сам)
ccx status                        # профиль/модель/демон
```

`~/.claude/settings.json` после первого `ccx use` больше не меняет
`ANTHROPIC_BASE_URL` — он всегда указывает на локальный демон
(`http://127.0.0.1:4567`), переключение профиля меняет маршрут демона на лету.

## Конфиг (`~/.claude/providers.json`, v2, права 600)

```jsonc
{
  "version": 2,
  "daemon": {"port": 4567, "auth_token": "<hex>"},
  "profiles": {
    "zai": {
      "name": "z.ai (GLM)",
      "protocol": "anthropic",
      "anthropic": {"base_url": "https://api.z.ai/api/anthropic", "auth_token": "<ключ>"},
      "models": [{"id": "glm-5.3[1m]", "label": "GLM 5.3", "default": true}]
    },
    "x5": {
      "name": "Рабочий шлюз",
      "protocol": "openai",
      "openai": {"base_url": "https://…/v1", "api_key": "<токен>", "user_agent": "claude-cli/2.0.1"},
      "models": […]
    }
  }
}
```

## Команды

`ccx` (меню) · `ccx use <профиль> [модель]` (алиас `ccx <профиль>`) · `list` ·
`status` · `init [--force]` · `edit` · `serve` (демон foreground) · `stop` ·
`auth copilot` · `auth codex` · `version` · `help`

## Разработка

```bash
make test        # тесты с покрытием (порог ≥80%)
make lint        # golangci-lint по изменённым
make lint-full   # по всем файлам
make generate    # моки + форматирование
```

Структура и конвенции — CLAUDE.md (по образцу idp-port-forward: один файл на
метод/ручку, foo_test.go, suite provider в internal/pkg/suite/provider).
