package codex

import (
	"time"

	"github.com/ivangurin/ccx/internal/pkg/jsonfile"
)

// newerGeneration: strictly newer wins; ties and unknowns lose (refusing to
// act on an ambiguous order is safer than guessing).
func newerGeneration(disk, loaded time.Time) bool {
	return disk.After(loaded)
}

// saveAuthCAS is the generation-aware save for OUR refresh results: if the
// refresh token on disk no longer equals the one we refreshed from, a
// non-cooperating writer (the codex CLI) landed a generation of its own — a
// strictly newer one wins and we do not overwrite; anything else (unchanged,
// or an older foreign write) takes our tokens. last_refresh is the
// acquisition moment, not the write moment — the ordering must survive a
// slow disk while another generation is in flight.
func saveAuthCAS(path string, auth Auth, expectedRefreshToken string, acquiredAt time.Time) error {
	var f authFile
	_ = jsonfile.ReadJSON(path, &f) // missing file is fine — we will create it
	if f.Tokens.RefreshToken != "" && f.Tokens.RefreshToken != expectedRefreshToken {
		if newerGeneration(parseRefreshClock(f.Tokens.LastRefresh), acquiredAt) {
			return nil // the foreign generation stays; ours is already in hand
		}
	}
	f.Tokens.AccessToken = auth.AccessToken
	f.Tokens.RefreshToken = auth.RefreshToken
	if f.Tokens.AccountID == "" {
		f.Tokens.AccountID = auth.AccountID
	}
	clock := acquiredAt.UTC().Format(time.RFC3339Nano)
	f.Tokens.LastRefresh = clock
	f.LastRefresh = clock

	return jsonfile.WriteAtomicJSON(path, f, 0o600)
}
