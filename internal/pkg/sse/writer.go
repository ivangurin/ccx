package sse

import (
	"encoding/json"
	"net/http"
)

// Writer — writes SSE to an http.ResponseWriter with a flush after each
// event (Claude Code reads the stream event by event; without a flush the
// client hangs).
type Writer struct {
	w     http.ResponseWriter
	flush http.Flusher
}

// NewWriter sets the SSE headers and builds the writer.
func NewWriter(w http.ResponseWriter) *Writer {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	flusher, ok := w.(http.Flusher)
	if !ok {
		// an http.ResponseWriter without Flusher cannot stream; we write
		// nowhere and do not panic — the error surfaces on the first Write
		flusher = noopFlusher{}
	}

	return &Writer{w: w, flush: flusher}
}

// Event writes event+data (data is a JSON-serializable value).
func (sw *Writer) Event(name string, data any) {
	payload, err := json.Marshal(data)
	if err != nil {
		payload = []byte(`{"type":"error","error":{"type":"api_error","message":"sse marshal"}}`)
	}
	_, _ = sw.w.Write([]byte("event: " + name + "\ndata: " + string(payload) + "\n\n"))
	sw.flush.Flush()
}

// Raw writes an already-ready data string (no event name — "message").
func (sw *Writer) Raw(data []byte) {
	_, _ = sw.w.Write([]byte("data: " + string(data) + "\n\n"))
	sw.flush.Flush()
}

type noopFlusher struct{}

func (noopFlusher) Flush() {}

// Close — the final flush (the http server closes the connection).
func (sw *Writer) Close() {
	sw.flush.Flush()
}
