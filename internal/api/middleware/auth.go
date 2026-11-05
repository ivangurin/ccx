// Package middleware — the daemon's HTTP middleware.
package middleware

import (
	"net/http"
	"strings"
)

// Auth — validates the daemon client token: Authorization: Bearer <token>
// or x-api-key: <token> (Claude Code sends both). 401 in the Anthropic
// error format so the client shows a meaningful message.
func Auth(tokenOf func() string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			want := tokenOf()
			got := extractToken(r)
			if want == "" || got == "" || !secureEqual(want, got) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"type":"error","error":{"type":"authentication_error","message":"invalid ccms token (daemon.auth_token)"}}`))

				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func extractToken(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
	}

	return strings.TrimSpace(r.Header.Get("X-Api-Key"))
}

// secureEqual — constant-time comparison (timing attacks on a local token
// are unlikely, but this is cheap).
func secureEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}

	return diff == 0
}
