// Package handlers — the daemon's HTTP layer: Anthropic-compatible /v1/* for
// Claude Code and /admin/* for the CLI. One port; a static token from
// providers.json (daemon.auth_token) guards both.
package handlers

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/ivangurin/ccx/internal/api/middleware"
	"github.com/ivangurin/ccx/internal/models"
)

// Routerer — the minimum the HTTP layer needs (implemented by services/router.Service).
type Routerer interface {
	Use(ctx context.Context, profile, model string) error
	Active() (string, string, bool)
	Ready() bool
	ServeMessages(w http.ResponseWriter, r *http.Request)
	ServeCountTokens(w http.ResponseWriter, r *http.Request)
	Models() []models.Model
	AuthToken() string
}

// Deps — HTTP layer dependencies.
type Deps struct {
	Router  Routerer
	Version string
	Stop    func() // graceful shutdown (called from /admin/stop)
}

// New assembles a chi router with middleware and routes.
// statusField — the shared status field name in JSON responses.
const statusField = "status"

func New(d Deps) http.Handler {
	r := chi.NewRouter()

	r.Get("/health", handleHealth(d))

	r.Group(func(pr chi.Router) {
		pr.Use(middleware.Auth(d.Router.AuthToken))
		pr.Post("/v1/messages", d.Router.ServeMessages)
		pr.Post("/v1/messages/count_tokens", d.Router.ServeCountTokens)
		pr.Get("/v1/models", handleModels(d.Router))

		pr.Get("/admin/health", handleAdminHealth(d))
		pr.Get("/admin/ready", handleAdminReady(d.Router))
		pr.Post("/admin/use", handleAdminUse(d.Router))
		pr.Post("/admin/stop", handleAdminStop(d))
	})

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
		profile, model, active := d.Router.Active()
		resp := map[string]any{
			"version": d.Version,
			"ready":   d.Router.Ready(),
			"active":  nil,
		}
		if active {
			resp["active"] = map[string]string{"profile": profile, "model": model}
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func handleAdminReady(rt Routerer) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if !rt.Ready() {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{statusField: "not ready"})

			return
		}
		writeJSON(w, http.StatusOK, map[string]string{statusField: "ready"})
	}
}

func handleAdminUse(rt Routerer) http.HandlerFunc {
	type useRequest struct {
		Profile string `json:"profile"`
		Model   string `json:"model"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		var req useRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Profile == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "expected {profile, model}"})

			return
		}
		if err := rt.Use(r.Context(), req.Profile, req.Model); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})

			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "profile": req.Profile, "model": req.Model})
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
