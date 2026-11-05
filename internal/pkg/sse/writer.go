package sse

import (
	"encoding/json"
	"net/http"
)

// Writer — запись SSE в http.ResponseWriter с flush после каждого события
// (Claude Code читает стрим по-событийно; без flush клиент висит).
type Writer struct {
	w     http.ResponseWriter
	flush http.Flusher
}

// NewWriter выставляет SSE-заголовки и строит writer.
func NewWriter(w http.ResponseWriter) *Writer {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	flusher, ok := w.(http.Flusher)
	if !ok {
		// http.ResponseWriter без Flusher не может стримить; пишем в никуда
		// не паникуем — ошибка всплывёт на первом Write
		flusher = noopFlusher{}
	}

	return &Writer{w: w, flush: flusher}
}

// Event пишет event+data (data — JSON-сериализуемое значение).
func (sw *Writer) Event(name string, data any) {
	payload, err := json.Marshal(data)
	if err != nil {
		payload = []byte(`{"type":"error","error":{"type":"api_error","message":"sse marshal"}}`)
	}
	_, _ = sw.w.Write([]byte("event: " + name + "\ndata: " + string(payload) + "\n\n"))
	sw.flush.Flush()
}

// Raw пишет уже готовую data-строку (без event-имени — "message").
func (sw *Writer) Raw(data []byte) {
	_, _ = sw.w.Write([]byte("data: " + string(data) + "\n\n"))
	sw.flush.Flush()
}

type noopFlusher struct{}

func (noopFlusher) Flush() {}

// Close — финальный flush (соединение закрывает http-сервер).
func (sw *Writer) Close() {
	sw.flush.Flush()
}
