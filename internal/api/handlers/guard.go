package handlers

import (
	"net"
	"net/http"
)

// adminMarker is the header only ccx's own clients send on /admin/*. A
// custom header is not a CORS "simple" one: a browser must preflight it, and
// the preflight has no CORS policy to satisfy — a drive-by web page cannot
// POST /admin/stop fire-and-forget anymore (the removed daemon token used to
// gate this; pass-through auth took it away, see the package doc).
const adminMarker = "X-Ccx"

// requireLocalHost rejects requests whose Host is not a loopback origin.
// Defense against DNS rebinding: a public name that resolves to 127.0.0.1
// would otherwise read the daemon's responses (the bind is loopback-only,
// but the Host header is attacker-controlled). Not auth: any local process
// passes — that is the design (localhost-only, no daemon secret).
func requireLocalHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(r.Host); err == nil {
			host = h
		}
		switch host {
		case "127.0.0.1", "localhost", "::1":
			next.ServeHTTP(w, r)
		default:
			w.WriteHeader(http.StatusForbidden)
		}
	})
}

// requireAdminMarker enforces the X-Ccx header on /admin/* (see adminMarker).
func requireAdminMarker(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(adminMarker) == "" {
			w.WriteHeader(http.StatusForbidden)

			return
		}
		next.ServeHTTP(w, r)
	})
}

// adminAccessLog writes one Info line per /admin/* request (method, path,
// remote address). /admin/stop is the daemon's only fully silent exit — the
// 2026-11-09 ECONNREFUSED forensics could not tell who stopped the daemon —
// so every admin caller identifies itself in daemon-*.log. Deps.Log == nil
// (the in-process tester) skips the line.
func adminAccessLog(d Deps) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if d.Log != nil {
				d.Log.Info("admin request", "method", r.Method, "path", r.URL.Path, "remote", r.RemoteAddr)
			}
			next.ServeHTTP(w, r)
		})
	}
}
