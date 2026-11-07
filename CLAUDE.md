# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

Module: `github.com/ivangurin/ccx`, Go 1.27.

ccx (the name is inherited from the bash original) is a single binary for
switching Claude Code LLM providers: the CLI switches profiles, the embedded
daemon (`ccx serve --provider <key> [--port N]`, 127.0.0.1:4567 by default)
owns `ANTHROPIC_BASE_URL` and translates the Anthropic Messages API into the
launched provider's protocol (anthropic-passthrough / openai / copilot /
codex); for openai the upstream credential is pass-through (the token Claude
Code sends travels upstream — the daemon holds no secret of its own). A
rewrite of the bash tool `~/projects/ccx` (the behavior reference; its 78
tests are being ported).

## Commands

```bash
make run           # run daemon in foreground (dev)
make build         # build binary to ./bin/ccx
make install       # build + install to $(PREFIX)/bin/ccx (default /usr/local, sudo)
make uninstall     # remove the installed binary (sudo)
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
restart with new launch params). Data ownership: ccx owns its data root
`~/.ccx` (`CCX_HOME`) — providers.json, ccx-state.json, backups/, logs/,
daemon.lock/pid, the per-provider credential stores auth/<key>.json; of
Claude Code's `~/.claude` only
`settings.json` is touched (edited in place, never moved; `CCX_SETTINGS`
redirects it). Pre-rename data in `~/.claude` (providers.json,
ccms-state.json) is copied into `~/.ccx` once at
startup (`cmd/ccx/migrate.go` → `config.MigrateLegacyData`); sources stay in
place, new files win, disposable history (backups/logs) is not migrated.
providers.json is unversioned, flat — the map sits under the `providers`
key, connection fields `base_url`/`api_key`/… sit directly in the provider
entry, `protocol` decides which of them are meaningful; the client
masquerade — UA, editor versions, integration id, originator, prefill — is
code constants, not config; `daemon.port` is optional; a legacy `profiles`
key / `version` / `daemon.auth_token` load forever and self-migrate on the
next load — best-effort rewrite via SaveProviders).

### Layer structure

```
cmd/ccx/main.go                        entry point: main() → migrateLegacy() → cli.Execute
  migrate.go                           one-time ~/.claude → ~/.ccx data move (process boundary:
                                         never inside cli.Execute — its tests run unsandboxed)
internal/
  cli/                                  stdlib command dispatcher (use/list/status/init/serve/...)
  app/                                  app.Run(): serve mode: config→logger→lock→route(Build)→http
                                         spawn.go: detached daemon launch (Setsid, pidfile,
                                         --provider/--port args)
  config/                               env config + CCX_* paths + providers.json (load/save, 0600)
                                         Home=~/.ccx; settings.json default stays in ~/.claude
                                         providers.go: ResolveDaemonPort (flag > CCX_PORT >
                                         daemon.port > 4567)
                                         legacy.go: MigrateLegacyData (old-world copy, new wins)
                                         init.go: ccx init — import from settings/env.json + templates
  models/                               DTOs: anthropic(+sse), openai, codex, profile, providers,
                                         settings, state, admin
  service_provider/                     DI: lazy singleton getters
  api/
    handlers/                           chi router: /v1/* (messages, count_tokens, models),
                                         /admin/* (health, stop), /health — localhost-only, no token;
                                         guard.go: loopback-Host check (DNS rebinding) + X-Ccx
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
    codex/                              per-provider auth store (flock,
                                         generations) + refresh + OAuth login
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
  `--port` flag > `CCX_PORT` > `daemon.port` > 4567.
- **Anthropic passthrough goes direct**: a `protocol: anthropic` profile
  needs no translation — the upstream `base_url` + credential are written
  straight into settings.json (bash-ccx semantics), and the unused daemon is
  stopped best-effort.
- **Codex login is allow-list-gated**: auth.openai.com accepts only the
  registered callback URIs `http://localhost:1455/auth/callback` (fallback
  `:1457`; a busy port gets a `GET /cancel` nudge first) and authorize only
  at `/oauth/authorize` with the codex CLI's exact parameter set
  (`originator=codex_cli_rs`, `codex_cli_simplified_flow`, connectors
  scopes). Any deviation lets the browser login succeed but the issuer never
  redirects — a silent hang until the 15-min budget (verified live). The
  reference is codex-rs `login/server.rs`: read it, don't pattern from
  memory; the flow is verifiable only in a real browser (Cloudflare blocks
  curl on auth.openai.com).
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
  provider's own (`auth_token` > `api_key` > placeholder `ccx`): for openai
  it is pass-through — Claude Code sends it, the daemon forwards it upstream
  unchanged.
- **`ccx-state.json`** `~/.ccx/ccx-state.json` `{profile, model, updated}` —
  format is 1:1 with bash-ccx's state file (statusline.sh reads it — point
  its path at `~/.ccx/ccx-state.json`; the JSON schema is unchanged).
- Atomic writes: tmp file in the same directory + rename; backups and configs
  are 0600.

### Translator runtime invariants (v0.0.4 hardening)

- **Upstream read deadlines (readguard)**: first-byte 120s / idle 120s for
  streams, whole-generation 600s for non-stream — a non-stream response's
  first body byte IS the finished generation, so the first-token patience
  must not cap it. A watchdog cancels the request context (client bodies
  expose no net.Conn for SetReadDeadline); timeouts surface as named errors.
- **Copilot quota marking**: the gateway bills one premium interaction per
  user-initiated turn — tool continuations, compaction, subagent and
  prefill-continuation turns are marked `x-initiator: agent` (compact
  detection matches machine-generated signals only; a user typing
  "Pending Tasks:" still bills). Request/interaction ids derive
  deterministically from session+turn so client retries aggregate into one
  interaction, computed from the PRE-merge body (the tool_result merge
  absorbs the text the turn seed needs). `ccx status` shows the quota panel
  only when the active provider's protocol is copilot, from that provider's
  own credential store.
- **Billing line strip**: Claude Code leads its system prompt with
  `x-anthropic-billing-header:` — stripped before any upstream (cache-prefix
  noise at best, a rejection on strict gateways at worst); leading line
  only, mid-text occurrences are the user's words.
- **Client session id (sessionid)**: headers first (Session-Id only counts
  beyond 20 chars), then metadata.session_id, then the `_session_` tail of
  metadata.user_id — feeds the codex cache key/session headers and the
  copilot quota ids; empty means no session, never an error.
- **Codex client-version gate**: the backend gates model availability by
  client version — the Version header and the UA carry 0.159.0 and move
  together; a stale one locks newer models out of the catalog.
- **Codex auth generations**: the native codex CLI takes no lock —
  tokens.last_refresh (RFC3339Nano, the token ACQUISITION moment, not the
  write moment) orders generations; a rejected refresh adopts a strictly
  newer on-disk generation (ONE adoptive refresh if its access is also
  expired), saves are compare-and-swap (a newer foreign generation is never
  clobbered); ties and undated files lose — refusing to act on an ambiguous
  order is safer than guessing.
- **Orphan tool_result healing (openai)**: a tool_result whose call is not
  among the pending assistant calls becomes a text block (a dangling
  tool_call_id is a guaranteed 400 on strict upstreams); pairing works by
  subtraction — a mixed tool_result+text message answers some calls without
  ending the turn, system hooks ride through.
- **Anthropic rectify retry**: ONE retry on thinking-contract 400s
  (signature/thinking-block → strip thinking from the history;
  budget_tokens floor → clamp to 1024); unrelated 400s relay untouched; the
  passthrough buffers the body (64 MiB bound; over → 413, never a silent
  truncation) and arms a FRESH deadline for the retry.
- **Usage log**: append-only `~/.ccx/logs/usage-YYYY-MM-DD.jsonl`
  (provider/protocol/model/stream/tokens/latency/TTFT/status; counts and
  timings only — never bodies or credentials); stream breaks record
  502+error, count_tokens records nothing; passthrough token counts stay
  zero by design (SSE is never parsed there).

## Testing

Tests use testify (`require`); the suite provider is
`internal/pkg/suite/provider` (temp CCX_HOME with CCX_SETTINGS pinned inside
it — the settings default points at the live ~/.claude and must not leak
into tests; safe config, quiet logger, lazy mocks, cleanup). Upstreams are
httptest fakes; SSE uses golden sequences in testdata. **Test coverage must
be at least 80%** (`make test`, `-coverpkg=./...`).

### Manual daemon verification (live, without touching settings.json)

`ccx serve` never writes settings.json — it is safe to run next to a live
Claude Code session (unlike `ccx use`, which switches the provider that
session itself routes through; verify switches in a sandbox CCX_HOME, never
live). One daemon per CCX_HOME (`daemon.lock` flock) — `ccx stop` the
previous one first. `--daemon` detaches (Setsid); a foreground serve under a
harness may be reaped between turns.

```bash
./bin/ccx serve --daemon --provider <key> --port 4570   # pick a free port
curl -s http://127.0.0.1:4570/health                      # answering == Ready() passed
TOKEN=$(jq -r '.providers.<key>.auth_token' ~/.ccx/providers.json)
curl -s http://127.0.0.1:4570/v1/messages -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"model":"<id>","max_tokens":300,"messages":[{"role":"user","content":"…"}]}'
# same request with "stream":true for SSE; also /v1/models, /v1/messages/count_tokens
```

Credentials per protocol: openai is pass-through (send the profile token);
copilot/codex own theirs (any placeholder bearer works). Prerequisites:
each translated provider needs its own login — `ccx auth <key>`
(copilot: GitHub device flow; codex: ChatGPT OAuth PKCE into
`~/.ccx/auth/<key>.json`) — without it the daemon exits at startup
with `copilot: not authenticated`. Protocol quirks: the codex upstream is
stream-only (non-stream client requests are served by upstream stream +
aggregation) and must send `store:false`; corporate OpenAI-compatible
gateways may block Go's default UA (openai.DefaultUserAgent masquerades it).
`CCX_PORT=<n> ./bin/ccx stop` stops a non-default-port daemon.

## Conventions

- **One file per service method / handler / translator step**: `switcher/apply.go`,
  `api/handlers/anthropic/messages.go`. One test file per method (`foo_test.go`).
- **"Why" decision comments** are written in code: they record invariants and
  precedent.
- Tokens/secrets are never logged; request bodies are logged only on error.
- Do not add the `Co-Authored-By: Claude` trailer to commits.
- All project artifacts (code, comments, commit messages, docs) are written
  in English; conversation with the user is in Russian.
