package handlers

import (
	"encoding/json"
	"net/http"
)

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	data, err := json.Marshal(v)
	if err != nil {
		data = []byte(`{"error":"marshal"}`)
	}
	_, _ = w.Write(data)
	_, _ = w.Write([]byte("\n"))
}
