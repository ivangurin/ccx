# Suite Provider Migration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Finish the test-migration wave: every test that touches the daemon graph goes through the suite provider (or the production composition root), every remaining in-package test file moves to the external `<pkg>_test` package, the App builds its own service provider, and the e2e test moves from `test/e2e` into `internal/app`.

**Architecture:** The suite provider (`internal/pkg/suite/provider`) is the test composition root: sandbox config (`NewProvider(t)` pins `CCX_HOME` + `CCX_SETTINGS`), mockery mocks at the three client seams, real services via `GetSwitcherService`/`GetRouteService(key)`/`GetTesterService`. Unexported symbols that block out-of-package moves get seams in `export_test.go` files (package-scoped, compiled only into test builds — zero production surface). The App stops receiving a ready `*provider.Provider` and constructs one internally from `(cfg, log)` — the provider stays the shared composition root for the non-serve CLI commands (`provider.NewEnv()`).

**Tech Stack:** Go 1.27, testify `require`, mockery (`.mockery.yaml`), cobra, golangci-lint.

**Spec:** No separate spec doc — the requirements are the owner directives in this session plus `CLAUDE.md` (owner rule: tests live in the external `<pkg>_test` package; suite provider as the test composition root; e2e relocation to `internal/app` approved; provider construction inside `app.New` approved).

## Scope decision (binding for all tasks)

The suite provider composes the **daemon graph only**. Deliberately OUT of scope — these stay hand-built leaf tests (documented in CLAUDE.md by Task 13):

- `internal/models/*`, `internal/pkg/{closer,flock,httpserver,jsonfile,jsonpatch,logger,readguard,sse,strutil,usagelog,reqdump}` — pure leaf packages; the suite adds nothing. (`readguard` and the leaf files still get the package-clause flip per Task 9/12 — the *owner rule* applies tree-wide even where the suite does not.)
- `internal/clients/{codex,copilot}` — client objects are deliberately outside the provider graph (suite `clients.go` doc); their tests build clients by hand over httptest upstreams. They get the external-package fix only (Tasks 8–10).
- `internal/config/config_test.go`, `internal/services/tester/render_test.go`, `internal/app/selfcheck_test.go`, `internal/translators/{anthropic,openai,relay,sessionid,prefill,billing,chatstream}/…` external files — already compliant; no change.

IN scope: the four in-package cli files, `clients/codex/login_test.go`, `clients/copilot/{quota,wiring}_test.go`, the five in-package `translators/copilot` pure-func files, `translators/billing`, `translators/chatstream/onfinish_test.go`, `pkg/readguard`, the three in-package `pkg/tui` files, the app provider-construction change, and the e2e relocation.

## Global Constraints

- Tests live in the external package `<pkg>_test`, never in-package (owner rule).
- Never pin port 4567 in fixtures — ephemeral `freePort` only (daemon-killer invariant).
- The suite sandbox uses `t.Setenv` — migrated tests must NOT call `t.Parallel()`.
- Coverage must stay ≥ 80% (`make test`), lint clean (`make lint` per task, `make lint-full` at the end).
- All project artifacts (code, comments, commit messages, docs) in English; no `Co-Authored-By: Claude` trailer; commit subjects follow the repo style (`ccx: …`, `docs: …`).
- Moved test files keep their assertions byte-for-byte — only the seam plumbing (package clause, identifier renames per the task's mapping table) may change.
- Every `export_test.go` seam must be restored by the test that swapped it (`t.Cleanup(restore)`).

## Review Focus

1. **Leaked global seams** — the cli auth/quota swaps mutate package vars; a test that swaps without restoring poisons every later test in the binary. Expectation: every seam swap is paired with `t.Cleanup(restore)` in the same test. Pinned by: seam code shape in Tasks 6/7 + the full `go test ./internal/cli/ -count=1` runs in every cli task.
2. **Port discipline during moves** — `command_status_test.go` and the e2e move carry live-HTTP probes; a hardcoded port here can kill the developer's live daemon. Expectation: only `freePort(t)`-derived ports; codex login keeps its `skipIfLoginPortsBusy` guard for the real 1455/1457. Pinned by: mapping tables in Tasks 3/5/8.
3. **Unified-log invariant under the new `app.New`** — in-process `app.Run` tests must assert on the writer passed to `Run`/`New`, never on `logs/daemon-*.log` files (no file exists for an in-process daemon). Pinned by: Task 2 keeps `usageLog`/logger injection parameters intact.
4. **Silent assertion drift in moved files** — a move that rewrites expected strings (hint texts, JSON bodies) is a behavior change masquerading as a migration. Expectation: diffs of moved files show only mechanical renames. Pinned by: the Global Constraints rule + per-task `git diff` review step.
5. **Production leakage of test seams** — an `export_test.go` helper accidentally referenced from production code breaks `go build`. Expectation: `go build ./...` green in every task; `make deadcode` still silent at the end. Pinned by: per-task build steps + Task 13.

---

### Task 1: Commit the in-flight getter rename

The working tree carries a complete, verified rename (`GetSwitcher`→`GetSwitcherService`, `GetRoute`→`GetRouteService`, `GetTester`→`GetTesterService` in both providers and all call sites; build+vet+full tests green). It also leaves one stale doc comment. Commit it as the base before new work.

**Files:**
- Modify: `internal/pkg/suite/provider/provider.go:6` (stale comment)
- Modify: `CLAUDE.md` (getter names)

- [ ] **Step 1: Fix the stale doc comment** in `internal/pkg/suite/provider/provider.go` line 6: `sp.GetDaemonMock().EXPECT()... → sp.GetSwitcher()` → `sp.GetDaemonMock().EXPECT()... → sp.GetSwitcherService()`.

- [ ] **Step 2: Update getter names in CLAUDE.md.** Run `grep -n 'GetSwitcher\|GetRoute\|GetTester' CLAUDE.md` and rename each hit to the Service-suffixed form (`GetSwitcherService`, `GetRouteService`, `GetTesterService`) — leave `GetDaemon`, `GetDaemonMock`, `GetCodexAuth*`, `GetCopilotToken*`, `GetConfig`, `GetLogger` untouched.

- [ ] **Step 3: Verify.** Run: `go build ./... && go vet ./... && go test ./internal/pkg/suite/provider/ ./internal/provider/ -count=1`
  Expected: exit 0.

- [ ] **Step 4: Commit** only the 14 already-modified files + `CLAUDE.md` + `provider.go` + this plan file (check `git status` first — nothing else may be swept in):
  ```bash
  git add internal/ CLAUDE.md docs/superpowers/plans/2026-11-14-suite-provider-migration.md
  git commit -m "ccx: service getters renamed to the XxxService form in both providers — GetSwitcherService/GetRouteService/GetTesterService (app and cli call sites with them), the plain GetX names stay on the client seams (GetDaemon/GetCodexAuth/GetCopilotToken); CLAUDE.md layer map and suite recipe carry the new names"
  ```

### Task 2: App builds its service provider internally

`app.New(p, usageLog)` → `app.New(cfg, log, usageLog)`: the App constructs `provider.New(cfg, log)` itself. The provider remains the composition root for the non-serve CLI commands (`provider.NewEnv()` in use/restore/test/stop stays).

**Files:**
- Modify: `internal/app/app.go` (package doc, `App`, `New`)
- Modify: `internal/cli/command_serve.go:100` (drop `provider.New`, drop the `provider` import)
- Test: `internal/app/app_test.go:28`, `internal/app/unified_log_test.go:68`, `internal/app/app_signals_test.go:72,101`

**Interfaces:**
- Produces: `func New(cfg *config.Config, log *slog.Logger, usageLog *slog.Logger) *App` — a nil `usageLog` still falls back to the provider's logger (current semantics preserved).
- Consumes: `provider.New(cfg, log)` unchanged.

- [ ] **Step 1: Update the call sites first (red).** In the three app test files replace, per the mapping:
  - `app.New(provider.New(sp.Config, sp.GetLogger()), nil)` → `app.New(sp.Config, sp.GetLogger(), nil)`
  - `app.New(provider.New(sp.Config, log), usageLog)` → `app.New(sp.Config, log, usageLog)`
  - `app.New(provider.New(sp.Config, recordingLogger(&buf)), nil)` → `app.New(sp.Config, recordingLogger(&buf), nil)`

  In `command_serve.go`: `app.New(provider.New(cfg, logs.Diagnostics), logs.Usage)` → `app.New(cfg, logs.Diagnostics, logs.Usage)` and delete the now-unused `provider` import. Run `go build ./internal/app/ ./internal/cli/` — expected: FAIL (`too many arguments`/`mismatched types` in app.go) — this is the red.
  Do NOT touch `test/e2e/e2e_test.go` beyond the mechanical call update `app.New(provider.New(cfg, log), nil)` → `app.New(cfg, log, nil)` (full relocation is Task 3); drop its `provider` import too.

- [ ] **Step 2: Change `app.New` (green).** In `app.go`:
  - Signature: `func New(cfg *config.Config, log *slog.Logger, usageLog *slog.Logger) *App`; body: `p := provider.New(cfg, log)`; if `usageLog == nil` → `usageLog = p.GetLogger()`; return `&App{sp: p, usageLog: usageLog}`.
  - Update the `App` struct doc and the package doc (app.go:1-10): the App owns its provider construction — `cfg`/`log` are constructor arguments; the CLI switching commands compose their own provider via `provider.NewEnv()`; `GetConfig`/`GetLogger`/`GetRouteService` still come from the provider inside `Run`.
  - Add the `config` import.

- [ ] **Step 3: Verify.** Run: `go build ./... && go vet ./... && go test ./internal/app/ ./internal/cli/ ./test/e2e/ -count=1`
  Expected: all PASS.

- [ ] **Step 4: Lint and commit.**
  ```bash
  make lint
  git add -A
  git commit -m "ccx: app.New builds its service provider internally — New(cfg, log, usageLog) constructs provider.New(cfg, log) itself, callers stop passing a ready provider; the provider stays the composition root for the non-serve cli commands (provider.NewEnv in use/restore/test/stop); every current call site was app-exclusive (runServe, e2e, app tests)"
  ```

### Task 3: e2e moves to internal/app over the suite sandbox

`test/e2e/e2e_test.go` → `internal/app/e2e_test.go` (package `app_test`), rebuilt over the suite sandbox — which also pins `CCX_SETTINGS`, unpinned today.

**Files:**
- Create: `internal/app/e2e_test.go`
- Delete: `test/e2e/e2e_test.go` (and the `test/e2e/` directory)
- Test: the new file IS the test.

**Interfaces:**
- Consumes: `suite_provider.NewProvider(t)` (sandbox + `sp.Config` + `sp.GetLogger()`), `app.New` from Task 2, `freePort(t)` already defined in `internal/app/app_signals_test.go` (same package — reuse, do not duplicate).
- Produces: `TestDaemon_EndToEnd_AnthropicPassthrough` (same name and subtests) in package `app_test`.

- [ ] **Step 1: Write the migrated file.** Package `app_test`; composition:
  ```go
  sp := suite_provider.NewProvider(t)
  port := freePort(t)
  upstream := startFakeUpstream(t)
  providers := &models.Providers{}
  providers.Providers.Set("zai", &models.Profile{Name: "z.ai (fake)", Protocol: models.ProtocolAnthropic, BaseURL: upstream.URL, AuthToken: "up-secret", Models: []models.Model{{ID: "glm-5.3[1m]", Default: true}}})
  require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), providers))
  ctx, cancel := context.WithCancel(context.Background())
  t.Cleanup(cancel)
  go func() { _ = app.New(sp.Config, sp.GetLogger(), nil).Run(ctx, app.ServeOptions{Provider: "zai", Port: port}) }()
  ```
  Deliberate simplifications vs the old file: drop the `models.Daemon{Port}` block and the `CCX_PORT` env pin — `opts.Port` is explicit (`resolveListenPort` flag-beats-env); drop the local `freePort` (reuse `app_signals_test.go`'s); keep `startFakeUpstream`, `alive`, and all three subtests (messages / admin health / models) byte-for-byte.

- [ ] **Step 2: Delete `test/e2e/`.**

- [ ] **Step 3: Verify.** Run: `go build ./... && go test ./internal/app/ -run TestDaemon_EndToEnd -count=1 -v`
  Expected: the test and its three subtests PASS; `test/e2e` no longer in `go list ./...`.

- [ ] **Step 4: Lint and commit.**
  ```bash
  make lint
  git add -A
  git commit -m "ccx: the e2e daemon test moves from test/e2e into internal/app (package app_test) over the suite sandbox — CCX_SETTINGS is pinned now, providers.json is written via sp.Config, the port comes from the package's freePort; the production composition is still exercised (app.New builds the real provider since Task 2); the redundant Daemon.port block and CCX_PORT pin are dropped (opts.Port is explicit)"
  ```

### Task 4: cli menu test → external package

`command_menu_test.go` is in-package only to reach unexported `menuStages`. Export it test-only via `export_test.go` and move the file.

**Files:**
- Create: `internal/cli/export_test.go`
- Modify: `internal/cli/command_menu_test.go` (package clause + identifiers)

**Interfaces:**
- Produces (in `export_test.go`, package `cli`, test-build only):
  ```go
  // MenuStagesForTest exposes the menu stage-chain builder (tests only).
  func MenuStagesForTest(providers *models.Providers, defProfile int) tui.NextStage {
      return menuStages(providers, defProfile)
  }
  ```

- [ ] **Step 1: Create `internal/cli/export_test.go`** with the seam above (imports: `models`, `tui`).

- [ ] **Step 2: Move the test file.** `command_menu_test.go`: package `cli` → `cli_test`; every `menuStages(` → `MenuStagesForTest(`; keep `menuTestProviders` and all assertions unchanged.

- [ ] **Step 3: Verify.** Run: `go build ./... && go test ./internal/cli/ -run 'Menu' -count=1`
  Expected: PASS.

- [ ] **Step 4: Lint and commit.**
  ```bash
  make lint
  git add internal/cli/export_test.go internal/cli/command_menu_test.go
  git commit -m "ccx: the cli menu stage-chain test moves to package cli_test over the MenuStagesForTest export_test.go seam — the owner rule (external test packages) clears the first of the four in-package cli files"
  ```

### Task 5: cli status test → external package over cli.Execute

`command_status_test.go` drives unexported `runStatus` directly. Its behavior is fully reachable through `cli.Execute` (the stable seam) — rewrite the driver, keep everything else.

**Files:**
- Modify: `internal/cli/command_status_test.go` (package clause + driver calls)

**Interfaces:**
- Consumes: `cli.Execute(args []string, stdout io.Writer) int` — mirror the call shape already used in `cli_test.go`.

- [ ] **Step 1: Move the test file.** Package `cli` → `cli_test`; every direct `runStatus(argv, stdout)` call → `cli.Execute([]string{"status", …argv…}, stdout)` matching the argv shapes used in `cli_test.go`; keep `writeStatusFixture`/`openAIProfile`/`freeStatusPort`/`portOf` and ALL assertions (exit codes, stdout texts) unchanged.

- [ ] **Step 2: Verify.** Run: `go test ./internal/cli/ -run 'Status' -count=1`
  Expected: PASS (same texts, same exit codes — only the driver changed).

- [ ] **Step 3: Lint and commit.**
  ```bash
  make lint
  git add internal/cli/command_status_test.go
  git commit -m "ccx: the ccx status daemon-liveness tests move to package cli_test driving cli.Execute — runStatus behavior is pinned through the stable Execute seam, fixtures and assertions unchanged"
  ```

### Task 6: cli status quota test → external + quota-fetcher seam

`command_status_quota_test.go` swaps the unexported package var `copilotQuota`. Export a swap seam, move the file.

**Files:**
- Modify: `internal/cli/export_test.go` (append the seam)
- Modify: `internal/cli/command_status_quota_test.go` (package clause + swap calls + driver)

**Interfaces:**
- Produces (appended to `export_test.go`):
  ```go
  // SetQuotaFetcherForTest swaps the copilot quota fetcher seam (tests only);
  // returns the restore func — tests must wire it via t.Cleanup.
  func SetQuotaFetcherForTest(f func(storePath string) (*copilot_client.QuotaInfo, error)) (restore func()) {
      orig := copilotQuota
      copilotQuota = f
      return func() { copilotQuota = orig }
  }
  ```
  (import alias `copilot_client` as in `command_status.go`)

- [ ] **Step 1: Append the seam to `export_test.go`.**

- [ ] **Step 2: Move the test file.** Package `cli` → `cli_test`; each `orig := copilotQuota; copilotQuota = …; t.Cleanup(…)` block → `restore := SetQuotaFetcherForTest(…); t.Cleanup(restore)` (same fakes, same assertions); direct `runStatus` calls → `cli.Execute([]string{"status"}, stdout)` as in Task 5; keep `setStatusState` and all assertions.

- [ ] **Step 3: Verify.** Run: `go test ./internal/cli/ -run 'Quota' -count=1 && go build ./...`
  Expected: PASS; build green (no production reference to the seam).

- [ ] **Step 4: Lint and commit.**
  ```bash
  make lint
  git add internal/cli/export_test.go internal/cli/command_status_quota_test.go
  git commit -m "ccx: the copilot quota panel tests move to package cli_test — the copilotQuota var swap becomes the SetQuotaFetcherForTest export_test.go seam with mandatory restore, the driver goes through cli.Execute"
  ```

### Task 7: cli auth test → external + auth seams

`command_auth_test.go` swaps `newCopilotAuthClient` and `codexLogin` and calls `runAuth` directly. Two seams + the Execute driver. The tricky part: `copilotAuthClient` is unexported, so the factory-replacement seam takes the FAKE (a concrete value — implementable in `cli_test` without naming the interface) plus a store-path recorder.

**Files:**
- Modify: `internal/cli/export_test.go` (append two seams)
- Modify: `internal/cli/command_auth_test.go` (package clause + swap blocks + driver)

**Interfaces:**
- Produces (appended to `export_test.go`):
  ```go
  // SetCodexLoginForTest swaps the codex login seam (tests only); returns the
  // restore func — tests must wire it via t.Cleanup.
  func SetCodexLoginForTest(f func(ctx context.Context, authPath string, openBrowser func(string) error) error) (restore func()) {
      orig := codexLogin
      codexLogin = f
      return func() { codexLogin = orig }
  }

  // SetCopilotAuthClientForTest swaps the auth client factory (tests only): the
  // fake is returned for every store; storeArg records the store path the
  // production code passed (assert it after the run). Returns the restore func —
  // tests must wire it via t.Cleanup.
  func SetCopilotAuthClientForTest(fake copilotAuthClient) (restore func(), storeArg *string) {
      got := new(string)
      orig := newCopilotAuthClient
      newCopilotAuthClient = func(store string) copilotAuthClient {
          *got = store
          return fake
      }
      restore = func() { newCopilotAuthClient = orig }
      return restore, got
  }
  ```

- [ ] **Step 1: Append both seams to `export_test.go`** (import `context`).

- [ ] **Step 2: Move the test file.** Package `cli` → `cli_test`. `fakeAuthClient` stays (its methods reference only exported types — `*copilot_client.DeviceFlowResult`, `string`, `error`). Per test:
  - the `newCopilotAuthClient` swap block → `restore, storeArg := SetCopilotAuthClientForTest(&fakeAuthClient{store: "<same>"})` + `t.Cleanup(restore)`; the old closure's `require.Equal(expectedStore, store)` moves AFTER the run as `require.Equal(expectedStore, *storeArg)`;
  - the `codexLogin` swap block → `restore := SetCodexLoginForTest(…same closure…)` + `t.Cleanup(restore)`;
  - direct `runAuth` calls → `cli.Execute([]string{"auth", …}, stdout)`;
  - `writeAuthFixture` and all assertions unchanged.

- [ ] **Step 3: Verify.** Run: `go test ./internal/cli/ -run 'Auth' -count=1 && go build ./...`
  Expected: PASS; build green.

- [ ] **Step 4: Lint and commit.**
  ```bash
  make lint
  git add internal/cli/export_test.go internal/cli/command_auth_test.go
  git commit -m "ccx: the ccx auth tests move to package cli_test — SetCopilotAuthClientForTest (fake + store-path recorder; the unexported interface never needs naming at the call site) and SetCodexLoginForTest replace the raw var swaps, runAuth goes through cli.Execute"
  ```

### Task 8: codex login test → external package

`login_test.go` is in-package for unexported `loginWithBase` and the port/path consts. The function gets a test-only re-export; the consts become literals (they are protocol constants, stable by the allow-list invariant in CLAUDE.md).

**Files:**
- Create: `internal/clients/codex/export_test.go`
- Modify: `internal/clients/codex/login_test.go` (package clause + identifiers)

**Interfaces:**
- Produces (in `export_test.go`, package `codex`):
  ```go
  // LoginWithBaseForTest re-exports the parameterized OAuth login flow (tests only).
  var LoginWithBaseForTest = loginWithBase
  ```

- [ ] **Step 1: Create `internal/clients/codex/export_test.go`** with the seam above.

- [ ] **Step 2: Move the test file.** Package `codex` → `codex_test`. Mapping table:
  - `loginWithBase(` → `LoginWithBaseForTest(`
  - `loginPort` → `1455`, `loginFallbackPort` → `1457`, `loginCallbackPath` → `"/auth/callback"`, `loginCancelPath` → `"/cancel"` (keep a short comment: protocol constants from codex-rs `login/server.rs`, pinned by the allow-list invariant)
  - keep `skipIfLoginPortsBusy`, all fixtures and assertions unchanged; the port literals must NOT be reformatted into anything else.

- [ ] **Step 3: Verify.** Run: `go test ./internal/clients/codex/ -count=1 && go build ./...`
  Expected: PASS (skipped subtests only if 1455/1457 are genuinely busy).

- [ ] **Step 4: Lint and commit.**
  ```bash
  make lint
  git add internal/clients/codex/export_test.go internal/clients/codex/login_test.go
  git commit -m "ccx: the codex OAuth login tests move to package codex_test — loginWithBase re-exported via export_test.go, the port/path consts become literals (protocol constants, allow-list invariant)"
  ```

### Task 9: Trivial package-clause flips (four files, exported-only symbols)

Four in-package files touch ONLY exported symbols — the in-package clause is gratuitous. Flip each to the external package; zero other changes.

**Files:**
- Modify: `internal/translators/billing/billing_test.go` (`billing` → `billing_test`)
- Modify: `internal/translators/chatstream/onfinish_test.go` (`chatstream` → `chatstream_test`)
- Modify: `internal/pkg/readguard/readguard_test.go` (`readguard` → `readguard_test`)
- Modify: `internal/clients/copilot/quota_test.go` (`copilot` → `copilot_test`)

- [ ] **Step 1: Flip the four package clauses.** If a file imports something only an in-package file could (none do, per inventory), surface it instead of forcing the flip.

- [ ] **Step 2: Verify.** Run: `go test ./internal/translators/billing/ ./internal/translators/chatstream/ ./internal/pkg/readguard/ ./internal/clients/copilot/ -count=1 && go build ./...`
  Expected: PASS.

- [ ] **Step 3: Lint and commit.**
  ```bash
  make lint
  git add internal/translators/billing/billing_test.go internal/translators/chatstream/onfinish_test.go internal/pkg/readguard/readguard_test.go internal/clients/copilot/quota_test.go
  git commit -m "ccx: four in-package test files flip to the external package (billing, chatstream onFinish, readguard, copilot quota) — they touch only exported symbols, the owner rule applies"
  ```

### Task 10: copilot wiring test → external + endpoints seam

`wiring_test.go` reads unexported fields `c.tokenURL`/`c.exURL`. A test-only getter exposes them.

**Files:**
- Create: `internal/clients/copilot/export_test.go`
- Modify: `internal/clients/copilot/wiring_test.go`

**Interfaces:**
- Produces (in `export_test.go`, package `copilot`):
  ```go
  // EndpointsForTest exposes the wired endpoint URLs (tests only).
  func (c *Client) EndpointsForTest() (tokenURL, exURL string) {
      return c.tokenURL, c.exURL
  }
  ```

- [ ] **Step 1: Create `internal/clients/copilot/export_test.go`** with the seam above.

- [ ] **Step 2: Move the test file.** Package `copilot` → `copilot_test`; `c.tokenURL`/`c.exURL` reads → `tokenURL, exURL := c.EndpointsForTest()`; assertions unchanged.

- [ ] **Step 3: Verify.** Run: `go test ./internal/clients/copilot/ -run 'Wiring' -count=1 && go build ./...`
  Expected: PASS.

- [ ] **Step 4: Lint and commit.**
  ```bash
  make lint
  git add internal/clients/copilot/export_test.go internal/clients/copilot/wiring_test.go
  git commit -m "ccx: the copilot client wiring tests move to package copilot_test over the EndpointsForTest export_test.go getter"
  ```

### Task 11: copilot pure-function tests → external package

Five in-package files test unexported pure functions (`isCompactRequest`, `quotaIDs`, `mergeToolResults`, `classifyQuota`, `detectSubagent`). One `export_test.go` re-exports all five; unexported consts used as test inputs become literals; `quotaClass` fields are already exported.

**Files:**
- Create: `internal/translators/copilot/export_test.go`
- Modify: `internal/translators/copilot/{compact,ids,merge,quota,subagent}_test.go`

**Interfaces:**
- Produces (in `export_test.go`, package `copilot`):
  ```go
  // Test-only re-exports of the request-classification helpers.
  var (
      IsCompactRequestForTest = isCompactRequest
      QuotaIDsForTest         = quotaIDs
      MergeToolResultsForTest = mergeToolResults
      ClassifyQuotaForTest    = classifyQuota
      DetectSubagentForTest   = detectSubagent
  )
  ```

- [ ] **Step 1: Create `internal/translators/copilot/export_test.go`** with the block above.

- [ ] **Step 2: Move the five files** to package `copilot_test`. Mapping:
  - the five func names → their `…ForTest` re-exports;
  - `initiatorAgent`/`initiatorUser` → literals `"agent"`/`"user"`;
  - `subagentMarker`/`subagentUserMarker` (if referenced) → `"__SUBAGENT_MARKER__"`/`"_agent_"`;
  - grep the five files for any remaining unexported identifier: re-export function-valued ones as additional `…ForTest` vars in the same `export_test.go` block, replace const-valued ones with literals. If a test mutates `randRead`/`fallbackCounter` (ids determinism), re-export a matching swap/reset seam in the same file with a restore func.

- [ ] **Step 3: Verify.** Run: `go test ./internal/translators/copilot/ -count=1 && go build ./...`
  Expected: PASS.

- [ ] **Step 4: Lint and commit.**
  ```bash
  make lint
  git add internal/translators/copilot/
  git commit -m "ccx: the five copilot request-classification test files move to package copilot_test — one export_test.go block re-exports isCompactRequest/quotaIDs/mergeToolResults/classifyQuota/detectSubagent, const expectations become literals"
  ```

### Task 12: tui tests → external package

Three in-package tui files (`escjoin`, `frame`, `interactive`) test unexported helpers. The `session` struct needs a constructor wrapper (its fields are unexported — an external package cannot write the literal).

**Files:**
- Create: `internal/pkg/tui/export_test.go`
- Modify: `internal/pkg/tui/{escjoin,frame,interactive}_test.go`

**Interfaces:**
- Produces (in `export_test.go`, package `tui`; match the actual field type of `session.out`):
  ```go
  var (
      EscJoinForTest       = escJoin
      BuildFrameForTest    = buildFrame
      RepaintPrefixForTest = repaintPrefix
  )

  // NewSessionForTest builds a session writing frames to out (tests only).
  func NewSessionForTest(out io.Writer) *session { return &session{out: out} }

  // HandleKeyForTest drives the session key handler (tests only).
  func HandleKeyForTest(s *session, key []byte, next NextStage) (bool, []int, error) {
      return s.handleKey(key, next)
  }
  ```

- [ ] **Step 1: Create `internal/pkg/tui/export_test.go`** with the seams above.

- [ ] **Step 2: Move the three files** to package `tui_test` (joining the existing `tui_test.go`). Mapping:
  - `escJoin(` → `EscJoinForTest(`, `buildFrame(` → `BuildFrameForTest(`, `repaintPrefix(` → `RepaintPrefixForTest(`;
  - `&session{out: out}` / the local `newSession` helper → `NewSessionForTest(out)`;
  - `s.handleKey(` → `HandleKeyForTest(s, )`;
  - grep the three files for remaining unexported identifiers (e.g. `escContWindow`): apply the Task 11 rule (re-export vars / const literals).

- [ ] **Step 3: Verify.** Run: `go test ./internal/pkg/tui/ -count=1 && go build ./...`
  Expected: PASS.

- [ ] **Step 4: Lint and commit.**
  ```bash
  make lint
  git add internal/pkg/tui/
  git commit -m "ccx: the tui esc/frame/session tests move to package tui_test — export_test.go re-exports the helpers and wraps session construction/key handling (unexported fields cannot be literalized externally)"
  ```

### Task 13: CLAUDE.md sync + final verification

**Files:**
- Modify: `CLAUDE.md`

- [ ] **Step 1: Update CLAUDE.md.** Assertions to record/replace (keep it one-line-per-fact, no duplication of what's already there):
  - The owner-rule debt note ("remaining in-package test files (10 packages) are recorded debt") is OBSOLETE — every `_test.go` file is now external; the remaining in-package-style package `test/e2e` is gone too.
  - New convention: unexported symbols that block out-of-package moves get seams in `export_test.go` (package-scoped, test-build only — `MenuStagesForTest`, `SetQuotaFetcherForTest`, `SetCopilotAuthClientForTest`, `SetCodexLoginForTest`, `LoginWithBaseForTest`, `EndpointsForTest`, the copilot/tui re-export blocks); `reqdump.SeqForTest` remains the production-file variant of the same idea.
  - `app.New(cfg, log, usageLog)` builds its own `provider.New(cfg, log)` — the provider stays the composition root for the non-serve CLI commands (`provider.NewEnv()`); callers never pass a ready provider.
  - The e2e daemon test lives in `internal/app/e2e_test.go` (package `app_test`) over the suite sandbox; `test/e2e` is gone.
  - Suite boundary: the suite composes the daemon graph only — `clients/*` objects and pure leaf packages keep hand-built tests by design.

- [ ] **Step 2: Full verification.** Run:
  ```bash
  make lint-full
  make test
  make deadcode
  ```
  Expected: lint 0 issues; tests PASS with coverage ≥ 80%; `make deadcode` silent.

- [ ] **Step 3: Commit.**
  ```bash
  git add CLAUDE.md
  git commit -m "docs: CLAUDE.md — the in-package test debt is cleared (external packages + export_test.go seams recorded), app.New owns its provider construction, the e2e daemon test lives in internal/app over the suite sandbox, the suite/leaf boundary is written down"
  ```
