package router

import (
	"net/http"
)

// writeReadErr — a half-read body cannot be re-delivered; fail loudly rather
// than hand the translator a truncated stream.
func writeReadErr(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"read request body"}}`))
}
