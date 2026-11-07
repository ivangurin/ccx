// login.go — the ChatGPT OAuth login that seeds the provider's auth store
// (the ccx-owned replacement for "codex login"): PKCE S256 against
// auth.openai.com with the codex CLI's public client, a one-shot localhost
// callback server catching the authorization code, and persistence through
// SaveAuth (the documented seeding path).
//
// The flow mirrors codex-rs login/server.rs exactly where the issuer gates
// it: the redirect-URI allow-list is EXACT (Hydra) — the registered URIs
// are http://localhost:1455/auth/callback and the fallback
// http://localhost:1457/auth/callback — so an ephemeral port would let the
// user finish the browser login yet never receive the redirect (the login
// then hangs; verified live), and the authorize endpoint is /oauth/authorize
// with the CLI's parameter set.
package codex

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// The registered callback ports ("Keep in sync with the Codex CLI Hydra
// redirect URI allow-list" — codex-rs login/server.rs) and the paths the
// callback server answers.
const (
	loginPort         = 1455
	loginFallbackPort = 1457
	loginCallbackPath = "/auth/callback"
	loginCancelPath   = "/cancel"
	// loginOriginator mirrors the codex CLI's originator in the authorize
	// request (codex-rs DEFAULT_ORIGINATOR) — part of the registered flow.
	loginOriginator = "codex_cli_rs"
	// loginScope is the codex CLI's authorize scope (the connectors grants
	// ride the same registration; a narrower scope is not what the
	// allow-listed client is registered for).
	loginScope = "openid profile email offline_access api.connectors.read api.connectors.invoke"
)

// bind tuning: a busy 1455 is usually a parked earlier login of our own —
// one /cancel nudge plus short retries, then the fallback port.
const (
	loginBindAttempts   = 3
	loginBindRetryDelay = 150 * time.Millisecond
	loginCancelDeadline = 2 * time.Second
)

// callbackResult is what the one-shot callback server delivers: either an
// authorization code or the OAuth error the issuer reported.
type callbackResult struct {
	code  string
	oauth string // the ?error= parameter, non-empty on a denied/failed login
}

// Login runs the ChatGPT OAuth flow and writes the tokens into authPath.
// openBrowser receives the authorize URL (the CLI prints it and opens the
// browser; its error is not fatal — the URL is visible for a manual open).
// The caller owns the context budget (the CLI arms 15 minutes, copilot
// parity).
func Login(ctx context.Context, authPath string, openBrowser func(string) error) error {
	return loginWithBase(ctx, AuthBase+"/oauth/authorize", AuthBase+"/oauth/token", authPath, openBrowser)
}

// loginWithBase is Login with overridden endpoints (tests).
func loginWithBase(ctx context.Context, authorizeBase, tokenURL, authPath string, openBrowser func(string) error) error {
	verifier, challenge, err := pkcePair()
	if err != nil {
		return fmt.Errorf("codex: login: %w", err)
	}
	state, err := randomState()
	if err != nil {
		return fmt.Errorf("codex: login: %w", err)
	}

	listener, port, err := bindLoginListener()
	if err != nil {
		return err
	}
	redirectURI := "http://localhost:" + strconv.Itoa(port) + loginCallbackPath

	authorize := authorizeBase + "?" + url.Values{
		"client_id":                  {ClientID},
		"response_type":              {"code"},
		"redirect_uri":               {redirectURI},
		"scope":                      {loginScope},
		"code_challenge":             {challenge},
		"code_challenge_method":      {"S256"},
		"state":                      {state},
		"id_token_add_organizations": {"true"},
		"codex_cli_simplified_flow":  {"true"},
		"originator":                 {loginOriginator},
	}.Encode()

	// A failed opener is not a failed login: the CLI has printed the URL,
	// the user can open it manually.
	_ = openBrowser(authorize)

	result := make(chan callbackResult, 1)
	srv := &http.Server{Handler: callbackHandler(state, result)} //nolint:gosec // localhost one-shot, no timeouts by design
	go func() { _ = srv.Serve(listener) }()
	// Shutdown (bounded) lets the handler flush the browser's final page
	// before the process side of the login returns; Close is the backstop
	// for a hung keep-alive connection.
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		_ = srv.Close()
	}()

	select {
	case <-ctx.Done():
		return fmt.Errorf("codex: login: interrupted: %w", ctx.Err())
	case res := <-result:
		if res.oauth != "" {
			return fmt.Errorf("codex: login: %s", res.oauth)
		}
		tokens, err := exchangeCode(ctx, tokenURL, res.code, redirectURI, verifier)
		if err != nil {
			return err
		}
		accountID := accountIDFromIDToken(tokens.IDToken)
		auth := Auth{
			AccessToken:  tokens.AccessToken,
			RefreshToken: tokens.RefreshToken,
			AccountID:    accountID,
		}
		if err := SaveAuth(authPath, auth); err != nil {
			return err
		}

		return nil
	}
}

// callbackHandler answers the one-shot callback: /cancel unblocks a parked
// login (checked FIRST — the cancel request carries no state by design),
// then the callback validates the state, surfaces an issuer-reported error,
// and delivers the code.
func callbackHandler(expectedState string, result chan<- callbackResult) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == loginCancelPath {
			deliverResult(w, result, callbackResult{oauth: "cancelled"}, "login cancelled")

			return
		}
		q := r.URL.Query()
		if oauthErr := q.Get("error"); oauthErr != "" {
			deliverResult(w, result, callbackResult{oauth: oauthErr}, "login failed — return to the terminal")

			return
		}
		if q.Get("state") != expectedState {
			// A forged/mismatched callback: answer the browser, and hand the
			// login an error — staying silent would hang it until the context
			// budget ran out.
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte("<html><body><p>state mismatch — return to the terminal</p></body></html>"))
			select {
			case result <- callbackResult{oauth: "state mismatch"}:
			default:
			}

			return
		}
		code := q.Get("code")
		if code == "" {
			w.WriteHeader(http.StatusBadRequest)

			return
		}
		deliverResult(w, result, callbackResult{code: code}, "login complete — return to the terminal")
	})
}

// deliverResult answers the browser and wakes the waiting login.
func deliverResult(w http.ResponseWriter, result chan<- callbackResult, res callbackResult, page string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte("<html><body><p>" + page + "</p></body></html>"))
	select {
	case result <- res:
	default: // a second callback (e.g. a refresh) — already logged in
	}
}

// bindLoginListener binds the registered callback ports in order (1455,
// then the fallback 1457 — the issuer's redirect-URI allow-list accepts
// exactly these two). A busy 1455 is usually a parked login of our own:
// nudge it with /cancel, retry briefly, then fall back to 1457.
func bindLoginListener() (net.Listener, int, error) {
	for _, port := range []int{loginPort, loginFallbackPort} {
		for attempt := range loginBindAttempts {
			listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
			if err == nil {
				return listener, port, nil
			}
			if port == loginPort && attempt == 0 {
				sendCancelRequest(port)
			}
			time.Sleep(loginBindRetryDelay)
		}
	}

	return nil, 0, errors.New("codex: login: callback ports 1455/1457 are busy — another login in progress?")
}

// sendCancelRequest nudges whatever holds the port with GET /cancel (the
// codex CLI's own unblock protocol): our parked login answers it and exits;
// a foreign listener just sees a harmless GET. Best effort — failures only
// cost a retry.
func sendCancelRequest(port int) {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), loginCancelDeadline)
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(loginCancelDeadline))
	_, _ = conn.Write([]byte("GET " + loginCancelPath + " HTTP/1.1\r\n" +
		"Host: 127.0.0.1:" + strconv.Itoa(port) + "\r\nConnection: close\r\n\r\n"))
	buf := make([]byte, 64) // drain the answer (or EOF) so the peer is not left writing
	_, _ = conn.Read(buf)
}

// exchangeCode swaps the authorization code for the token pair at the same
// endpoint the refresh uses.
func exchangeCode(ctx context.Context, tokenURL, code, redirectURI, verifier string) (*tokenPair, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {ClientID},
		"code_verifier": {verifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode())) //nolint:gosec // caller-supplied test URL
	if err != nil {
		return nil, fmt.Errorf("codex: login: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("codex: login: token exchange: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("codex: login: token exchange: status %d", resp.StatusCode)
	}
	var pair tokenPair
	if err := json.NewDecoder(resp.Body).Decode(&pair); err != nil || pair.AccessToken == "" {
		return nil, errors.New("codex: login: token exchange: unexpected response")
	}

	return &pair, nil
}

// tokenPair is the exchange response shape.
type tokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
}

// accountIDFromIDToken extracts chatgpt_account_id from the id_token claims
// (the codex CLI's derivation). Best effort by design (spec): a missing
// claim or a malformed token yields "" — the account header is then simply
// not sent (the translator's existing behavior for an empty value), never a
// wasted browser login.
func accountIDFromIDToken(idToken string) string {
	if idToken == "" {
		return ""
	}
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		Auth struct {
			ChatGPTAccountID string `json:"chatgpt_account_id"`
		} `json:"https://api.openai.com/auth"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}

	return claims.Auth.ChatGPTAccountID
}

// pkcePair: verifier = base64url(32 random bytes) (43 chars, RFC 7636),
// challenge = base64url(SHA-256(verifier)), method S256.
func pkcePair() (verifier, challenge string, err error) {
	buf := make([]byte, 32)
	if _, err = rand.Read(buf); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(buf)
	sum := sha256.Sum256([]byte(verifier))

	return verifier, base64.RawURLEncoding.EncodeToString(sum[:]), nil
}

// randomState: 16 random bytes hex — binds the callback to this login.
func randomState() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}

	return hex.EncodeToString(buf), nil
}
