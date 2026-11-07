// Package sessionid identifies the client's conversation session on a
// best-effort basis: quota accounting and per-session upstream hints (cache
// keys, request ids) need a stable conversation handle, and the client gives
// us one in several places depending on its version.
package sessionid

import (
	"encoding/json"
	"net/http"
	"strings"
)

// sessionMarker splits Claude Code's metadata.user_id:
// user_<hash>_account_<uuid>_session_<uuid> — the part after it is the session.
const sessionMarker = "_session_"

// FromRequest extracts the session id: headers first (the client's own
// session header when it sends one; the codex-client convention only counts
// beyond 20 chars — shorter values are not sessions), then the request
// metadata (session_id, or the _session_ tail of user_id). Empty when nothing
// is found — callers treat that as "no session", never as an error.
func FromRequest(r *http.Request, metadata json.RawMessage) string {
	if sid := fromHeaders(r); sid != "" {
		return sid
	}

	return fromMetadata(metadata)
}

func fromHeaders(r *http.Request) string {
	for _, h := range []string{
		"X-Claude-Code-Session-Id",
		"Claude-Code-Session-Id",
		"Session-Id",
		"X-Session-Id",
	} {
		if v := strings.TrimSpace(r.Header.Get(h)); v != "" {
			if (h == "Session-Id" || h == "X-Session-Id") && len(v) <= 20 {
				continue
			}

			return v
		}
	}

	return ""
}

func fromMetadata(metadata json.RawMessage) string {
	var meta struct {
		SessionID string `json:"session_id"`
		UserID    string `json:"user_id"`
	}
	if len(metadata) == 0 || json.Unmarshal(metadata, &meta) != nil {
		return ""
	}
	if meta.SessionID != "" {
		return meta.SessionID
	}
	idx := strings.LastIndex(meta.UserID, sessionMarker)
	if idx < 0 {
		return ""
	}

	return strings.TrimSpace(meta.UserID[idx+len(sessionMarker):])
}
