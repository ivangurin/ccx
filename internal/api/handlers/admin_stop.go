package handlers

import (
	"net/http"
)

func handleAdminStop(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]string{statusField: "stopping"})
		if d.Stop != nil {
			go d.Stop()
		}
	}
}
