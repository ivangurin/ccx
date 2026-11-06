# Daemon restart with launch parameters (`--provider`/`--port`)

Date: 2026-11-06
Status: approved design (pending spec review)

## Summary

Replace the daemon's in-process route switching (`/admin/use`, state restore)
with restart-on-switch: the daemon is launched with the selected provider as a
launch parameter and derives everything it needs for proxying from
`providers.json` itself. For `openai` profiles, upstream authorization becomes
pass-through: the token Claude Code sends (the provider's own credential from
`settings.json`) is forwarded to the upstream verbatim. `version` and
`daemon.auth_token` are removed from `providers.json`; `daemon.port` becomes
optional with default 4567.

## Motivation

- One mechanism for all translated protocols: any switch (openai, copilot,
  codex) is a daemon restart with explicit parameters — no in-daemon route
  state, no restore path, no `/admin/use`.
- The daemon stops being a secret holder for openai: the provider credential
  lives in `settings.json` (written by the CLI) and flows through the daemon
  per request. `providers.json` keeps `auth_token` only as the value the CLI
  copies into `settings.json`.
- Simpler config: no version bookkeeping, no generated daemon token.

## Non-goals

- Streaming continuity across a switch: a restart kills in-flight SSE
  requests. Accepted explicitly (the user chose restart semantics).
- Changing the direct anthropic path (upstream URL + credential straight into
  `settings.json`, best-effort daemon stop) — untouched.
- Changing the providers.json profile layout (v3 flat profiles stay; only the
  root `version` and `daemon.auth_token` fields go away).

## Design

### 1. providers.json schema

Root becomes `{ "daemon": { "port": 1234 }, "profiles": { ... } }`:

- `version` — removed from the struct; `LoadProviders` drops the version
  check and `ErrUnsupportedVersion`. Existing files containing `"version": 3`
  still parse (unknown fields are ignored by the decoder); `ccms init`
  stops writing the field.
- `daemon.auth_token` — removed. `Daemon` struct keeps only `Port`
  (`json:"port,omitempty"`); the whole `daemon` block (and `port` within it)
  is optional. Zero/absent port means "default".

Port resolution, one helper in `config` used by CLI and daemon alike:

```
--port flag > CCMS_DAEMON_PORT env > providers.json daemon.port > 4567
```

The spawner always passes `--port` explicitly (computed by this precedence),
so the daemon's listen port and the `ANTHROPIC_BASE_URL` written into
`settings.json` agree by construction; the flag stays optional for manual
`ccms serve` runs.

### 2. Daemon launch and startup

```
ccms serve --daemon --provider <key> [--port N]
```

- `--provider` is required (`serve` without it exits with a usage error —
  there is no state to restore anymore).
- `--model` is NOT passed: the model arrives in every request from Claude
  Code (pinned in `settings.json`); the daemon has no use for it.
- Startup sequence: resolve port → load `providers.json` → look up the
  profile by key → build the translator via the existing protocol factory →
  `Ready(ctx)` → only then start listening.
- `/health` answering == route ready. `/admin/ready` is removed (redundant).
- Any startup failure (unknown provider, broken config, `Ready()` error such
  as a copilot token refresh failure) exits non-zero with the reason in
  `daemon.log`; the CLI's health wait then fails and `settings.json` is left
  untouched.
- Startup lock race: the old daemon may still hold `daemon.lock` for a moment
  after its port closes (graceful shutdown of in-flight requests). Instead of
  exiting quietly on `TryLock` failure, the new daemon retries the lock for up
  to 5 seconds before giving up.
- `/admin/health` reports provider key, protocol, port, version, ready.

### 3. Auth model

- `middleware.Auth` is deleted. All endpoints (`/v1/*`, `/admin/*`) are
  unauthenticated; the daemon binds `127.0.0.1` only (unchanged). There is no
  daemon-owned secret left to validate against.
- `openai` translator: upstream auth is taken from the incoming request —
  `Authorization: Bearer <t>`, falling back to `x-api-key` (Claude Code sends
  one of the two, depending on which env var is set) — and forwarded upstream
  as `Authorization: Bearer <t>`. No incoming token → no Authorization header
  upstream. `profile.api_key` is no longer used for upstream auth.
- `copilot` and `codex` keep their own credential machinery (TokenSource /
  `auth.json`); pass-through does not apply to them. Copilot's translator is
  a separate implementation and is not affected by the openai change.
- The anthropic translator (manual `serve` of an anthropic profile) keeps its
  existing auth-substitution behavior; the CLI never spawns a daemon for
  anthropic profiles (direct path).

### 4. Switcher flow (translated profiles)

Under the switcher flock, after profile/model resolution:

1. `daemon.Restart(ctx, providerKey, port)` — the former `Ensure`+`Use`
   collapse into one operation:
   - stop a running daemon if any: `POST /admin/stop`, wait for the port to
     close (bounded, ~10s), then `SIGTERM` to the pid from `daemon.pid`, then
     `SIGKILL` (belt and braces; graceful shutdown can hang on long SSE);
   - `SpawnDetached(exe serve --daemon --provider <key> --port <port>)`;
   - wait for `/health` (existing `WaitForHealth`).
2. On success, write `settings.json` (byte-precise patch, managed keys,
   stale-key removal — unchanged):
   - `ANTHROPIC_BASE_URL = http://127.0.0.1:<port>`;
   - credential from the profile, direct-path precedence:
     `auth_token` → `ANTHROPIC_AUTH_TOKEN`, else `api_key` →
     `ANTHROPIC_API_KEY`, else a placeholder `ANTHROPIC_AUTH_TOKEN = "ccms"`
     (Claude Code expects a credential to be present; the daemon ignores it
     for non-pass-through protocols);
   - model pinning (`ANTHROPIC_MODEL`, all `ANTHROPIC_DEFAULT_*_MODEL`,
     `ANTHROPIC_SMALL_FAST_MODEL`) — unchanged.
3. Write `ccms-state.json` `{profile, model, updated}` — unchanged (the
   statusline keeps working; the daemon no longer reads it).

Failure at any point before the settings write leaves `settings.json`
untouched — the readiness invariant is preserved (readiness now = the
restarted daemon answering `/health`, which implies `Ready()` passed).

The direct anthropic path is unchanged, including its best-effort daemon
stop after the committed switch.

### 5. HTTP surface

Kept: `GET /health`, `GET /admin/health`, `POST /admin/stop`, `POST
/v1/messages`, `POST /v1/messages/count_tokens`, `GET /v1/models` (models of
the provider named at launch). All without auth, localhost-only.

Removed: `POST /admin/use`, `GET /admin/ready`, the auth middleware.

### 6. Deletions

- `internal/api/middleware/auth.go` (whole package) and its tests.
- `router.Use`, `router.Restore`, `router.AuthToken`, the atomic route swap —
  the router service degenerates to "build one translator at startup and
  serve it".
- `HTTPDaemon.Use` (admin API client); the `Daemon` interface becomes
  `Restart` + `Stop`.
- Token generation in `ccms init`; `ErrUnsupportedVersion`; the `version`
  check in `LoadProviders`.
- Token plumbing: `handlers.AuthToken` interface method, `command_stop` and
  `daemon.go` token lookups.

### 7. Testing

- Suite provider: providers.json fixtures without `version`/`auth_token`.
- New/changed tests: port resolution precedence; serve flag validation;
  daemon startup failure (unknown provider) exits non-zero; token pass-through
  (present, x-api-key fallback, absent) against an httptest upstream; restart
  flow against a fake daemon (injectable spawn) — stop→spawn→health ordering;
  settings.json contents for translated profiles (URL, credential variants,
  placeholder); copilot/codex translators unaffected (existing suites).
- Coverage stays ≥ 80%.

### 8. Documentation

CLAUDE.md: architecture (daemon launch params, no auth middleware, default
port 4567, restart-on-switch invariant replacing readiness-before-switch
wording where needed), key invariants section. README: serve usage, example
providers.json without version/auth_token, direct-mode docs untouched.
