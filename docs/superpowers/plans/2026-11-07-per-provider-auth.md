# Per-Provider Authentication Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `ccx auth <key>` runs the login flow of that provider's protocol (copilot device flow, new codex PKCE login) and stores credentials per provider in `<CCX_HOME>/auth/<key>.json`; flat stores and `auth_file` are removed.

**Architecture:** Path policy is one new helper in `config` (`AuthStorePath`) plus key validation at `LoadProviders`; the codex login is a new file in the existing codex client reusing its constants, token endpoint and `SaveAuth` seeding path; the CLI dispatches by protocol with injectable constructors (existing `copilotQuota`-var pattern); router/status switch to per-provider store paths.

**Tech Stack:** Go 1.27, stdlib `crypto/rand`+`crypto/sha256`+`net/http` for PKCE, testify `require`, `internal/pkg/suite/provider` fixtures.

**Spec:** `docs/superpowers/specs/2026-11-07-per-provider-auth-design.md`

## Global Constraints

- Coverage ≥ 80% (`make test`, `-coverpkg=./...`); tests via testify `require` and `suite_provider.NewProvider(t)`.
- One file per method (`switcher/apply.go` style); one test file per method (`foo_test.go`).
- Token files 0600 (existing `jsonfile.WriteAtomicJSON` handles perms + MkdirAll).
- Tokens/secrets are never logged; all artifacts (code, comments, commit messages, docs) in English.
- No `Co-Authored-By: Claude` trailer in commits.
- Verified fact: curl probes of `auth.openai.com/authorize` hit a Cloudflare interstitial — redirect_uri acceptance is only decidable in a real browser (Task 6 live step). Token endpoint `/oauth/token` from Go is proven (the refresh uses it in production).

## Review Focus

1. Provider key that is a path (`a/b`, `..`, empty) in providers.json → every command fails at load with a clear error; ccx never writes outside `<CCX_HOME>/auth/`. → Task 1, `TestLoadProviders_RejectsNonFileNameKeys`.
2. Re-login over an existing codex store → `SaveAuth` preserves foreign fields (`id_token`, existing `auth_mode`) and only seeds `auth_mode` on a fresh file. → Task 2, `TestSaveAuth_PreservesForeignFields` (extend the existing authfix suite).
3. OAuth failure callback (`?error=access_denied`) and a state mismatch → login returns an error, no file is written. → Task 3, `TestLogin_CallbackErrorParam` / `TestLogin_StateMismatch`.
4. Ctrl-C / context cancel while waiting for the callback → listener closed, error returned, no file. → Task 3, `TestLogin_ContextCancelled`.
5. `ccx auth` with providers.json missing → the existing `ccx init` hint path, exit 1, no panic. → Task 4, `TestCommandAuth_NoArgs_MissingProviders`.

---

### Task 1: `config.AuthStorePath` + provider-key validation

**Files:**
- Modify: `internal/config/providers.go`
- Test: `internal/config/providers_test.go`

**Interfaces:**
- Consumes: `models.Providers` iteration (see `internal/models/ordered.go`).
- Produces: `func (c *Config) AuthStorePath(providerKey string) string` returning `filepath.Join(c.Home, "auth", providerKey+".json")` — Tasks 4 and 5 build every credential store through it.

- [ ] **Step 1: Write the failing tests**

`TestAuthStorePath_JoinsAuthDir`: `AuthStorePath("gh-work")` == `filepath.Join(cfg.Home, "auth", "gh-work.json")`.
`TestLoadProviders_RejectsNonFileNameKeys`: table over provider keys `""`, `"a/b"`, `"a\\b"`, `..`, `.` — `LoadProviders` returns an error containing `provider key` and the key itself; a normal key (`gh-work`) loads fine.
`TestLoadProviders_ToleratesAuthFileResidue`: a file with a profile still carrying `"auth_file": "~/.codex/auth.json"` loads without error (unknown fields are ignored — they die on the next save).

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/config/... -run 'TestAuthStorePath|TestLoadProviders' -count=1`
Expected: FAIL (no `AuthStorePath`, no validation).

- [ ] **Step 3: Implement in `internal/config/providers.go`**

`AuthStorePath` as pinned above. Validation runs in `LoadProviders` after the decode, before the legacy-residue migration (a broken file must surface the unmarshal error first — existing invariant): iterate every provider key and reject via a `validProviderKey(key string) bool` helper (rejects `""`, `.`, `..`, and any key containing `/` or `\`); error text: `providers: provider key %q is not usable as a file name (it names the auth store file)`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/config/... -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

`git add internal/config/providers.go internal/config/providers_test.go && git commit -m "config: per-provider auth store path + filename-safe provider key validation"`

### Task 2: client groundwork — fresh-file `auth_mode`, message rewording, `Authenticated`

**Files:**
- Modify: `internal/clients/codex/client.go`, `internal/clients/copilot/client.go`
- Test: `internal/clients/codex/authfix_test.go`, `internal/clients/copilot/copilot_test.go`

**Interfaces:**
- Consumes: existing `authStore`/`authFile` formats, `LoadAuth`.
- Produces (Task 4 consumes): `copilot.Authenticated(storePath string) bool` (store readable and `user_access_token` non-empty); `codex.Authenticated(authPath string) bool` (`LoadAuth` succeeds).

- [ ] **Step 1: Write the failing tests**

`TestSaveAuth_SeedsAuthModeOnFreshFile`: `SaveAuth` to a missing path → decoded file has `"auth_mode": "chatgpt"` plus the tokens.
`TestSaveAuth_PreservesForeignFields`: pre-write a file with `auth_mode`, `tokens.id_token`, `tokens.account_id`; `SaveAuth` with new tokens → those fields byte-identical, tokens replaced.
`TestAuthenticated_*`: both packages — true for a valid store, false for missing file / empty token.
Message tests: assert the exact strings below where the errors are constructed (grep existing tests for the old copy and update them).

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/clients/... -count=1`
Expected: FAIL on the new tests; existing suites may fail on old copy — that is the point.

- [ ] **Step 3: Implement**

`saveAuthLocked`: after the read, `if f.AuthMode == "" { f.AuthMode = "chatgpt" }` (seeding only — an existing mode is someone else's truth and stays).
`Authenticated` helpers as pinned above.
Reword, keeping prefixes: `copilot: not authenticated — run ccx auth <provider>`; `copilot: user token invalid (401) — run ccx auth <provider>`; `codex: not authenticated — run ccx auth <provider>`; `codex: access expired, no refresh token — run ccx auth <provider>`; every remaining `codex login` mention in errors (`adoptNewerGeneration`, `refresh` status lines) → `run ccx auth <provider>`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/clients/... ./internal/services/router/... -count=1`
Expected: PASS (router suite asserts copilot not-ready copy — update it too if it pins the old string).

- [ ] **Step 5: Commit**

`git add -A internal/clients && git commit -m "clients: per-provider auth wording, fresh-file auth_mode seeding, Authenticated probes"`

### Task 3: codex PKCE login (`internal/clients/codex/login.go`)

**Files:**
- Create: `internal/clients/codex/login.go`
- Test: `internal/clients/codex/login_test.go`

**Interfaces:**
- Consumes: `ClientID`, `AuthBase`, `SaveAuth` (Task 2's `auth_mode` seeding).
- Produces (Task 4 consumes): `func Login(ctx context.Context, authPath string, openBrowser func(string) error) error`. Tests additionally use the unexported `loginWithBase(ctx, authorizeBase, tokenURL, authPath string, openBrowser func(string) error) error` (same package).

- [ ] **Step 1: Write the failing tests** (package `codex`; craft JWTs with `base64.RawURLEncoding.EncodeToString` — same trick as `router_test.go:210`)

`TestLogin_HappyPath`: httptest token server asserts the exchange form (`grant_type=authorization_code`, `code`, `client_id`, `code_verifier` of length 43, `redirect_uri` equal to the callback URL captured from the authorize link) and answers `{access_token, refresh_token, id_token}` where the `id_token` payload carries `"https://api.openai.com/auth": {"chatgpt_account_id": "acct-1"}`. The test plays the browser: capture the authorize URL via `openBrowser`, extract `redirect_uri` and `state`, GET the callback with `?code=c1&state=<state>`. Assert the store file: `auth_mode chatgpt`, tokens saved, `account_id` extracted, `last_refresh` non-empty.
`TestLogin_AuthorizeURLParams`: from the captured URL — `code_challenge_method=S256`, `scope=openid profile email offline_access`, `response_type=code`, non-empty `state` and `code_challenge`, `client_id == ClientID`, `redirect_uri` = `http://localhost:<port>/callback`.
`TestLogin_StateMismatch`: callback with a wrong `state` → error mentioning `state`, store file absent.
`TestLogin_CallbackErrorParam`: callback with `?error=access_denied` → error mentioning `access_denied`, store absent.
`TestLogin_TokenEndpointError`: token server 400 → error containing `status 400`, store absent.
`TestLogin_ContextCancelled`: cancel the context before triggering the callback → error, listener closed (the test's own GET is refused or errors), store absent.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/clients/codex/... -run TestLogin -count=1`
Expected: FAIL (no login.go).

- [ ] **Step 3: Implement `login.go`**

`Login` = `loginWithBase(ctx, AuthBase+"/authorize", AuthBase+"/oauth/token", authPath, openBrowser)`. Flow: verifier = base64url(crypto/rand 32B) (43 chars); challenge = base64url(sha256(verifier)); state = hex(crypto/rand 16B); `net.Listen("tcp", "127.0.0.1:0")`; redirect_uri `http://localhost:<port>/callback`. Build the authorize URL (params as in `TestLogin_AuthorizeURLParams`), call `openBrowser(url)` — an opener error is ignored (the CLI printed the URL; manual open is the fallback). Serve a one-shot `http.Server`: `/callback` validates `state`, short-circuits on `?error=`, answers 200 `text/html` ("return to the terminal"), delivers the code on a buffered channel; `select` on channel vs `ctx.Done()`, then `Shutdown`. Exchange via `POST tokenURL` (form as asserted; reuse a `15 * time.Second` client like `refresh`). `account_id`: decode the `id_token` payload (split `.`, base64url part 1 — same shape as `TokenValid`) and read claim `"https://api.openai.com/auth" → "chatgpt_account_id"`; absent claim → empty id. Persist through `SaveAuth(authPath, Auth{AccessToken, RefreshToken, AccountID})`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/clients/codex/... -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

`git add internal/clients/codex/login.go internal/clients/codex/login_test.go && git commit -m "codex: ChatGPT OAuth PKCE login writing the provider's auth store"`

### Task 4: CLI `ccx auth` rework

**Files:**
- Modify: `internal/cli/command_auth.go` (full rework), `internal/cli/cli.go` (usage line), `internal/cli/use_test.go` (drop `TestCommandAuth_CodexHint`; keep `TestCommandAuth_UnknownProvider`)
- Create: `internal/cli/command_auth_test.go`

**Interfaces:**
- Consumes: `config.AuthStorePath` (T1), `copilot.Authenticated`/`codex.Authenticated` (T2), `codex.Login` (T3), `loadProvidersOrHint` (helpers.go).
- Produces: test seams — `newCopilotAuthClient func(store string) copilotAuthClient` where `copilotAuthClient` is a two-method interface (`StartDeviceFlow(context.Context) (*copilot_client.DeviceFlowResult, error)`; `PollDeviceFlow(context.Context, string) (string, error)`); `codexLogin func(ctx context.Context, authPath string, openBrowser func(string) error) error` (defaults to `codex.Login`).

- [ ] **Step 1: Write the failing tests**

`TestCommandAuth_ListsAuthState`: providers.json with a copilot provider (store file pre-written via `AuthStorePath` → contains `user_access_token`), a codex provider without a store, an anthropic provider; `ccx auth` exit 0; output contains `<key> (copilot) — authorized`, `<key> (codex) — not authorized`, `<key> (anthropic) — auth not supported` (substrings; layout is free).
`TestCommandAuth_NoArgs_MissingProviders`: no providers.json → non-zero, output contains the `ccx init` hint (via `loadProvidersOrHint`).
`TestCommandAuth_UnknownProvider`: unchanged expectation (`unknown provider`).
`TestCommandAuth_UnsupportedProtocol`: `ccx auth zai` (anthropic) → exit 1, exactly `ccx: provider zai (protocol anthropic) does not support auth`.
`TestCommandAuth_CopilotFlow`: swap `newCopilotAuthClient` for a fake whose `PollDeviceFlow` writes the store; run `ccx auth gh`; assert exit 0, the fake received `filepath.Join(sp.HomeDir(), "auth", "gh.json")`, output contains `Done: tokens saved to`.
`TestCommandAuth_CodexFlow`: swap `codexLogin`; `ccx auth cx` → exit 0, called with `AuthStorePath("cx")`, output contains `Done: login saved to`.
`TestCommandAuth_ExtraArgs`: `ccx auth gh extra` → exit 1, usage error.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cli/... -run TestCommandAuth -count=1`
Expected: FAIL (old `runAuth` dispatches on protocol names).

- [ ] **Step 3: Implement `command_auth.go`**

`runAuth`: `len(args) == 1` → listing via `loadProvidersOrHint` + `Authenticated` probes (one line per provider, states `authorized` / `not authorized` / `auth not supported`); `len(args) == 2` → `providers.Providers.Get(args[1])`, nil → `ccx: unknown provider <key>`; dispatch copilot → the existing device-flow body with `store = cfg.AuthStorePath(key)` and `newCopilotAuthClient(store)`; codex → `codexLogin(ctx15min, cfg.AuthStorePath(key), browserOpener)` then `Done: login saved to <path>`; default → the unsupported message above; `len(args) > 2` → usage error. `browserOpener` closes over stdout: prints `Open in browser: <url>`, then `exec open` (darwin) / `xdg-open` (else); its error is swallowed after printing. Usage line in `cli.go` (verbatim from the spec): `ccx auth [provider]  login flow (copilot/codex)`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/cli/... -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

`git add internal/cli/command_auth.go internal/cli/command_auth_test.go internal/cli/cli.go internal/cli/use_test.go && git commit -m "cli: ccx auth <provider> — per-provider login dispatch and auth state listing"`

### Task 5: consumer rewiring + deletions

**Files:**
- Modify: `internal/services/router/router.go`, `internal/services/router/router_test.go`, `internal/cli/command_status.go`, `internal/models/profile.go`, `internal/config/templates.go`, `internal/config/legacy.go`, `internal/config/legacy_test.go`, `internal/clients/codex/client.go`
- Test: extend `internal/cli/command_status_quota_test.go` where it pins the store path

**Interfaces:**
- Consumes: `config.AuthStorePath` (T1).
- Produces: `buildTranslator(cfg *config.Config, providerKey string, profile *models.Profile)`; `printCopilotQuota(cfg *config.Config, providerKey string, stdout io.Writer)`.

- [ ] **Step 1: Write the failing tests**

`TestBuild_CodexProfile_PersonalStore` (replaces `TestBuild_CodexProfile_ExpandsHomeAuthFile`): craft a future-`exp` JWT exactly like the old test, write it to `filepath.Join(sp.HomeDir(), "auth", "cx.json")`, profile without `auth_file` → `router.Build` ready. Add the copilot twin: `TestBuild_CopilotProfile_PersonalStore` — store at `auth/gh.json` with a `user_access_token` → Build ready (fake exchange upstream not needed if the store carries a valid `copilot_token`/`expires_at`, mirroring existing copilot readiness tests).
`TestMigrateLegacyData_NoCopilotCopy` (legacy_test.go): old-world `ccms/copilot-auth.json` present → after migration `<CCX_HOME>/copilot-auth.json` does NOT appear (providers/state copies still do).
Status quota test: pin that the quota client is built over `AuthStorePath(activeKey)` (the existing `copilotQuota` var already intercepts construction — pass the store path into the fake and assert).

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/services/router/... ./internal/config/... ./internal/cli/... -count=1`
Expected: FAIL on the new tests.

- [ ] **Step 3: Implement**

`router.buildTranslator` takes `providerKey`; copilot → `copilot_client.New(cfg.AuthStorePath(providerKey))`, codex → `codex.New(cfg.AuthStorePath(providerKey))`; delete the `AuthFile` branch and `expandHome`. `command_status.go`: `printCopilotQuota(cfg, state.Profile, stdout)` builds the quota client over `cfg.AuthStorePath(state.Profile)`. Delete: `models.Profile.AuthFile` field, the `AuthFile` line in `templates.go` `builtinCodexProfile` (comment → `// login: ccx auth codex`), the copilot copy block in `legacy.go` (doc comment updated: only providers/state migrate now), `codex.DefaultAuthPath` (single consumer removed). Fix every test still referencing `AuthFile`/`auth_file` (grep both spellings).

- [ ] **Step 4: Run the full suite**

Run: `make test`
Expected: PASS, coverage ≥ 80%.

- [ ] **Step 5: Commit**

`git add -A && git commit -m "router/status: per-provider auth stores; drop auth_file, flat copilot store, legacy copilot copy"`

### Task 6: docs + live verification

**Files:**
- Modify: `README.md`, `CLAUDE.md`

**Interfaces:** none (documentation + a manual check).

- [ ] **Step 1: README**

Rewrite the auth prerequisites block (lines ~50–53, ~95), the command table rows (`ccx auth copilot` / `ccx auth codex` → `ccx auth [provider]`), the credentials note (~145, `~/.codex/auth.json` mention), drop every `auth_file` mention; add the `<CCX_HOME>/auth/<key>.json` store and a two-copilot-providers example.

- [ ] **Step 2: CLAUDE.md**

Data ownership: `~/.ccx` now also owns `auth/<provider>.json` per-provider credential stores; update the copilot token store mention and the codex `auth_file` mention in the layer map; manual verification section: prerequisites become `ccx auth <key>` (copilot cached token / codex login no longer phrased as external), and the codex prerequisite sentence about `~/.codex/auth.json` is replaced by the per-provider store.

- [ ] **Step 3: Live check with the human partner**

`./bin/ccx auth <their-codex-key>` in a sandbox `CCX_HOME` — the real browser decides the open redirect_uri question (curl cannot: Cloudflare interstitial). If auth.openai.com rejects the ephemeral-port redirect after login, switch `login.go` to the codex CLI's exact `http://localhost:1455/auth/callback` (constant + `TestLogin_AuthorizeURLParams` update) and re-run. Then a live `ccx auth <their-copilot-key>` re-login and `make lint` + `make test`.

- [ ] **Step 4: Commit**

`git add README.md CLAUDE.md docs/ && git commit -m "docs: per-provider authentication"`
