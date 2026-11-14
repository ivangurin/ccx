package handlers

import (
	"net/http"
)

func handleModels(rt Routerer) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		profileModels := rt.Models()
		data := make([]map[string]string, 0, len(profileModels))
		for _, m := range profileModels {
			data = append(data, map[string]string{"id": m.ID, "display_name": m.Label})
		}
		writeJSON(w, map[string]any{"data": data})
	}
}
