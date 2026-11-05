# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

Module: `github.com/ivangurin/ccx`, Go 1.27.

ccx is a single binary for switching Claude Code LLM providers: the CLI
switches profiles, the embedded daemon (`ccx serve`, 127.0.0.1:4567) owns
`ANTHROPIC_BASE_URL` and translates the Anthropic Messages API into the
active provider's protocol (anthropic-passthrough / openai / copilot / codex).
A rewrite of the bash tool `~/projects/ccx` (the behavior reference; its 78
tests are being ported).

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

CLI (profile switching, editing `~/.claude/settings.json`) + daemon (routing
the Anthropic API to the active profile's translator). Data ownership: the
CLI owns `settings.json`, the daemon owns `ccx-state.json`; upstream
credentials live in `~/.claude/providers.json` (v2, `protocol` per profile).

### Layer structure

```
cmd/ccx/main.go                         entry point: run() → cli.Execute
internal/
  cli/                                  stdlib command dispatcher (use/list/status/init/serve/...)
  app/                                  app.Run(): serve mode: config→logger→closer→provider→router→http
                                         spawn.go: detached daemon launch (Setsid, pidfile)
  config/                               env config + CCX_* paths + providers.json v2 (load/save, 0600)
                                         migrate.go: v1→v2 + import from settings/env.json
  models/                               DTOs: anthropic(+sse), openai, codex, profile, providers,
                                         settings, state, admin
  service_provider/                     DI: lazy singleton getters
  api/
    handlers/                           chi router: /v1/* (messages, count_tokens, models),
                                         /admin/* (use, health, ready, status, stop), /health
    middleware/                         auth (daemon token), logging
  services/
    router/                             active route (atomic.Pointer), factory by protocol,
                                         activate: build → Ready() → swap → state
    switcher/                           port of bash-ccx apply_profile: flock, backup 0600, atomic,
                                         managed keys, model pinning, EnsureDaemon
    meta/                               health/version
  translators/
    translator.go                       Translator + ResponseSink interfaces
    prefill/                            prefill fix (port of copilot-bridge.py)
    anthropic/                          passthrough (auth substitution, SSE not parsed)
    openai/                             Anthropic ⇄ chat/completions (request/response/stream)
    copilot/                            reuse of openai + prefill + VSCode masquerading
    codex/                              Anthropic ⇄ Responses API
  clients/
    openai/                             chat/completions, configurable UA
    copilot/                            GitHub device flow + copilot-token + store
    codex/                              ~/.codex/auth.json (flock) + refresh
  pkg/
    closer/                             LIFO resource shutdown
    logger/                             slog wrapper (dev/json)
    httpserver/                         http.Server with graceful shutdown (WriteTimeout=0: SSE)
    flock/                              file locks (LOCK_EX|LOCK_NB)
    jsonfile/                           atomic JSON writes (tmp+rename, permissions)
    sse/                                upstream SSE reader + AnthropicWriter
    tui/                                interactive menu (arrows/filter/fallback)
    tokenestimate/                      local count_tokens estimation
    suite/provider/                     test counterpart of service_provider
```

### Key invariants (from bash-ccx, mandatory)

- **Readiness before settings.json**: switching a profile first brings up the
  daemon and prepares the translator (`Ready()`); on failure,
  `~/.claude/settings.json` is left untouched and the user stays on the
  previous profile.
- **Managed keys**: only `ANTHROPIC_*` keys and profile env keys are edited;
  user variables (API_TIMEOUT_MS, MCP tokens) are preserved.
- **Model pinning**: `model`, `availableModels=[m]`, `enforceAvailableModels`,
  all `ANTHROPIC_DEFAULT_*_MODEL` = the selected model.
- **`ANTHROPIC_BASE_URL` is always the daemon** — it never changes when
  switching profiles.
- **`ccx-state.json`** `{profile, model, updated}` — format is 1:1 with the
  old ccx (read by statusline.sh).
- Atomic writes: tmp file in the same directory + rename; backups and configs
  are 0600.

## Testing

Tests use testify (`require`); the suite provider is
`internal/pkg/suite/provider` (temp CCX_HOME, safe config, quiet logger, lazy
mocks, cleanup). Upstreams are httptest fakes; SSE uses golden sequences in
testdata. **Test coverage must be at least 80%** (`make test`,
`-coverpkg=./...`).

## Conventions

- **One file per service method / handler / translator step**: `switcher/apply.go`,
  `api/handlers/anthropic/messages.go`. One test file per method (`foo_test.go`).
- **"Why" decision comments** are written in code: they record invariants and
  precedent.
- Tokens/secrets are never logged; request bodies are logged only on error.
- Do not add the `Co-Authored-By: Claude` trailer to commits.
- All project artifacts (code, comments, commit messages, docs) are written
  in English; conversation with the user is in Russian.
