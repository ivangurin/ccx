# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

Module: `github.com/ivangurin/ccms`, Go 1.27.

ccms (Claude Code Model Switcher) is a single binary for switching Claude Code
LLM providers: the CLI switches profiles, the embedded daemon (`ccms serve`,
127.0.0.1:4567) owns `ANTHROPIC_BASE_URL` and translates the Anthropic
Messages API into the active provider's protocol (anthropic-passthrough /
openai / copilot / codex). A rewrite of the bash tool `~/projects/ccx`
(the behavior reference; its 78 tests are being ported).

## Commands

```bash
make run           # run daemon in foreground (dev)
make build         # build binary to ./bin/ccms
make install       # install to $(PREFIX)/bin/ccms (default /opt/homebrew)
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
CLI owns `settings.json`, the daemon owns `ccms-state.json`; upstream
credentials live in `~/.claude/providers.json` (v3: flat — connection fields
`base_url`/`api_key`/… sit directly in the profile, `protocol` decides which
of them are meaningful).

### Layer structure

```
cmd/ccms/main.go                        entry point: run() → cli.Execute
internal/
  cli/                                  stdlib command dispatcher (use/list/status/init/serve/...)
  app/                                  app.Run(): serve mode: config→logger→closer→provider→router→http
                                         spawn.go: detached daemon launch (Setsid, pidfile)
  config/                               env config + CCMS_* paths + providers.json v3 (load/save, 0600)
                                         init.go: ccms init — import from settings/env.json + templates
                                         (no migrations: an old-format file is only regenerated via
                                         ccms init --force)
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
                                         managed keys, model pinning, EnsureDaemon; anthropic →
                                         direct env (upstream base_url + credential, daemon stop)
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

- **Readiness before settings.json**: switching a translated profile
  (openai/copilot/codex) first brings up the daemon and prepares the
  translator (`Ready()`); on failure, `~/.claude/settings.json` is left
  untouched and the user stays on the previous profile.
- **Anthropic passthrough goes direct**: a `protocol: anthropic` profile
  needs no translation — the upstream `base_url` + credential are written
  straight into settings.json (bash-ccx semantics), and the unused daemon is
  stopped best-effort.
- **Managed keys**: only `ANTHROPIC_*` keys and profile env keys are edited;
  user variables (API_TIMEOUT_MS, MCP tokens) are preserved.
- **Byte-precise settings.json edits**: only the env block and the model pin
  change; everything else — key order, indentation, user blocks, the trailing
  newline — survives a switch byte-for-byte (`internal/pkg/jsonpatch`; a fresh
  env key appends at the end of the block, a replaced one keeps its line).
  Deliberately stricter than bash-ccx, whose jq round-trip reformatted the
  whole file.
- **Model pinning**: `model`, `availableModels=[m]`, `enforceAvailableModels`,
  all `ANTHROPIC_DEFAULT_*_MODEL` = the selected model.
- **`ANTHROPIC_BASE_URL`**: always the daemon for translated protocols (it
  never changes when switching between them); the upstream URL itself for
  direct anthropic profiles.
- **`ccms-state.json`** `{profile, model, updated}` — format is 1:1 with
  bash-ccx's ccx-state.json (statusline.sh reads it — after the ccms rename
  its path must point to ccms-state.json; the JSON schema is unchanged).
- Atomic writes: tmp file in the same directory + rename; backups and configs
  are 0600.

## Testing

Tests use testify (`require`); the suite provider is
`internal/pkg/suite/provider` (temp CCMS_HOME, safe config, quiet logger, lazy
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
