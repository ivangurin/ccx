// Package middleware — HTTP-middleware демона.
package middleware

import (
	"net/http"
	"strings"
)

// Auth — проверка клиентского токена демона: Authorization: Bearer <token>
// либо x-api-key: <token> (Claude Code шлёт оба варианта). 401 в формате
// Anthropic-ошибки, чтобы клиент показал осмысленное сообщение.
func Auth(tokenOf func() string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			want := tokenOf()
			got := extractToken(r)
			if want == "" || got == "" || !secureEqual(want, got) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"type":"error","error":{"type":"authentication_error","message":"недействительный токен ccx (daemon.auth_token)"}}`))

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

// secureEqual — сравнение за константное время (timing-атаки на локальный
// токен маловероятны, но дёшево).
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
