# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

Module: `github.com/ivangurin/ccms`, Go 1.27.

ccms (Claude Code Model Switcher) is a single binary for switching Claude Code
LLM providers: the CLI switches profiles, the embedded daemon (`ccms serve
--provider <key> [--port N]`, 127.0.0.1:4567 by default) owns
`ANTHROPIC_BASE_URL` and translates the Anthropic Messages API into the
launched provider's protocol (anthropic-passthrough / openai / copilot /
codex); for openai the upstream credential is pass-through (the token Claude
Code sends travels upstream — the daemon holds no secret of its own). A
rewrite of the bash tool `~/projects/ccx` (the behavior reference; its 78
tests are being ported).

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
the Anthropic API to the launched provider's translator; switching = daemon
restart with new launch params). Data ownership: the CLI owns
`settings.json` and `ccms-state.json`; upstream credentials live in
`~/.claude/providers.json` (unversioned, flat — the map sits under the
`providers` key, connection fields `base_url`/`api_key`/… sit directly in
the provider entry, `protocol` decides which of them are meaningful;
`daemon.port` is optional; a legacy `profiles` key / `version` /
`daemon.auth_token` load forever and self-migrate on the next load —
best-effort rewrite via SaveProviders).

### Layer structure

```
cmd/ccms/main.go                        entry point: run() → cli.Execute
internal/
  cli/                                  stdlib command dispatcher (use/list/status/init/serve/...)
  app/                                  app.Run(): serve mode: config→logger→lock→route(Build)→http
                                         spawn.go: detached daemon launch (Setsid, pidfile,
                                         --provider/--port args)
  config/                               env config + CCMS_* paths + providers.json (load/save, 0600)
                                         providers.go: ResolveDaemonPort (flag > CCMS_PORT >
                                         daemon.port > 4567)
                                         init.go: ccms init — import from settings/env.json + templates
  models/                               DTOs: anthropic(+sse), openai, codex, profile, providers,
                                         settings, state, admin
  service_provider/                     DI: lazy singleton getters
  api/
    handlers/                           chi router: /v1/* (messages, count_tokens, models),
                                         /admin/* (health, stop), /health — localhost-only, no token;
                                         guard.go: loopback-Host check (DNS rebinding) + X-Ccms
                                         marker on /admin/* (browser drive-by); openai is pass-through
  services/
    router/                             build-once at startup: --provider key → profile →
                                         translator → Ready() before listening (no in-daemon
                                         switching: a switch is a daemon restart)
    switcher/                           port of bash-ccx apply_profile: flock, backup 0600, atomic,
                                         managed keys, model pinning, daemon Restart (stop with
                                         SIGTERM/SIGKILL escalation → spawn --provider/--port →
                                         health wait); anthropic → direct env (upstream base_url +
                                         credential, daemon stop)
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
  (openai/copilot/codex) restarts the daemon with `--provider`/`--port` and
  waits for `/health` — which answering means the translator's `Ready()`
  passed (the daemon builds the route before listening); on failure,
  `~/.claude/settings.json` is left untouched and the user stays on the
  previous profile. Restart kills in-flight SSE requests — accepted
  trade-off of the restart semantics. Port precedence everywhere:
  `--port` flag > `CCMS_PORT` > `daemon.port` > 4567.
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
  direct anthropic profiles. The credential written alongside is the
  provider's own (`auth_token` > `api_key` > placeholder `ccms`): for openai
  it is pass-through — Claude Code sends it, the daemon forwards it upstream
  unchanged.
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
