# Daemon Restart with Launch Parameters — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace in-daemon route switching (`/admin/use`, state restore) with restart-on-switch: the daemon launches with `--provider <key> [--port N]`, resolves everything from providers.json at startup, and openai upstream auth becomes pass-through.

**Architecture:** The daemon builds one translator at startup (`Ready()` before listening; `/health` answering == ready). The CLI switcher stops any running daemon, respawns with launch flags, waits for health, then patches settings.json. providers.json loses `version` and `daemon.auth_token`; port precedence: `--port` > `CCMS_PORT` > `daemon.port` > 4567.

**Tech Stack:** Go 1.27 stdlib (+ chi, testify, mockery as already in the repo).

**Spec:** `docs/superpowers/specs/2026-11-06-daemon-restart-launch-params-design.md`

## Global Constraints

- One file per service method / handler / translator step; one test file per method (`foo_test.go`). Testify `require`. Test suite provider: `internal/pkg/suite/provider`.
- Coverage ≥ 80% (`make test`, `-coverpkg=./...`). Lint 0 issues (`make lint`).
- Atomic writes (tmp+rename), configs/backups 0600. Tokens/secrets never logged.
- All project artifacts (code, comments, commit messages) in English.
- No `Co-Authored-By: Claude` trailer in commits.
- The daemon binds 127.0.0.1 only. Nothing in this plan changes the direct anthropic path (`switcher.applyDirect`) or the anthropic/copilot/codex translators' auth (copilot/codex keep their own token machinery).

## Review Focus

1. `serve --provider <key>` where the key is absent from providers.json → daemon exits non-zero before listening (CLI health wait then fails, settings.json untouched). Test in Task 2.
2. OpenAI request with no credentials at all → no `Authorization` header sent upstream (not `Bearer ` with empty token). Test in Task 1.
3. Incoming `x-api-key` only (settings had `ANTHROPIC_API_KEY`) → forwarded upstream as `Authorization: Bearer <t>`. Test in Task 1.
4. Old daemon hangs on graceful stop (long SSE) → `Stop` escalates: bounded port wait → SIGTERM by pidfile → SIGKILL; no crash on a missing/garbage pidfile. Test in Task 3.
5. providers.json with no `daemon` block (or `port: 0`) → port 4567 in listen addr, spawn args, and settings.json `ANTHROPIC_BASE_URL` alike. Tests in Tasks 3–4.

---

### Task 1: OpenAI pass-through auth

**Files:**
- Modify: `internal/translators/openai/translator.go`
- Test: `internal/translators/openai/translator_test.go` (extend)

**Interfaces:**
- Consumes: `models.MessagesRequest` (unchanged).
- Produces: `func (t *Translator) do(src *http.Request, body []byte) (*http.Response, error)` — `src` is the incoming Claude Code request. `ServeMessages`/`ServeCountTokens` signatures unchanged; later tasks don't touch this package.

- [ ] **Step 1: Write failing tests** (httptest upstream captures headers; three cases):

```go
// Authorization: Bearer <t> from the incoming request is forwarded verbatim.
// x-api-key falls back to Bearer. No credential → no Authorization header at all.
func TestServeMessages_ForwardsBearerToken(t *testing.T) // upstream asserts "Authorization" == "Bearer sk-upstream"
func TestServeMessages_ForwardsXAPIKeyAsBearer(t *testing.T) // incoming sets only X-Api-Key
func TestServeMessages_NoTokenNoAuthorizationHeader(t *testing.T) // upstream asserts header absent
```

- [ ] **Step 2: Run tests, verify the new assertions fail**

Run: `go test ./internal/translators/openai/... -run Forwards -count=1`
Expected: FAIL (upstream currently receives no Authorization: the profile has no api_key in these tests).

- [ ] **Step 3: Implement pass-through**

`do` gains the `src *http.Request` parameter; token extraction mirrors the old `middleware.extractToken`: `Authorization: Bearer <t>` prefix, else `X-Api-Key`. Set upstream `Authorization: Bearer <t>` only when the extracted token is non-empty; delete the `profile.APIKey` Authorization line. `UserAgent`/`Headers` logic untouched. Update `ServeMessages` call site.

- [ ] **Step 4: Run tests, verify pass**

Run: `go test ./internal/translators/openai/... -count=1`
Expected: PASS (whole package).

- [ ] **Step 5: Commit**

```bash
git add internal/translators/openai/
git commit -m "openai: pass-through auth — the upstream token comes from the incoming request (Authorization: Bearer / x-api-key fallback), profile.api_key no longer authorizes upstream"
```

---

### Task 2: Daemon restructure — serve flags, build-once router, auth-less surface

**Files:**
- Modify: `internal/cli/command_serve.go`, `internal/cli/cli.go` (usage line), `internal/app/app.go`, `internal/app/spawn.go` (no behavior change here, only if compile requires), `internal/services/router/router.go`, `internal/api/handlers/handlers.go`
- Delete: `internal/api/middleware/auth.go` (whole package; no test file exists)
- Test: `internal/services/router/router_test.go`, `internal/api/handlers/handlers_test.go` (rewrite affected cases), `internal/cli/commands_test.go` (serve flag validation)

**Interfaces:**
- Consumes: Task 1 nothing; existing `buildTranslator` (router), `httpserver.New`, `flock.TryLock`.
- Produces (later tasks rely on these exact names):
  - `type ServeOptions struct { Provider string; Port int }` and `func (a *App) Run(ctx context.Context, opts ServeOptions) error` (app).
  - `func Build(ctx context.Context, cfg *config.Config, log *slog.Logger, providerKey string) (*Service, error)` (router; `New` is deleted). `Build` loads providers.json, resolves the profile, calls the existing translator factory and `Ready(ctx)`; on any failure returns the error BEFORE listening.
  - `func (s *Service) Active() (provider string, protocol models.Protocol, ok bool)`; `ServeMessages`/`ServeCountTokens`/`Models()`/`Close` unchanged in signature.
  - handlers `Routerer` drops `Use`, `Ready`, `AuthToken`; keeps `Active`, `ServeMessages`, `ServeCountTokens`, `Models`.
  - `func SpawnDetached(cfg *config.Config, args ...string) error` — args appended after `serve --daemon` (Task 3 uses it; in this task just widen the signature, `app.go` call sites pass nothing).

- [ ] **Step 1: Write failing tests**

```go
// router: Build resolves the profile by key and yields a serving route.
func TestBuild_UnknownProviderFails(t *testing.T) // Build(ctx, cfg, log, "nope") → error mentioning "unknown provider"; service must not listen
func TestBuild_OpenAIProfileReady(t *testing.T) // providers.json fixture with an openai profile → Build ok, Active() == ("work", models.ProtocolOpenAI, true)
func TestBuild_MissingBaseURLFails(t *testing.T) // openai profile without base_url → error "no base_url"

// handlers: auth-less surface.
func TestV1Messages_NoAuthHeaderAccepted(t *testing.T) // request without Authorization reaches the (mock) router
func TestAdminUse_Returns404(t *testing.T)             // route removed
func TestAdminReady_Returns404(t *testing.T)           // route removed
func TestAdminHealth_ReportsLaunchState(t *testing.T) // {"version":…,"ready":true,"port":4567,"active":{"provider":"work","protocol":"openai"}}

// cli: serve flags.
func TestServe_RequiresProvider(t *testing.T) // runServe(["serve"], out) → exit 1, message mentions --provider
func TestServe_FlagParsing(t *testing.T) // --provider work --port 4600 --daemon parsed into ServeOptions (assert via a seam: exported-for-test parse func)
```

- [ ] **Step 2: Run tests, verify they fail**

Run: `go test ./internal/services/router/... ./internal/api/handlers/... ./internal/cli/... -count=1`
Expected: FAIL (no Build, auth middleware rejects, /admin/use exists, flags unhandled).

- [ ] **Step 3: Implement**

- `command_serve.go`: `flag.NewFlagSet("serve", flag.ContinueOnError)` with `--daemon` (bool, accepted and unused beyond spawn semantics), `--provider` (required — error "ccms: serve requires --provider <key>"), `--port` (int); call `app.New(cfg, log).Run(ctx, app.ServeOptions{Provider: *provider, Port: *port})`.
- `app.Run`: resolve listen port via `opts.Port`, falling back to the existing `listenAddr` chain (`CCMS_PORT` > providers `daemon.port` > `config.DefaultDaemonPort`); acquire `daemon.lock` with a retry loop (up to 5s, 100ms interval — the previous daemon may hold it briefly during shutdown) before the quiet-exit; `router.Build(ctx, ...)` — on error return it (process exits 1, reason in daemon.log); keep pidfile/closer/signals as is. `/admin/stop` wiring unchanged.
- `router.go`: replace `New`+`Use`+`Restore`+`AuthToken`/authTok with `Build`; the `route`/atomic machinery collapses to fields set once in `Build` (keep `buildTranslator` unchanged).
- `handlers.go`: drop the middleware group and auth import; delete `handleAdminUse`/`handleAdminReady`; `Deps` gains `Port int`; `/admin/health` reports `{version, ready:true, port, active:{provider, protocol}}` (`ready` is always true once `/health` answers — kept for probe compatibility); update package doc comment (no token guards; localhost-only). `app.Run` passes the resolved listen port into `Deps`.
- `cli.go` usage: `ccms serve [--daemon] --provider <key> [--port N]   daemon (foreground; spawned by ccms use)`.

- [ ] **Step 4: Run tests, verify pass; whole build must be green**

Run: `go build ./... && go test ./internal/services/router/... ./internal/api/handlers/... ./internal/cli/... ./internal/app/... -count=1`
Expected: PASS. (`switcher` still compiles: its `Daemon` interface and `HTTPDaemon.Use` don't reference removed router methods; the `Daemon.AuthToken` field still exists until Task 4.)

- [ ] **Step 5: Commit**

```bash
git add -A internal/cli/ internal/app/ internal/services/router/ internal/api/
git commit -m "daemon: restart-with-launch-params core — serve takes --provider/--port, router.Build resolves the profile and Runs Ready() before listening, auth middleware and /admin/use + /admin/ready removed (/health answering == ready)"
```

---

### Task 3: Switcher restart flow

**Files:**
- Modify: `internal/services/switcher/switcher.go`, `internal/services/switcher/daemon.go`, `internal/app/spawn.go`, `internal/config/providers.go` (port helper)
- Test: `internal/services/switcher/switcher_test.go`, `internal/services/switcher/daemon_test.go` (rewrite), `internal/config/providers_test.go` (extend)

**Interfaces:**
- Consumes: `SpawnDetached(cfg, args...)`, `app.HealthCheck`/`WaitForHealth` (existing), `config.DefaultDaemonPort`.
- Produces:
  - `func ResolveDaemonPort(c *Config, flagPort int) int` (config) — `flagPort > 0` → flagPort; `c.DaemonPort > 0` → c.DaemonPort; else load providers.json → `daemon.port > 0` → that; else `DefaultDaemonPort` (4567). Providers load failure falls through to the default.
  - switcher `Daemon` interface becomes: `Restart(ctx context.Context, providerKey string, port int) error` and `Stop(ctx context.Context) error` (`Ensure`/`Use` deleted).
  - `HTTPDaemon.Restart`: stop-if-running (below) → `SpawnDetached(cfg, "--provider", providerKey, "--port", strconv.Itoa(port))` → `WaitForHealth`; error if health never comes (message with daemon.log path, as today).
  - `HTTPDaemon.Stop`: health-check no-op as today; `POST /admin/stop`; wait port close ≤10s (200ms polls); then read `cfg.CCMSDir()/daemon.pid` → `SIGTERM` → wait ≤2s → `SIGKILL`; a missing/garbage pidfile is not an error (best-effort, log-free).

- [ ] **Step 1: Write failing tests**

```go
// config: port precedence.
func TestResolveDaemonPort_FlagWins(t *testing.T)          // flag 4700 over env and providers
func TestResolveDaemonPort_EnvOverProviders(t *testing.T)  // CCMS_PORT via cfg.DaemonPort
func TestResolveDaemonPort_ProvidersOverDefault(t *testing.T)
func TestResolveDaemonPort_DefaultWhenNoDaemonBlock(t *testing.T) // == 4567

// switcher: translated apply now restarts with args and writes provider credentials.
func TestApply_OpenAI_RestartsDaemonWithProviderAndPort(t *testing.T) // mock Daemon asserts Restart("work", 4567); settings.json: ANTHROPIC_BASE_URL http://127.0.0.1:4567, ANTHROPIC_AUTH_TOKEN = profile auth_token
func TestApply_OpenAI_AuthTokenWinsOverAPIKey(t *testing.T)
func TestApply_OpenAI_APIKeyWhenNoAuthToken(t *testing.T)  // ANTHROPIC_API_KEY = profile api_key, no ANTHROPIC_AUTH_TOKEN written
func TestApply_NoCredentials_PlaceholderToken(t *testing.T) // ANTHROPIC_AUTH_TOKEN == "ccms"
func TestApply_RestartFailureLeavesSettingsUntouched(t *testing.T) // Restart error → settings.json byte-identical

// HTTPDaemon against a fake daemon + injectable spawn (add a package-level var spawn = app.SpawnDetached seam in daemon.go tests).
func TestRestart_StopsThenSpawnsThenWaitsHealth(t *testing.T) // fake old daemon answering /health+/admin/stop records ordering; spawn seam starts a new fake answering /health
func TestStop_EscalatesToSignalWhenPortStaysOpen(t *testing.T) // fake daemon ignores /admin/stop; daemon.pid holds a real `sleep` child pid; Stop returns nil; the child is dead after
func TestStop_MissingPidfileNoError(t *testing.T)
```

- [ ] **Step 2: Run tests, verify they fail**

Run: `go test ./internal/config/... ./internal/services/switcher/... -count=1`
Expected: FAIL (no ResolveDaemonPort, Daemon interface mismatch, old Ensure/Use flow).

- [ ] **Step 3: Implement**

- `config.ResolveDaemonPort` per the interface above.
- `daemon.go`: rewrite `HTTPDaemon` — `baseURL(port int)` helper; `Restart`/`Stop` per interface; delete `Ensure`/`Use`; signals via `syscall.Kill(pid, syscall.SIGTERM/SIGKILL)` (unix-only build is fine: darwin/linux targets).
- `switcher.go` `applyLocked` translated branch: `port := config.ResolveDaemonPort(s.cfg, 0)` → `s.daemon.Restart(ctx, profileKey, port)` (error message keeps the "settings.json untouched, you stay on the previous profile" contract) → `writeSettings(providers, daemonEnv(profile, model, port), model)` → `writeState`. `daemonEnv(providers, profile, model, port)`: profile `Env` extras; `ANTHROPIC_BASE_URL = http://127.0.0.1:<port>`; credential per direct-path precedence (`auth_token` → `ANTHROPIC_AUTH_TOKEN`, else `api_key` → `ANTHROPIC_API_KEY`, else `ANTHROPIC_AUTH_TOKEN = "ccms"` placeholder — Claude Code expects a credential; the daemon ignores it for non-pass-through protocols); `pinnedKeys` unchanged. Success line keeps the daemon URL.

- [ ] **Step 4: Run tests, verify pass; build green**

Run: `go build ./... && go test ./internal/services/switcher/... ./internal/config/... -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/services/switcher/ internal/config/providers.go internal/config/providers_test.go internal/app/spawn.go
git commit -m "switcher: restart-on-switch — Daemon collapses to Restart/Stop (graceful stop, bounded port wait, SIGTERM/SIGKILL by pidfile, spawn with --provider/--port, health wait), daemonEnv writes the provider credential (auth_token > api_key > placeholder) and the resolved daemon port"
```

---

### Task 4: providers.json schema — drop `version` and `daemon.auth_token`

**Files:**
- Modify: `internal/models/providers.go`, `internal/config/providers.go`, `internal/config/init.go`, `internal/cli/command_stop.go`
- Test: `internal/config/providers_test.go`, `internal/config/init_test.go`, `internal/config/config_test.go`, fixtures across suites that write providers.json (`grep -rl '"version"' internal/`)

**Interfaces:**
- Consumes: `config.ResolveDaemonPort` (Task 3).
- Produces: `models.Providers{Daemon Daemon; Profiles ProfileMap}` with `Daemon{Port int `json:"port,omitempty"`}` — the final schema; nothing downstream reads `Version`/`Daemon.AuthToken` anymore.

- [ ] **Step 1: Write failing tests**

```go
func TestLoadProviders_NoVersionRequired(t *testing.T) // file without "version" loads; file with "version": 3 also loads (unknown field ignored)
func TestLoadProviders_MissingDaemonBlock(t *testing.T) // no "daemon" key → Daemon.Port == 0, profiles intact
func TestInit_NoVersionNoTokenWritten(t *testing.T)     // init output JSON contains neither "version" nor "auth_token"; no daemon block
func TestSaveLoad_RoundTripKeepsPort(t *testing.T)      // daemon.port 4600 survives save/load
```

- [ ] **Step 2: Run tests, verify they fail**

Run: `go test ./internal/config/... -count=1`
Expected: FAIL (loader rejects missing version; init writes version/token).

- [ ] **Step 3: Implement**

- `models/providers.go`: delete `Version` field; `Daemon` keeps only `Port` with `json:"port,omitempty"`; update doc comments (auth model: pass-through/local-only).
- `config/providers.go`: delete the version check and `ErrUnsupportedVersion`; `LoadProviders` = read + return.
- `config/init.go`: stop setting `providers.Version`, `providers.Daemon.Port`, generating/storing the token; delete `generateToken` and the now-unused crypto/rand+hex imports.
- `command_stop.go`: port via `config.ResolveDaemonPort(cfg, 0)`; drop the token header and the second `LoadProviders`.
- Sweep test fixtures: remove `"version"`/`auth_token` daemon fields from every fixture that writes providers.json (they still load either way, but fixtures should model the new schema); fix compile errors from the removed field.

- [ ] **Step 4: Run tests, verify pass — whole repo**

Run: `go build ./... && go test ./... -count=1`
Expected: PASS everywhere.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "providers.json: drop version and daemon.auth_token — unversioned schema, optional daemon.port (default 4567 via ResolveDaemonPort), init writes neither token nor daemon block, ccms stop needs no token"
```

---

### Task 5: Docs and full verification

**Files:**
- Modify: `CLAUDE.md`, `README.md`
- No new code.

**Interfaces:** none.

- [ ] **Step 1: Update CLAUDE.md**

Architecture section: `app/` — `spawn.go: detached daemon launch (Setsid, pidfile, --provider/--port args)`; `services/router/` — `build-once at startup (--provider), no in-daemon switching`; remove the middleware line from `api/middleware` (package deleted); daemon paragraph in the module header (`ANTHROPIC_BASE_URL` owner: restarts with launch params, pass-through openai auth). Key invariants: replace the readiness bullet's `/admin/use` wording with restart semantics ("switching a translated profile restarts the daemon with `--provider`/`--port` and waits for `/health` (= `Ready()` passed) before settings.json is touched"); add the port precedence line.

- [ ] **Step 2: Update README**

Serve usage line, providers.json example without `version`/`auth_token` (daemon block optional), keep direct-mode docs unchanged.

- [ ] **Step 3: Full verification**

Run: `make lint && make test && make deadcode`
Expected: lint 0 issues; tests pass with coverage ≥ 80%; no new dead code from the refactor (deleted `Ensure`/`Use`/`Restore`/auth surface leave nothing dangling).

- [ ] **Step 4: Commit**

```bash
git add CLAUDE.md README.md
git commit -m "docs: daemon restart launch params — serve --provider/--port, pass-through openai auth, unversioned providers.json with optional daemon.port"
```
