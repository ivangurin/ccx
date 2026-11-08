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
`~/.ccx` (`CCX_HOME`) — providers.json, state.json, backups/, logs/,
daemon.lock/pid, switch.lock (the switcher's flock; was the hidden
bash-ccx `.ccx.lock`, renamed the same day as state.json), the
per-provider credential stores auth/<key>.json; of
Claude Code's `~/.claude` only
`settings.json` is touched (edited in place, never moved; `CCX_SETTINGS`
redirects it). The pre-rename one-time data move from `~/.claude` is gone
(2026-11-07): fresh installs start from `ccx init`; old-world data, if any,
must be copied by hand. providers.json is unversioned, flat — the map sits
under the `providers`
key, connection fields `base_url`/`api_key`/… sit directly in the provider
entry, `protocol` decides which of them are meaningful; the client
masquerade — UA, editor versions, integration id, originator, prefill — is
code constants, not config; the `daemon` block (`port`, `debug`) is
optional and SWITCHER-OWNED — the switcher reads it and spawns the daemon
with `--port`/`--debug`, the daemon itself never reads the block; a legacy `profiles`
key / `version` / `daemon.auth_token` load forever and self-migrate on the
next load — best-effort rewrite via SaveProviders).

### Layer structure

```
cmd/ccx/main.go                        entry point: main() → cli.Execute
internal/
  cli/                                  cobra command tree (use/list/status/init/serve/...);
                                         Execute(args, stdout) int is the stable seam — bodies
                                         runXxx(argv, stdout) int keep bash-ccx exit codes and
                                         hint texts, cobra owns parsing/help/completion
  app/                                  app.Run(): serve mode: config→logger→lock→route(Build)→http
                                         spawn.go: detached daemon launch (Setsid, pidfile,
                                         --provider/--port/--debug args)
  config/                               env config + CCX_* paths + providers.json (load/save, 0600)
                                         Home=~/.ccx; settings.json default stays in ~/.claude
                                         providers.go: ResolveDaemonPort (flag > CCX_PORT >
                                         daemon.port > 4567)
                                         init.go: ccx init — import from settings/env.json + templates
  models/                               DTOs: anthropic(+sse), openai, codex, profile, providers,
                                         settings, state, admin
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
    suite/provider/                     test suite: temp CCX_HOME, safe config, lazy mocks,
                                         cleanup (settings default pinned away from the live ~/.claude)
```

### Key invariants (from bash-ccx, mandatory)

- **Readiness before settings.json**: switching a translated profile
  (openai/copilot/codex) restarts the daemon with `--provider`/`--port`
  (plus `--debug` when `daemon.debug` is set) and waits for `/health` —
  which answering means the translator's `Ready()` passed (the daemon
  builds the route before listening); on failure, `~/.claude/settings.json`
  is left untouched and the user stays on the previous profile. Restart
  kills in-flight SSE requests — accepted trade-off of the restart
  semantics. Port precedence everywhere: `--port` flag > `CCX_PORT` >
  `daemon.port` > 4567 — but the providers.json `daemon` block is read by
  the SWITCHER only (it spawns with explicit `--port`/`--debug`); the
  daemon's own fallback without `--port` is just `CCX_PORT` > 4567.
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
- **`state.json`** `~/.ccx/state.json` `{profile, model, updated}` —
  format is 1:1 with bash-ccx's state file (statusline.sh reads it — point
  its path at `~/.ccx/state.json`; the JSON schema is unchanged). Renamed
  from `ccx-state.json` (2026-11-07) with no fallback read: the pre-rename
  file stays on disk unread, same clean break as the legacy data migration.
- Atomic writes: tmp file in the same directory + rename; backups and configs
  are 0600.

### Translator runtime invariants (v0.0.4 hardening)

- **Upstream read deadlines (readguard)**: first-byte 120s / idle 120s for
  streams, whole-generation 600s for non-stream — a non-stream response's
  first body byte IS the finished generation, so the first-token patience
  must not cap it. A watchdog cancels the request context (client bodies
  expose no net.Conn for SetReadDeadline); timeouts surface as named errors.
- **Copilot quota marking**: under the LEGACY premium-request billing the
  gateway billed one premium interaction per user-initiated turn — tool
  continuations, compaction, subagent and prefill-continuation turns are
  marked `x-initiator: agent` (compact detection matches machine-generated
  signals only; a user typing "Pending Tasks:" still bills). The marking
  stays: legacy annual plans still bill per request. But monthly accounts
  moved to AI credits (2026-06-01, token-metered at API list rates incl.
  cached tokens, 1 credit = $0.01 — no per-request charge, no initiator
  exemption): a 22-request/1.26M-token agent session burned ~118 credits
  with correct marking (verified live 2026-11-07, usage log vs
  credits_used) — under credits the marking cannot reduce cost, only token
  count can. Request/interaction ids derive
  deterministically from session+turn so client retries aggregate into one
  interaction, computed from the PRE-merge body (the tool_result merge
  absorbs the text the turn seed needs). `ccx status` shows the quota panel
  only when the active provider's protocol is copilot, from that provider's
  own credential store.
- **Copilot quota response shape**: `/copilot_internal/user` dropped the
  top-level `premium_interactions` object — the pool now lives in
  `quota_snapshots["premium_interactions"]` with the reset date hoisted to
  top-level `quota_reset_date`; the legacy shape stays parsed as fallback
  (verified live 2026-11-07). The copilot token itself is now an opaque
  `tid=…;…` string, not a JWT, and carries no quota — nothing parses it as
  one (verified live).
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
- **Parallel codex function calls carry no argument deltas**: the ChatGPT
  backend streams `response.function_call_arguments.delta` fragments only
  for single calls; parallel calls deliver the complete arguments solely in
  `response.function_call_arguments.done` (top-level `arguments` string)
  and the `output_item.done` item, in either order — the translator seals
  the full string once per item (a delta-streamed item ignores the done
  carriers: input_json_delta concatenates client-side) (verified live
  2026-11-08: parallel Agent launches reached Claude Code with input `{}`
  → InputValidationError on required params; the retried turns then 400'd
  on the poisoned history until the session was abandoned).
- **Codex omitted arguments are a hard 400**: a tool_use without `input`
  must serialize as `arguments:"{}"`, never omitted via omitempty
  (`Missing required parameter: 'input[N].arguments'`, verified live
  2026-11-08 — replaying a history with inputless tool_use calls).
- **Codex `output` is type-strict per item** (verified live 2026-11-08):
  `function_call_output` must carry `output` even when the result is empty
  (`Missing required parameter: 'input[N].output'` — an Agent call that
  returned no content), while every OTHER item type must NOT carry it
  (`Unknown parameter: 'input[N].output'`). A bare string + omitempty
  cannot express both, so `ResponsesItem.Output` is a `*string`: nil keeps
  the field off the wire, `&""` serializes an empty result. The output twin
  of the `arguments:"{}"` rule.
- **A codex stream without a terminal event is a truncation, not an empty
  answer** (verified live 2026-11-08 17:42–17:45): eight consecutive
  requests returned 200 + an EMPTY assistant message — usage log in=0/out=0
  is the tell (no terminal usage arrived; ttft present means the stream
  did start) — and Claude Code rendered "no visible output" and retried
  into the same wall; the identical signature appeared twice on the
  pre-fix binary at 16:50, i.e. a backend-side truncation, not a ccx
  regression. The follow-up window (20:53–20:56, post-fix) exposed the
  actual terminal event: `response.incomplete` with reason
  `max_output_tokens`, NO usage in the payload, ~6–8s after stream start —
  the pre-fix code dropped it as an unknown event, which is what the 17:42
  empties were. The failure is intermittent and backend-side: a byte-exact
  replay of a failing request (reassembled from the CC transcript's
  api-request-blobs) passes minutes later. The stream machine now treats a
  clean end without response.completed/failed/incomplete as an error event
  (usage log 502 "upstream stream ended without completion");
  `response.incomplete` is handled as terminal — usage captured when
  present, `incomplete_details.reason` relayed in the error. Catch the next
  window live with CCX_DUMP_DIR to see the raw incomplete event.
- **CCX_DUMP_DIR captures the raw codex upstream SSE** (`codex-sse-*.json`,
  one file per event) next to the request/error dumps — the wire-shape
  class of bugs (like the parallel-call delivery) is only visible on the
  real backend.
- **Orphan tool_result healing (openai)**: a tool_result whose call is not
  among the pending assistant calls becomes a text block (a dangling
  tool_call_id is a guaranteed 400 on strict upstreams); pairing works by
  subtraction — a mixed tool_result+text message answers some calls without
  ending the turn, system hooks ride through.
- **Anthropic rectify retry**: ONE retry on thinking-contract 400s
  (signature/thinking-block → strip thinking from the history;
  budget_tokens floor → clamp to 1024); unrelated 400s relay untouched; the
  passthrough buffers the body (64 MiB bound; over → 413, never a silent
  truncation) and arms a FRESH deadline for the retry — the retried response
  comes back WITH its guard: the body reads through the guard's context, so
  stopping that guard before the caller relays cancels every streamed retry
  on its first read (unit tests survived on buffered bodies; a gated-chunk
  test pins the stream now). The synthetic 502 body escapes via %q —
  transport errors quote the URL.
- **Usage log**: append-only `~/.ccx/logs/usage-YYYY-MM-DD.jsonl`
  (provider/protocol/model/stream/tokens/latency/TTFT/status; counts and
  timings only — never bodies or credentials); stream breaks record
  502+error, count_tokens records nothing; passthrough token counts stay
  zero by design (SSE is never parsed there).

### CLI invariants (cobra)

- **Provider shortcut rides on root Args**: an unknown first argument
  reaches the root RunE (which forwards it to `use`) only because root has
  an Args validator set (`cobra.ArbitraryArgs`) — without one cobra answers
  "unknown command" itself (verified live).
- **Cobra built-ins shadow provider keys**: a provider named `completion`
  or `help` cannot be switched via the shortcut — cobra's built-in
  subcommands win (verified live: `ccx completion zsh` prints the script);
  `ccx use completion` still works.
- **Exit codes and hint texts live in the bodies**: RunE wraps the body's
  int into `exitError{code}` (SilenceUsage/Errors on; Execute prints
  cobra-level failures itself as `ccx: <err>` to stdout) — the ported
  bash-ccx tests pin those texts through `cli.Execute`.
- zsh does not word-split an unquoted `$var` — manual multi-word CLI checks
  need explicit arguments or `${=var}` (verified live: a loop over
  "serve --provider x …" tested the shortcut path instead of serve).

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

The live `~/.ccx` is not a stable fixture: it routes the very session
working on this code, and the owner may switch profiles or hand-move files
mid-task (verified live: state.json was hand-renamed while the rename change
was being built, and a blind hand-copy would have clobbered the fresh file) —
re-check live paths immediately before any write into them.

Diagnostic shape of "copilot stopped answering" (verified live 2026-11-07,
a full evening traced via usage log + daemon.log + settings backups +
zsh history + CC transcripts): `ENOTFOUND` in the client error means the
direct provider's hostname DNS — never the daemon (127.0.0.1 resolves no
name); a bare `Connection error.` right after a switch to a direct
anthropic profile is the switcher's by-design silent daemon stop while the
running session's env still says 127.0.0.1:4567 — a stale-daemon
diagnosis must first check what settings.json pointed at WHEN the failing
session started (backups/ carry the timestamped sequence). daemon.log
records every spawn but NO stop: `/admin/stop` exits silently (pidfile
removed) and SIGTERM logs `signal — shutting down` — absence of that line
means no signal escalation ever ran.

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
