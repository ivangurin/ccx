# Per-provider authentication (`ccx auth <provider>`)

Date: 2026-11-07
Status: approved design (pending spec review)

## Summary

Authentication becomes per-provider: `ccx auth <key>` resolves the provider in
`providers.json` and runs the login flow its protocol supports — the GitHub
OAuth device flow for `copilot` and a new ChatGPT OAuth PKCE login for `codex`
(no codex CLI involvement anymore). Credentials are stored per provider in
`<CCX_HOME>/auth/<key>.json`. Protocols without a login flow (anthropic,
openai) answer "does not support auth". The flat `copilot-auth.json` store,
the `auth_file` profile field, and the `~/.codex/auth.json` default are
removed — a clean break, existing logins are re-done once per provider.

## Motivation

- Two providers can share a protocol (two GitHub accounts, two ChatGPT
  accounts). Protocol-keyed auth (`ccx auth copilot`) cannot express that:
  one store per protocol means one account per protocol.
- `ccx auth codex` was a hint, not a command: the login lived in the codex
  CLI, so a second ChatGPT account was impossible and ccx depended on an
  external tool's file format by necessity (the coexistence flock/generation
  machinery exists only because that file belonged to someone else).
- The provider key is the natural identity: it already names the route
  (`--provider <key>`), the state file, and the menu; credentials join them.

## Non-goals

- Migrating existing tokens (user decision: clean break). The flat
  `copilot-auth.json` and `~/.codex/auth.json` are simply no longer read;
  files already on disk are left in place, never deleted.
- Re-login/logout commands, token inspection, account switching mid-session
  — `ccx auth <key>` overwrites the store, which covers re-login.
- Auth for other protocols: anthropic/openai credentials are config
  (`api_key`/`auth_token` in providers.json), not OAuth flows.

## Design

### 1. CLI surface

```
ccx auth              list providers and their auth state
ccx auth <key>        run the login flow for that provider
```

- No arguments: one line per provider from `providers.json` —
  `<key> (<protocol>)` plus the auth state: `authorized` / `not authorized`
  for copilot and codex (a cheap local file check: copilot —
  `user_access_token` non-empty; codex — access or refresh token non-empty),
  `auth not supported` for other protocols. No network calls.
- Unknown key: `ccx: unknown provider <key>` (exit 1) — same wording as
  elsewhere in the CLI.
- Unsupported protocol: `ccx: provider <key> (protocol anthropic) does not
  support auth` (exit 1). The protocol is named so the message is
  self-explaining in the shell history.
- Extra arguments: usage error (exit 1).
- Usage text in `cli.go`: `ccx auth [provider]  login flow (copilot/codex)`.

### 2. Storage: one credential store per provider

- Path policy lives in `config` next to the other path helpers:
  `AuthStorePath(key) = <CCX_HOME>/auth/<key>.json` (no env override —
  `CCX_HOME` already moves the whole root). The directory is created on
  first write — `jsonfile.WriteAtomicJSON` already MkdirAll's the target's
  directory, so no new plumbing.
- The file format is the protocol's existing one, unchanged:
  - copilot — the current `authStore` JSON (`user_access_token`,
    `copilot_token`, `expires_at`), written by the existing client.
  - codex — the auth.json-compatible format the codex client already
    reads/writes (`auth_mode`, `last_refresh`, `tokens{…}`). The file is
    ccx-owned now, but the format and the flock sidecar stay: two ccx
    processes (daemon refresh + `ccx auth`) can still race on it.
- Provider keys become filenames, so `LoadProviders` rejects keys that are
  not filename-safe: empty, containing `/` or `\`, or equal to `.`/`..`.
  One check at the single load point — a bad key surfaces at every entry
  point with `provider key %q is not usable as a file name` instead of
  escaping the auth directory later.

### 3. Copilot flow (existing mechanics, new store path)

`ccx auth <copilot-key>` runs the unchanged device flow
(`StartDeviceFlow` → user confirms → `PollDeviceFlow`) with the client
constructed over `AuthStorePath(key)`. Nothing else about the flow changes;
its error strings switch from "run ccx auth copilot" to
"run ccx auth <provider>" (the router wrapper already prefixes the provider
key). The CLI builds the client through a package-level constructor var so
tests can substitute a fake — the same pattern as `copilotQuota` in
`command_status.go`.

### 4. Codex login (new: `internal/clients/codex/login.go`)

A standalone `Login(ctx, authPath)` (plus a base-URL override variant for
tests, mirroring `NewWithBase`) implementing the codex CLI's OAuth flow with
the constants the client already holds (`AuthBase`, `ClientID`):

1. **PKCE**: `code_verifier` = 32 random bytes, base64url (43 chars, RFC
   7636); `code_challenge` = base64url(SHA-256(verifier)), method `S256`.
   `state` = 16 random bytes hex.
2. **Callback listener**: the issuer's redirect-URI allow-list is exact
   (Hydra — "Keep in sync with the Codex CLI Hydra redirect URI allow-list",
   codex-rs login/server.rs), so the listener binds the REGISTERED ports:
   `http://localhost:1455/auth/callback`, falling back to
   `http://localhost:1457/auth/callback` when 1455 is busy (a parked
   earlier login is first nudged with `GET /cancel`, the codex CLI's own
   unblock protocol — the callback server answers `/cancel` by exiting the
   login). The handler validates `state`, answers with a minimal HTML page
   ("return to the terminal"), and hands the code to the exchange. One-shot
   server: shuts down on first callback or context cancel.
3. **Authorize URL**: `{AuthBase}/oauth/authorize?client_id&response_type=code&
   redirect_uri&scope=openid profile email offline_access
   api.connectors.read api.connectors.invoke&code_challenge&
   code_challenge_method=S256&state&id_token_add_organizations=true&
   codex_cli_simplified_flow=true&originator=codex_cli_rs` — the codex
   CLI's exact parameter set (codex-rs login/server.rs build_authorize_url).
   The URL is opened via `open` (darwin)
   / `xdg-open` and always printed too (SSH/no-browser environments).
4. **Exchange**: `POST {AuthBase}/oauth/token` — the endpoint the refresh
   already uses — with `grant_type=authorization_code`, `code`,
   `redirect_uri`, `client_id`, `code_verifier`. Response carries
   `access_token`, `refresh_token`, `id_token`.
5. **account_id**: decoded from the `id_token` JWT payload, claim
   `https://api.openai.com/auth` → `chatgpt_account_id` (the codex CLI's
   derivation). Best effort: absent claim → empty account id, and the
   translator then omits `Chatgpt-Account-Id` (its existing behavior for an
   empty value).
6. **Persist**: through the existing `SaveAuth` — the path documented as
   exactly this seeding use. It gains one behavior: a freshly created file
   gets `auth_mode: "chatgpt"` (mirroring the codex CLI's file).
7. **Budget**: 15-minute overall context (copilot parity); a cancel
   (Ctrl-C) stops the listener cleanly.

The login runs only in the CLI process; the daemon keeps its read/refresh
role and never drives the browser flow.

**Resolved (verified live)**: `auth.openai.com` does NOT accept an
ephemeral localhost port in `redirect_uri` for this client_id — the browser
login completes but the issuer never redirects (Hydra allow-list). The
design above is the post-verification shape: fixed registered ports
1455/1457, path `/auth/callback`, authorize endpoint `/oauth/authorize`
with the codex CLI's parameter set. An earlier draft assumed
`127.0.0.1:0` + `/callback`; that flow hangs after a successful login
(reproduced live).

### 5. Consumer rewiring

- `router.buildTranslator` takes the provider key alongside the profile and
  builds both clients over `cfg.AuthStorePath(key)`:
  - copilot: `copilot_client.New(cfg.AuthStorePath(key))`;
  - codex: `codex.New(cfg.AuthStorePath(key))`.
- `ccx status` quota panel uses the active provider's store path (the key
  from `ccx-state.json`).
- `ccx init` codex template drops the `auth_file` field (a comment now
  documents `ccx auth <key>` instead of pointing at `~/.codex/auth.json`).

### 6. Deletions

- `models.Profile.AuthFile` (`auth_file`); unknown fields are ignored on
  load, so existing files carrying it keep parsing and drop it on the next
  save.
- The `auth_file` branch and `expandHome` in `router`.
- The flat copilot store: path joins in `command_auth`/`command_status`/
  `router`, and the legacy copy `ccms/copilot-auth.json →
  <CCX_HOME>/copilot-auth.json` in `config/legacy.go` (no reader left).
- The `~/.codex/auth.json` default (`codex.DefaultAuthPath`) and the
  "codex login" hint branch of `runAuth`.

### 7. Error messages

| Situation | Message |
| --- | --- |
| unknown key | `ccx: unknown provider <key>` |
| unsupported protocol | `ccx: provider <key> (protocol <p>) does not support auth` |
| copilot, no store | `copilot: not authenticated — run ccx auth <provider>` |
| codex, no store | `codex: not authenticated — run ccx auth <provider>` |
| codex, expired without refresh | `codex: access expired, no refresh token — run ccx auth <provider>` |

The daemon surfaces these through the existing `router: provider <key> not
ready:` wrapper at startup; `/health` then never answers and a switch leaves
`settings.json` untouched (readiness invariant unchanged).

### 8. Testing

- CLI (`command_auth`): no-args listing (authorized/not/unsupported
  states), unknown key, unsupported protocol, extra args, copilot happy
  path via the injectable constructor writing to
  `<home>/auth/<key>.json`, codex dispatch via an injectable login func.
- codex login: authorize URL parameters (S256 challenge, state, scope,
  redirect_uri), callback handler (correct state, wrong state → error),
  token exchange against an httptest fake, `account_id` extraction from a
  crafted `id_token`, resulting file contents (`auth_mode`, tokens,
  `last_refresh`), SaveAuth flock behavior unchanged (existing suite).
- copilot client: unchanged flow suite; message updates only.
- config: `AuthStorePath`, `LoadProviders` key validation (separator,
  `..`, empty) + tolerant load of a file still carrying `auth_file`.
- router: copilot/codex translators wired to the per-provider store path.
- Suite fixtures: the codex template change and removed `auth_file` ripple
  through `ccx init` tests.
- Coverage stays ≥ 80%.

### 9. Documentation

- README: auth section rewritten (`ccx auth <key>`, the auth directory,
  two-providers example, removal of the codex CLI prerequisite and
  `auth_file`), command table rows updated.
- CLAUDE.md: data ownership (`~/.ccx/auth/<key>.json` per-provider
  credential stores), the copilot/codex prerequisites in the manual
  verification section, `auth` in the layer map (`clients/copilot`,
  `clients/codex` descriptions).
