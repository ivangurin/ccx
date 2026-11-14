package codex

import (
	"errors"
	"fmt"
	"time"

	"github.com/ivangurin/ccx/internal/pkg/flock"
	"github.com/ivangurin/ccx/internal/pkg/jsonfile"
)

// ErrNotLoggedIn means the provider's auth store is missing/empty (ccx auth needed).
var ErrNotLoggedIn = errors.New("codex: not authenticated — run ccx auth <provider>")

// authLockTimeout bounds the wait for a concurrent ccx writer (a refresh
// carries a network call; anything slower than this is stuck, not busy).
const authLockTimeout = 15 * time.Second

// Auth is the slice of the auth store we need.
type Auth struct {
	AccessToken  string
	RefreshToken string
	IDToken      string    // tokens.id_token — persisted by the login (a foreign reader of a symlinked store derives the account from it)
	AccountID    string    // tokens.account_id of the live file
	LastRefresh  time.Time // tokens.last_refresh — the generation clock
}

// authFile is the full live auth.json format (codex CLI fields must survive
// our refresh: an RMW patch, not a file recreation).
type authFile struct {
	AuthMode    string `json:"auth_mode"`
	LastRefresh string `json:"last_refresh,omitempty"`
	Tokens      struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		AccountID    string `json:"account_id"`
		IDToken      string `json:"id_token"`
		LastRefresh  string `json:"last_refresh"`
	} `json:"tokens"`
}

// Authenticated reports whether the auth store at path holds a login (the
// ccx auth listing probe — no network, just the file).
func Authenticated(path string) bool {
	_, err := LoadAuth(path)

	return err == nil
}

// LoadAuth reads the stored login.
func LoadAuth(path string) (Auth, error) {
	var f authFile
	if err := jsonfile.ReadJSON(path, &f); err != nil {
		return Auth{}, ErrNotLoggedIn
	}
	if f.Tokens.AccessToken == "" && f.Tokens.RefreshToken == "" {
		return Auth{}, ErrNotLoggedIn
	}

	return Auth{
		AccessToken:  f.Tokens.AccessToken,
		RefreshToken: f.Tokens.RefreshToken,
		AccountID:    f.Tokens.AccountID,
		LastRefresh:  parseRefreshClock(f.Tokens.LastRefresh),
	}, nil
}

// SaveAuth is read-modify-write: it reads the existing file and patches
// tokens.access/refresh/last_refresh plus (when the caller derived one)
// account_id — auth_mode, id_token and other auth.json fields survive
// untouched (the file may be a hand-made symlink to a foreign auth.json, so
// the RMW keeps it byte-compatible with its other readers). If the file is
// missing, it creates a minimal one. The whole read-modify-write runs under
// the sidecar flock — see the package doc.
//
// This is the SEEDING path (writing tokens obtained elsewhere into the
// file). Refresh results must go through Token → saveAuthCAS: this write is
// generation-naive (no compare-and-swap, last_refresh stamped at write time)
// and would clobber a concurrent native-CLI generation.
func SaveAuth(path string, auth Auth) error {
	lock, err := flock.LockWait(path+".lock", authLockTimeout)
	if err != nil {
		return fmt.Errorf("codex: auth lock: %w", err)
	}
	defer func() { _ = lock.Unlock() }()

	return saveAuthLocked(path, auth)
}

func saveAuthLocked(path string, auth Auth) error {
	var f authFile
	_ = jsonfile.ReadJSON(path, &f) // missing file is fine — we will create it
	// seed only: an existing auth_mode is someone else's truth and stays
	if f.AuthMode == "" {
		f.AuthMode = "chatgpt"
	}
	f.Tokens.AccessToken = auth.AccessToken
	f.Tokens.RefreshToken = auth.RefreshToken
	// caller-wins: the only production caller is the login, which derives
	// the id from the FRESH login's id_token — keeping a stored one would
	// send the old account's header with the new account's token (an empty
	// derivation still preserves a foreign value)
	if auth.AccountID != "" {
		f.Tokens.AccountID = auth.AccountID
	}
	// same policy for the id_token itself: the login holds the fresh one —
	// a stale id_token paired with new tokens misleads a foreign reader of
	// a symlinked store (an empty value still preserves a foreign one)
	if auth.IDToken != "" {
		f.Tokens.IDToken = auth.IDToken
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	f.Tokens.LastRefresh = now
	f.LastRefresh = now

	return jsonfile.WriteAtomicJSON(path, f, 0o600)
}
