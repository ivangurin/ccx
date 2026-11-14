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

Releasing = push an annotated tag `v*` to origin: `.github/workflows/release.yml`
triggers on the tag, bakes `GITHUB_REF_NAME` into `cli.Version` via ldflags
(no version bump commit — the code itself only carries `dev`), attaches
`ccx-mac-<arch>-<tag>.zip` for BOTH darwin/arm64 and darwin/amd64 (the zip
carries the binary AS `ccx` at its root — a per-arch build name at the zip
root breaks the formula's `bin "ccx"` and install.sh; verified live,
v0.4.0 first run), and regenerates `Formula/ccx.rb` in
`ivangurin/homebrew-tap` (pushed via the `TAP_DEPLOY_KEY` deploy key
secret — a write-scoped key, not a PAT, because `GITHUB_TOKEN` cannot push
outside its own repo and PATs cannot be minted via API);
`brew install ivangurin/tap/ccx` tracks the latest release; install.sh maps
`uname -m` (arm64/x86_64) onto the asset arch; tag messages are release-note
style. Re-releasing the same tag: delete the release AND the remote tag,
re-tag, push — but note GitHub's CDN caches release assets by URL: the
re-created release can serve STALE zip bytes for a while (verified live
2026-11-09 — `?nocache=<timestamp>` on the download URL bypasses it; the
API's `updated_at` tells fresh from stale), and `gh release delete` does
NOT remove the remote tag — a force-pushed re-tag does not refresh it
either; push the tag ref explicitly (`git push origin refs/tags/<tag>`)
or `gh release create` fails with "tag exists locally but has not been
pushed" (verified live, v0.4.0). The tap formula's install step is
`bin.install "ccx"` — `bin` is the destination PATHNAME (prefix/bin), not
an install method; `bin "ccx"` dies inside the build child with an
opaque `ArgumentError: wrong number of arguments` (backtrace pinned to
Formula#bin), the only reliable diagnosis is `brew install --formula` on
a local probe tap or reading formula.rb:1155.

## Architecture

CLI (profile switching, editing `~/.claude/settings.json`) + daemon (routing
the Anthropic API to the launched provider's translator; switching = daemon
restart with new launch params). Data ownership: ccx owns its data root
`~/.ccx` (`CCX_HOME`) — providers.json, state.json, logs/,
daemon.lock/pid, switch.lock (the switcher's flock; was the hidden
bash-ccx `.ccx.lock`, renamed the same day as state.json), the
per-provider credential stores auth/<key>.json; of
Claude Code's `~/.claude` only
`settings.json` is touched (edited in place, never moved; `CCX_SETTINGS`
redirects it) plus its one-time backup: the first switch freezes the
original to a fixed `settings.json.bak` next to the file (0600,
`O_EXCL` — later switches never overwrite it; `ccx restore` copies it
back and stops the daemon; known limit for installs that lived through
the old accumulating scheme ≤ v0.7.0: their first post-upgrade switch
freezes an ALREADY ccx-managed file — the pristine pre-ccx original
sits as the oldest file in ~/.ccx/backups and nothing imports it;
restore resets `state.json` to an empty provider — rollback/status read
it, and a stale record after a restore would make the "you stay on the
previous provider" promise a lie). The pre-rename one-time data move from `~/.claude` is gone
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
                                         --provider/--port/--debug args) via os.Executable():
                                         the daemon always re-executes the very binary the
                                         user invoked (dev ./bin/ccx or brew ccx), never a
                                         PATH lookup (verified live 2026-11-11)
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
    switcher/                           port of bash-ccx apply_profile: flock, one-time original backup
                                         (settings.json.bak, 0600, create-once) + restore, atomic,
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

- **Model schema is id/label-only** (2026-11-14): a model entry is
  `id` + `label` (+ `default`/`max_context_tokens`/`effort`) — the historical
  `name` alias field is GONE: `ResolveModelEntry` matches by exact id (a bare
  id without the `[1m]` suffix does NOT match the bracketed one), a legacy
  `name` in providers.json is ignored on load and dropped on the next save;
  the model picker (`ccx` menu, TUI and fallback) and `ccx list` render the
  label only (id as fallback), so the filter matches labels.
- **Readiness before settings.json**: switching a translated profile
  (openai/copilot/codex) restarts the daemon with `--provider`/`--port`
  (plus `--debug` when `daemon.debug` is set) and waits for `/health` —
  which answering means the translator's `Ready()` passed (the daemon
  builds the route before listening); on failure, `~/.claude/settings.json`
  is left untouched and the user stays on the previous profile — ENFORCED,
  not just printed: switchAborted respawns the daemon for the provider
  named by state.json (a failed Restart already killed it; a settings-write
  failure after a successful Restart leaves the NEW daemon serving under an
  untouched settings.json). A direct-anthropic previous provider needs no
  rollback; a failed rollback names the recovery command in the error.
  Restart
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
- **Managed keys**: only `ANTHROPIC_*` keys, profile env keys, the
  `max_context_tokens` trio and the `effort` key (see Model pinning) and the
  always-env flags are edited; user variables (API_TIMEOUT_MS, MCP tokens)
  are preserved — an always-env key the user set by hand keeps its value
  (add-if-missing, never overwrite).
- **Byte-precise settings.json edits**: only the env block and the model pin
  change; everything else — key order, indentation, user blocks, the trailing
  newline — survives a switch byte-for-byte (`internal/pkg/jsonpatch`; a fresh
  env key appends at the end of the block, a replaced one keeps its line).
  Deliberately stricter than bash-ccx, whose jq round-trip reformatted the
  whole file.
- **Model pinning**: `model`, `availableModels=[m]`, `enforceAvailableModels`,
  and exactly ONE env pin `ANTHROPIC_MODEL` = the selected model (2026-11-10:
  the legacy `ANTHROPIC_DEFAULT_*_MODEL`/`ANTHROPIC_SMALL_FAST_MODEL`
  multi-mapping is not written anymore — a leftover in settings.json dies on
  the next switch via the `ANTHROPIC_*` stale sweep). A model entry may carry
  `max_context_tokens` (providers.json): when set, the switch writes
  `CLAUDE_CODE_DISABLE_1M_CONTEXT=1`, `CLAUDE_CODE_MAX_CONTEXT_TOKENS=<v>`,
  `CLAUDE_CODE_AUTO_COMPACT_WINDOW=<v − 10%>`; when unset the trio is wiped —
  the keys are ccx-owned, a manual value dies too. A model entry may also
  carry `effort` → `CLAUDE_CODE_EFFORT_LEVEL`, written verbatim (no
  validation); when unset the key is wiped — ccx-owned, a manual value dies
  too. Always-env (add-if-missing,
  never overwritten: a user value or a profile extra wins, a stale profile
  extra being wiped falls back to the default): 
  `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1`,
  `CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1`, `CLAUDE_CODE_AUTO_MODE_SERVER=0`.
- **`ANTHROPIC_BASE_URL`**: always the daemon for translated protocols (it
  never changes when switching between them); the upstream URL itself for
  direct anthropic profiles. The credential written alongside is the
  provider's own (`auth_token` > `api_key` > placeholder `ccx`): for openai
  it is pass-through — Claude Code sends it, the daemon forwards it upstream
  unchanged.
- **Env references in credential fields** (2026-11-11): `auth_token`/`api_key`
  in providers.json may be a whole-value env reference `$NAME`/`${NAME}` —
  the secret lives in the owner's environment (Claude Code's settings.json
  `env` block does NO interpolation: values are copied as written, so a
  reference there would reach the API as a literal `$NAME`). Expansion
  happens ONLY at the points of consumption — `config.ExpandCredentials`:
  switcher.applyLocked (BEFORE the daemon restart and any write — a missing
  var must not kill the live daemon), tester.testProvider (before the
  structural check — fa8099f semantics, no daemon lift), router.Build
  (before listening) — never on load: LoadProviders' migration rewrite
  (SaveProviders) would persist the expanded secret over the reference.
  Unset or EMPTY variable = hard fail before any side effect; no recursion
  (a var whose value is a reference comes back as that literal text); no
  inline splices (`$NAME-x`, `pre-$NAME` are literal). providers.json keeps
  the reference forever; only settings.json / the Bearer ever see the secret.
- **`state.json`** `~/.ccx/state.json` `{profile, model, updated}` —
  format is 1:1 with bash-ccx's state file (statusline.sh reads it — point
  its path at `~/.ccx/state.json`; the JSON schema is unchanged). Renamed
  from `ccx-state.json` (2026-11-07) with no fallback read: the pre-rename
  file stays on disk unread, same clean break as the legacy data migration.
  statusline.sh also renders `effort.level` (low/medium/high/xhigh/max) from
  the statusline stdin JSON — the whole `effort` object is ABSENT on models
  without effort support, so the segment prints only when present (2026-11-14,
  docs + mock runs; jq fallback `// empty`, no dangling `|`).
- Atomic writes: tmp file in the same directory + rename (one implementation:
  jsonfile.WriteAtomicJSON/WriteAtomicRaw); the backup and ccx-owned configs are
  0600, but an EXISTING target keeps its own perms (settings.json belongs to
  Claude Code — a switch must not silently tighten it).
- **Daemon Stop is home-scoped (foreign-daemon refusal)**: the port is a
  machine-global namespace, CCX_HOME is not — switcher Stop admin-stops only
  a daemon whose pidfile (in the CALLER's home) names a live OWN process
  (`ownsCCX`, ps comm == our binary — NOT a bare signal-0 `pidAlive`, which
  counts any live process including another user's via its EPERM branch); anything
  else answering on the port is a foreign home's daemon and gets an explicit
  error (a direct-profile switch prints a note and stays committed; Restart
  fails fast instead of losing the bind race). ownsCCX in the gate is not
  optional hardening: a SIGKILLed daemon leaves its pidfile behind (the
  defer never runs), so stale pidfiles are ROUTINE, and a reused pid must
  neither be signaled nor admin-stopped. Root cause of the 2026-11-09
  "daemon dies silently, ECONNREFUSED, only model re-selection helps" day:
  `go test`/`make test` runs from parallel Claude Code sessions killed the
  LIVE x5 daemon — the suite sandboxes CCX_HOME/CCX_SETTINGS but not the
  port, fixtures pinned 4567, and the old health-gated Stop trusted
  whatever answered there (verified live: the /admin/* access log named the
  killer — `POST /admin/stop` seconds after `go test ./...`; every death of
  the day matches a test run to the second).
- **Ownership-gate fixtures must name an own-named process**: a test
  pidfile pointing at `/bin/sleep` passes the old pidAlive gate but is
  correctly refused by ownsCCX — the fixture child must re-run the test
  binary itself (`selfChild`/`ownTestChild` + a `GO_WANT_HELPER_PROCESS`
  sleep payload), because ps comm is the identity the gate checks.
- **Daemon exit observability**: SIGHUP is handled like SIGTERM (an
  unhandled SIGHUP kills a Go process with zero output — a whole
  silent-death class), the ctx-cancel exit (the /admin/stop path) logs
  `daemon stopping`, every /admin/* request is logged with its remote addr,
  `--debug` runs a 10s self-dial of own /health (`self-check: listener not
  answering` = alive process, broken listener), and Run starts with a
  `daemon launch` snapshot (pid/ppid/sid/rlimit). `ccx status` probes
  /health and prints `Daemon: up/down/not used (direct)` — it used to show
  nothing about the daemon, which fed a day of "the daemon IS running"
  misdiagnosis (verified live 2026-11-09).

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
  window live with `--debug` to see the raw incomplete event.
- **One unified daemon log** (2026-11-09): everything a daemon emits goes
  into the single `daemon-<launch time>.log` (one per launch, the spawned
  child's stderr redirect — no file sink inside the process) as JSONL lines
  discriminated by the `kind` attribute: plain slog records carry no kind,
  usage is `kind=usage`, request dumps are `kind=reqdump`. The separate
  `usage-YYYY-MM-DD.jsonl` and `dump-<launch time>.log` files are GONE
  (CCX_DUMP_DIR with them); a day's usage slice is
  `jq -c 'select(.kind=="usage")' logs/daemon-*.log` — the recipe reads the
  JSON format; usage lines are info+json PINNED (kind=usage always on a
  JSONL line regardless of CCX_LOG_LEVEL/CCX_LOG_FORMAT), so it works even
  when the diagnostics stream is dev/text or warn-level. JSONL integrity by
  construction: one writer process, slog's handler mutex, one Write per
  line; a dump body rides as an attribute — `json.RawMessage` embeds into
  slog's JSON output VERBATIM (a nested object, not a string — verified
  live), a non-JSON body is handed over as a plain string and escaped by
  the handler, so a broken line is impossible by either path.
  `json.Valid`-gating in reqdump decides which of the two encodings applies.
  A test logger writing into a plain `bytes.Buffer` from a background
  goroutine is a -race FAILURE — wrap it in a mutex (`syncBuffer` /
  `suite_provider.Capture`), `require.Eventually` polls it from the test
  goroutine concurrently. In-process `app.Run` tests must assert on the
  writer passed to Run, NOT on `logs/daemon-*.log` files — the file is
  materialized only by the spawn redirect, an in-process daemon creates no
  file (verified live).
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
- **Usage log**: rides the unified daemon log as `kind=usage` lines
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
- The `[1m]` bracket suffix on a model id (Claude Code's 1M-context
  marker) is a CLIENT-side convention — z.ai accepted `glm-5.3[1m]` on
  2026-11-07..09 morning and began rejecting it mid-day 2026-11-09 with
  `1211 Unknown Model` while `/v1/models` lists only bare ids (verified
  live, both sides); anything probing an upstream must strip the bracket
  suffix for anthropic profiles (`tester.probeID`).
- Go treats `command_test.go` in a non-test package as a TEST file (the
  `_test.go` suffix wins over any convention) — a source file named that
  silently drops out of `go build` and its symbols vanish ("undefined:
  runTest" in cli.go); the fix is a different name (`command_testcmd.go`).
  Same trap shape: a package-level const initialized from an identically
  named const (`probeModelID = probeModelID`) is an initialization cycle
  after a mechanical perl rename — check the const block after scripted
  edits (verified live, 2026-11-09, ccx test work).

## Testing

- **Never pin port 4567 in test fixtures** — it is the machine-global port
  of the developer's LIVE daemon; a fixture resolving to it turns the test
  suite into a daemon killer (see the foreign-daemon refusal invariant;
  verified live 2026-11-09). Fixtures take an ephemeral port (`freePort`),
  and tests driving Stop/Restart write a live pid into the sandbox
  daemon.pid — the ownership gate refuses anything else by design.

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
a full evening traced via usage log + daemon-*.log + zsh history + CC
transcripts): `ENOTFOUND` in the client error means the
direct provider's hostname DNS — never the daemon (127.0.0.1 resolves no
name); a bare `Connection error.` right after a switch to a direct
anthropic profile is the switcher's by-design silent daemon stop while the
running session's env still says 127.0.0.1:4567 — a stale-daemon
diagnosis must first check what settings.json pointed at WHEN the failing
session started. daemon-*.log
(one file per launch) records every spawn but NO stop: `/admin/stop`
exits silently (pidfile removed) and SIGTERM logs `signal — shutting
down` — absence of that line means no signal escalation ever ran.

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
