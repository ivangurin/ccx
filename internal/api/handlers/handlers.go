// Package handlers — the daemon's HTTP layer: Anthropic-compatible /v1/* for
// Claude Code and /admin/* for the CLI. One port, localhost-only; no auth
// middleware — upstream credentials are pass-through (openai) or owned by the
// translator (copilot/codex), so the daemon has no secret of its own.
package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/ivangurin/ccms/internal/models"
)

// Routerer — the minimum the HTTP layer needs (implemented by services/router.Service).
type Routerer interface {
	Active() (provider string, protocol models.Protocol, ok bool)
	ServeMessages(w http.ResponseWriter, r *http.Request)
	ServeCountTokens(w http.ResponseWriter, r *http.Request)
	Models() []models.Model
}

// Deps — HTTP layer dependencies.
type Deps struct {
	Router  Routerer
	Version string
	Port    int // the resolved listen port (reported by /admin/health)
	Stop    func() // graceful shutdown (called from /admin/stop)
}

// New assembles a chi router with middleware and routes.
// statusField — the shared status field name in JSON responses.
const statusField = "status"

func New(d Deps) http.Handler {
	r := chi.NewRouter()

	r.Get("/health", handleHealth(d))

	r.Post("/v1/messages", d.Router.ServeMessages)
	r.Post("/v1/messages/count_tokens", d.Router.ServeCountTokens)
	r.Get("/v1/models", handleModels(d.Router))

	r.Get("/admin/health", handleAdminHealth(d))
	r.Post("/admin/stop", handleAdminStop(d))

	return r
}

func handleHealth(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{statusField: "ok", "version": d.Version})
	}
}

func handleModels(rt Routerer) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		profileModels := rt.Models()
		data := make([]map[string]string, 0, len(profileModels))
		for _, m := range profileModels {
			data = append(data, map[string]string{"id": m.ID, "display_name": m.Label})
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": data})
	}
}

func handleAdminHealth(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		provider, protocol, active := d.Router.Active()
		resp := map[string]any{
			"version": d.Version,
			// /health answering already implies the route is ready (Build ran
			// before listening); kept as a constant for probe compatibility.
			"ready":  true,
			"port":   d.Port,
			"active": nil,
		}
		if active {
			resp["active"] = map[string]string{"provider": provider, "protocol": string(protocol)}
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func handleAdminStop(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{statusField: "stopping"})
		if d.Stop != nil {
			go d.Stop()
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	data, err := json.Marshal(v)
	if err != nil {
		data = []byte(`{"error":"marshal"}`)
	}
	_, _ = w.Write(data)
	_, _ = w.Write([]byte("\n"))
}
