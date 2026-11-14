// Package copilot implements the GitHub OAuth device flow and exchange for a
// copilot-token. Tokens are stored per provider in
// <CCX_HOME>/auth/<key>.json (0600); exchange is lazy — driven by expires_at
// (no background goroutines, per the "lazy refresh" plan).
//
// One operation per file: client.go (the Client + the store + the
// masquerade), device_flow.go (GitHub OAuth device flow), token.go (the
// copilot-token exchange), quota.go (the premium-quota probe).
package copilot

import (
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ivangurin/ccx/internal/pkg/jsonfile"
)

// ErrNotAuthenticated means the device is not authenticated (run ccx auth <provider>).
var ErrNotAuthenticated = errors.New("copilot: not authenticated — run ccx auth <provider>")

// GitHubBase is the OAuth entry point (overridden by tests).
const GitHubBase = "https://github.com"

// APIBase is api.github.com (copilot-token exchange).
const APIBase = "https://api.github.com"

// Defaults are the public VSCode extension parameters (masquerade).
const (
	DefaultClientID = "Iv1.b507a08c87ecfe98"
	DefaultScope    = "read:user"
)

// Masquerade constants (public values of the GitHub Copilot Chat extension
// — the single source for the auth client AND the chat translator): GitHub
// answers the default Go UA on the Copilot endpoints with "Please only use
// approved clients" (notification_id go_http_client, verified live on the
// token exchange) and is suspected of stalling the device-flow poll for it
// — every Copilot-bound request masquerades as the extension.
const (
	MasqueradeUA    = "GitHubCopilotChat/0.28.0"
	EditorVersion   = "vscode/1.99.0"
	EditorPluginVer = "copilot-chat/0.28.0"
	IntegrationID   = "copilot-chat"
)

// masquerade dresses a request as the Copilot Chat extension client.
func masquerade(req *http.Request) {
	req.Header.Set("User-Agent", MasqueradeUA)
	req.Header.Set("Editor-Version", EditorVersion)
	req.Header.Set("Editor-Plugin-Version", EditorPluginVer)
}

// DeviceFlowResult is the device flow start.
type DeviceFlowResult struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	Interval        int    `json:"interval"`
	ExpiresIn       int    `json:"expires_in"`
}

// authStore is the copilot-auth.json store format.
type authStore struct {
	UserAccessToken string `json:"user_access_token"`
	CopilotToken    string `json:"copilot_token,omitempty"`
	ExpiresAt       string `json:"expires_at,omitempty"`
}

// Client is the Copilot OAuth client.
type Client struct {
	codeURL  string
	tokenURL string
	exURL    string
	store    string

	mu     sync.Mutex
	cached string
	expiry time.Time
}

// New creates a client (store is the provider's auth store path). The device
// flow (code + token polling) lives on github.com; only the copilot-token
// exchange is api.github.com — wiring tokenURL to APIBase made every poll
// hit api.github.com/login/oauth/access_token (404, JSON body) and loop
// silently forever.
func New(store string) *Client {
	return NewWithBase(GitHubBase, GitHubBase, APIBase, store)
}

// NewWithBase is a client with overridden URLs (tests).
func NewWithBase(codeURL, tokenURL, exchangeURL, store string) *Client {
	return &Client{
		codeURL:  strings.TrimSuffix(codeURL, "/") + "/login/device/code",
		tokenURL: strings.TrimSuffix(tokenURL, "/") + "/login/oauth/access_token",
		exURL:    strings.TrimSuffix(exchangeURL, "/") + "/copilot_internal/v2/token",
		store:    store,
	}
}

func (c *Client) saveUserToken(token string) error {
	return jsonfile.WriteAtomicJSON(c.store, authStore{UserAccessToken: token}, 0o600)
}

// Authenticated reports whether the store at storePath holds a user login
// (the ccx auth listing probe — no network, just the file).
func Authenticated(storePath string) bool {
	var s authStore
	if err := jsonfile.ReadJSON(storePath, &s); err != nil {
		return false
	}

	return s.UserAccessToken != ""
}

func (c *Client) loadStore() (authStore, error) {
	var s authStore
	if err := jsonfile.ReadJSON(c.store, &s); err != nil {
		return s, ErrNotAuthenticated
	}
	if s.UserAccessToken == "" {
		return s, ErrNotAuthenticated
	}

	return s, nil
}
