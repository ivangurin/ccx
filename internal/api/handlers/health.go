package handlers

import (
	"net/http"
)

func handleHealth(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]string{statusField: "ok", "version": d.Version})
	}
}
