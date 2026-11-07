package codex

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ivangurin/ccx/internal/pkg/jsonfile"
	"github.com/stretchr/testify/require"
)

// OAuth query parameter literals (goconst: they repeat across the tests).
const (
	codeParam  = "code"
	stateParam = "state"
	authCode   = "c1" // the fake authorization code the tests redeem
)

// jwtWithAccountID crafts an id_token whose payload carries the ChatGPT
// account claim (the codex CLI's account_id derivation source).
func jwtWithAccountID(t *testing.T, accountID string) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"https://api.openai.com/auth": map[string]string{"chatgpt_account_id": accountID},
	})
	require.NoError(t, err)

	return base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`)) + "." +
		base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

// loginFixture drives loginWithBase as the OAuth parties: openBrowser
// captures the authorize URL, the test plays the browser hitting the
// callback, and a fake token endpoint answers the exchange. exchangeForm
// holds the form the token endpoint received (read AFTER login finished —
// the errs channel establishes the happens-before).
type loginFixture struct {
	authPath     string
	authorizeCh  chan string // openBrowser delivers the authorize URL here
	authorizeURL url.URL
	exchangeForm url.Values
	exchangeHit  bool
	tokenSrv     *httptest.Server
	// occupyDefaultPort: the test itself binds 1455 before startLogin (the
	// fallback-port test) — the port guard then checks only 1457.
	occupyDefaultPort bool
}

// newLoginFixture builds the fake token endpoint. handleExchange decides
// the endpoint's answer; the recorded form is asserted after the login.
func newLoginFixture(t *testing.T, authPath string, handleExchange func(w http.ResponseWriter, form url.Values)) *loginFixture {
	t.Helper()
	lf := &loginFixture{authPath: authPath, authorizeCh: make(chan string, 1)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ParseForm() != nil {
			w.WriteHeader(http.StatusBadRequest)

			return
		}
		lf.exchangeForm = r.PostForm
		lf.exchangeHit = true
		handleExchange(w, r.PostForm)
	}))
	t.Cleanup(srv.Close)
	lf.tokenSrv = srv

	return lf
}

// authorizeBase is the fake authorize endpoint URL (same fake server); the
// path mirrors the real issuer's authorize endpoint.
func (lf *loginFixture) authorizeBase() string {
	return lf.tokenSrv.URL + "/oauth/authorize"
}

// skipIfLoginPortsBusy guards the registered callback ports: a live
// `ccx auth` / `codex login` on this machine must not be /cancel-nudged (or
// flake the suite) — a test that will bind 1455/1457 skips when one is
// already held. Defaults to both ports; pass a subset when the test holds
// one itself.
func skipIfLoginPortsBusy(t *testing.T, ports ...int) {
	t.Helper()
	if len(ports) == 0 {
		ports = []int{loginPort, loginFallbackPort}
	}
	for _, port := range ports {
		ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err != nil {
			t.Skipf("callback port %d busy — a live login may be in progress", port)
		}
		_ = ln.Close()
	}
}

// startLogin runs loginWithBase in the background and returns once
// openBrowser captured the authorize URL.
func (lf *loginFixture) startLogin(t *testing.T) chan error {
	t.Helper()
	if lf.occupyDefaultPort {
		skipIfLoginPortsBusy(t, loginFallbackPort)
	} else {
		skipIfLoginPortsBusy(t)
	}
	errs := make(chan error, 1)
	go func() {
		errs <- loginWithBase(context.Background(), lf.authorizeBase(), lf.tokenSrv.URL, lf.authPath,
			func(u string) error {
				lf.authorizeCh <- u

				return nil
			})
	}()
	select {
	case raw := <-lf.authorizeCh:
		parsed, err := url.Parse(raw)
		require.NoError(t, err)
		lf.authorizeURL = *parsed
	case <-time.After(5 * time.Second):
		t.Fatal("openBrowser was never called")
	}

	return errs
}

// hitCallback plays the browser: GET the redirect_uri with the query and
// expect the given status (401 answers a state mismatch).
func (lf *loginFixture) hitCallback(t *testing.T, status int, query url.Values) {
	t.Helper()
	cb := lf.authorizeURL.Query().Get("redirect_uri") + "?" + query.Encode()
	resp, err := http.Get(cb) //nolint:gosec // local test server
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	require.Equal(t, status, resp.StatusCode, "callback body: %s", body)
}

func (lf *loginFixture) state() string {
	return lf.authorizeURL.Query().Get("state")
}

func storeExists(path string) bool {
	_, err := os.Stat(path)

	return err == nil
}

func TestLogin_HappyPath(t *testing.T) {
	lf := newLoginFixture(t, filepath.Join(t.TempDir(), "auth", "cx.json"),
		func(w http.ResponseWriter, _ url.Values) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"AT","refresh_token":"RT","id_token":"` + jwtWithAccountID(t, "acct-1") + `"}`))
		})
	errs := lf.startLogin(t)

	state := lf.state()
	require.NotEmpty(t, state)
	lf.hitCallback(t, http.StatusOK, url.Values{codeParam: {authCode}, stateParam: {state}})
	require.NoError(t, <-errs)

	require.True(t, lf.exchangeHit)
	require.Equal(t, "authorization_code", lf.exchangeForm.Get("grant_type"))
	require.Equal(t, "c1", lf.exchangeForm.Get("code"))
	require.Equal(t, ClientID, lf.exchangeForm.Get("client_id"))
	require.Len(t, lf.exchangeForm.Get("code_verifier"), 43)
	require.Equal(t, lf.authorizeURL.Query().Get("redirect_uri"), lf.exchangeForm.Get("redirect_uri"))

	wantIDToken := jwtWithAccountID(t, "acct-1")
	var f struct {
		AuthMode    string `json:"auth_mode"`
		LastRefresh string `json:"last_refresh"`
		Tokens      struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			AccountID    string `json:"account_id"`
			IDToken      string `json:"id_token"`
		} `json:"tokens"`
	}
	require.NoError(t, jsonfile.ReadJSON(lf.authPath, &f))
	require.Equal(t, "chatgpt", f.AuthMode)
	require.Equal(t, "AT", f.Tokens.AccessToken)
	require.Equal(t, "RT", f.Tokens.RefreshToken)
	require.Equal(t, "acct-1", f.Tokens.AccountID)
	require.Equal(t, wantIDToken, f.Tokens.IDToken) // the fresh login's id_token is persisted
	require.NotEmpty(t, f.LastRefresh)
}

func TestLogin_AuthorizeURLParams(t *testing.T) {
	lf := newLoginFixture(t, filepath.Join(t.TempDir(), "auth", "cx.json"),
		func(w http.ResponseWriter, _ url.Values) {
			_, _ = w.Write([]byte(`{"access_token":"AT","refresh_token":"RT"}`))
		})
	errs := lf.startLogin(t)
	q := lf.authorizeURL.Query()

	require.Equal(t, "/oauth/authorize", lf.authorizeURL.Path)
	require.Equal(t, "code", q.Get("response_type"))
	require.Equal(t, "S256", q.Get("code_challenge_method"))
	require.Equal(t, "openid profile email offline_access api.connectors.read api.connectors.invoke", q.Get("scope"))
	require.Equal(t, ClientID, q.Get("client_id"))
	require.NotEmpty(t, q.Get("code_challenge"))
	// The issuer's redirect-URI allow-list is exact (Hydra): the registered
	// URI is http://localhost:1455/auth/callback — anything else completes
	// the browser login but never receives the redirect (the live hang).
	require.Equal(t, "http://localhost:1455/auth/callback", q.Get("redirect_uri"))
	require.Equal(t, "codex_cli_rs", q.Get("originator"))
	require.Equal(t, "true", q.Get("id_token_add_organizations"))
	require.Equal(t, "true", q.Get("codex_cli_simplified_flow"))

	lf.hitCallback(t, http.StatusOK, url.Values{codeParam: {"c"}, stateParam: {lf.state()}})
	require.NoError(t, <-errs)
}

// Login (the production wiring) must point the browser at the real issuer's
// authorize endpoint and the allow-listed callback URI — an ephemeral-port
// redirect_uri here is exactly the hang seen live.
func TestLogin_ProductionAuthorizeURL(t *testing.T) {
	urlCh := make(chan string, 1)
	ctx, cancel := context.WithCancel(context.Background())
	errs := make(chan error, 1)
	go func() {
		errs <- Login(ctx, filepath.Join(t.TempDir(), "auth", "cx.json"), func(u string) error {
			urlCh <- u

			return nil
		})
	}()

	select {
	case raw := <-urlCh:
		parsed, err := url.Parse(raw)
		require.NoError(t, err)
		require.Equal(t, AuthBase+"/oauth/authorize", parsed.Scheme+"://"+parsed.Host+parsed.Path)
		require.Equal(t, "http://localhost:1455/auth/callback", parsed.Query().Get("redirect_uri"))
	case <-time.After(5 * time.Second):
		t.Fatal("openBrowser was never called")
	}
	cancel()

	require.Error(t, <-errs)
}

// A busy 1455 (a parked earlier login) must not kill the login: after a
// /cancel nudge the flow falls back to the other registered callback port.
func TestLogin_FallbackPortWhenDefaultBusy(t *testing.T) {
	blocker, err := net.Listen("tcp", "127.0.0.1:1455")
	require.NoError(t, err)
	go func() { // swallow the /cancel nudge and hold the port
		for {
			conn, aerr := blocker.Accept()
			if aerr != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	t.Cleanup(func() { _ = blocker.Close() })

	lf := newLoginFixture(t, filepath.Join(t.TempDir(), "auth", "cx.json"),
		func(w http.ResponseWriter, _ url.Values) {
			_, _ = w.Write([]byte(`{"access_token":"AT","refresh_token":"RT"}`))
		})
	lf.occupyDefaultPort = true
	errs := lf.startLogin(t)

	require.Equal(t, "http://localhost:1457/auth/callback", lf.authorizeURL.Query().Get("redirect_uri"))
	lf.hitCallback(t, http.StatusOK, url.Values{codeParam: {authCode}, stateParam: {lf.state()}})
	require.NoError(t, <-errs)
}

// GET /cancel is how a next login unblocks a parked one: the running login
// must exit with an error, not hang until its context budget runs out.
func TestLogin_CancelEndpoint(t *testing.T) {
	lf := newLoginFixture(t, filepath.Join(t.TempDir(), "auth", "cx.json"),
		func(w http.ResponseWriter, _ url.Values) { _, _ = w.Write([]byte(`{}`)) })
	errs := lf.startLogin(t)

	base := strings.TrimSuffix(lf.authorizeURL.Query().Get("redirect_uri"), loginCallbackPath)
	resp, err := http.Get(base + loginCancelPath) //nolint:gosec // local test server
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	require.ErrorContains(t, <-errs, "cancel")
	require.False(t, storeExists(lf.authPath))
}

// A forged/mismatched callback must NOT end the login (codex-rs behavior):
// the browser gets a 400, the login keeps waiting for its own callback — a
// stale tab from an earlier login or a local prober must not kill a fresh
// 15-minute wait.
func TestLogin_StateMismatchKeepsWaiting(t *testing.T) {
	lf := newLoginFixture(t, filepath.Join(t.TempDir(), "auth", "cx.json"),
		func(w http.ResponseWriter, _ url.Values) {
			_, _ = w.Write([]byte(`{"access_token":"AT","refresh_token":"RT"}`))
		})
	errs := lf.startLogin(t)

	lf.hitCallback(t, http.StatusBadRequest, url.Values{codeParam: {authCode}, stateParam: {"forged"}})

	select {
	case err := <-errs:
		t.Fatalf("login aborted on a forged callback: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	// the login still completes on its own valid callback
	lf.hitCallback(t, http.StatusOK, url.Values{codeParam: {authCode}, stateParam: {lf.state()}})
	require.NoError(t, <-errs)
	require.True(t, lf.exchangeHit)
}

// Only /auth/callback participates in the exchange: any other path is a 404
// and the login keeps waiting (a stray localhost request must not kill it).
func TestLogin_ForeignPathGets404(t *testing.T) {
	lf := newLoginFixture(t, filepath.Join(t.TempDir(), "auth", "cx.json"),
		func(w http.ResponseWriter, _ url.Values) {
			_, _ = w.Write([]byte(`{"access_token":"AT","refresh_token":"RT"}`))
		})
	errs := lf.startLogin(t)

	base := strings.TrimSuffix(lf.authorizeURL.Query().Get("redirect_uri"), loginCallbackPath)
	resp, err := http.Get(base + "/favicon.ico") //nolint:gosec // local test server
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusNotFound, resp.StatusCode)

	select {
	case err := <-errs:
		t.Fatalf("login aborted on a foreign path: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	lf.hitCallback(t, http.StatusOK, url.Values{codeParam: {authCode}, stateParam: {lf.state()}})
	require.NoError(t, <-errs)
}

func TestLogin_CallbackErrorParam(t *testing.T) {
	lf := newLoginFixture(t, filepath.Join(t.TempDir(), "auth", "cx.json"),
		func(w http.ResponseWriter, _ url.Values) { _, _ = w.Write([]byte(`{}`)) })
	errs := lf.startLogin(t)

	// error_description carries the actionable reason (e.g.
	// missing_codex_entitlement) — it must reach the user, not just the code
	lf.hitCallback(t, http.StatusOK, url.Values{
		"error":             {"access_denied"},
		"error_description": {"missing_codex_entitlement"},
		stateParam:          {lf.state()},
	})

	err := <-errs
	require.ErrorContains(t, err, "access_denied")
	require.ErrorContains(t, err, "missing_codex_entitlement")
	require.False(t, storeExists(lf.authPath))
	require.False(t, lf.exchangeHit)
}

// A valid-state callback without a code is terminal: the login must exit
// with an error immediately, not hang until its context budget (codex-rs
// exits with "Missing authorization code" for the same case).
func TestLogin_CallbackWithoutCodeFails(t *testing.T) {
	lf := newLoginFixture(t, filepath.Join(t.TempDir(), "auth", "cx.json"),
		func(w http.ResponseWriter, _ url.Values) { _, _ = w.Write([]byte(`{}`)) })
	errs := lf.startLogin(t)

	lf.hitCallback(t, http.StatusBadRequest, url.Values{stateParam: {lf.state()}})

	select {
	case err := <-errs:
		require.ErrorContains(t, err, "missing authorization code")
	case <-time.After(2 * time.Second):
		t.Fatal("login hangs on a codeless callback")
	}
	require.False(t, storeExists(lf.authPath))
	require.False(t, lf.exchangeHit)
}

func TestLogin_TokenEndpointError(t *testing.T) {
	lf := newLoginFixture(t, filepath.Join(t.TempDir(), "auth", "cx.json"),
		func(w http.ResponseWriter, _ url.Values) { w.WriteHeader(http.StatusBadRequest) })
	errs := lf.startLogin(t)

	lf.hitCallback(t, http.StatusOK, url.Values{codeParam: {authCode}, stateParam: {lf.state()}})

	require.ErrorContains(t, <-errs, "status 400")
	require.False(t, storeExists(lf.authPath))
}

// A token response without refresh_token must fail the login HERE: storing
// it "successfully" surfaces hours later as "access expired, no refresh
// token" and forces a full browser redo. (id_token stays optional — the
// account derivation is best-effort by spec.)
func TestLogin_TokenEndpointMissingRefreshToken(t *testing.T) {
	lf := newLoginFixture(t, filepath.Join(t.TempDir(), "auth", "cx.json"),
		func(w http.ResponseWriter, _ url.Values) {
			_, _ = w.Write([]byte(`{"access_token":"AT"}`))
		})
	errs := lf.startLogin(t)

	lf.hitCallback(t, http.StatusOK, url.Values{codeParam: {authCode}, stateParam: {lf.state()}})

	require.ErrorContains(t, <-errs, "unexpected response")
	require.False(t, storeExists(lf.authPath))
}

func TestLogin_ContextCancelled(t *testing.T) {
	skipIfLoginPortsBusy(t)
	lf := newLoginFixture(t, filepath.Join(t.TempDir(), "auth", "cx.json"),
		func(w http.ResponseWriter, _ url.Values) { _, _ = w.Write([]byte(`{}`)) })

	ctx, cancel := context.WithCancel(context.Background())
	errs := make(chan error, 1)
	go func() {
		errs <- loginWithBase(ctx, lf.authorizeBase(), lf.tokenSrv.URL, lf.authPath, func(string) error { return nil })
	}()
	time.Sleep(100 * time.Millisecond) // let the listener come up
	cancel()

	require.Error(t, <-errs)
	require.False(t, storeExists(lf.authPath))
}

// The account_id derivation is best effort (spec): a malformed id_token
// must not waste an already-completed browser login — the store is written
// with an empty account id and the header is simply not sent.
func TestLogin_MalformedIDTokenStillLogsIn(t *testing.T) {
	lf := newLoginFixture(t, filepath.Join(t.TempDir(), "auth", "cx.json"),
		func(w http.ResponseWriter, _ url.Values) {
			_, _ = w.Write([]byte(`{"access_token":"AT","refresh_token":"RT","id_token":"not-a-jwt"}`))
		})
	errs := lf.startLogin(t)

	lf.hitCallback(t, http.StatusOK, url.Values{codeParam: {authCode}, stateParam: {lf.state()}})
	require.NoError(t, <-errs)

	var f struct {
		Tokens struct {
			AccountID   string `json:"account_id"`
			AccessToken string `json:"access_token"`
		} `json:"tokens"`
	}
	require.NoError(t, jsonfile.ReadJSON(lf.authPath, &f))
	require.Empty(t, f.Tokens.AccountID)
	require.Equal(t, "AT", f.Tokens.AccessToken)
}
