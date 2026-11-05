package sse_test

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ivangurin/ccx/internal/pkg/sse"
	"github.com/stretchr/testify/require"
)

func readAll(t *testing.T, r *sse.Reader) []sse.Event {
	t.Helper()

	var events []sse.Event
	for {
		ev, err := r.Next()
		if err == io.EOF {
			return events
		}
		require.NoError(t, err)
		if ev.Done {
			return events
		}
		events = append(events, ev)
	}
}

func TestReader_EventsAndData(t *testing.T) {
	in := "event: message_start\ndata: {\"a\":1}\n\nevent: content_block_delta\ndata: {\"b\":2}\n\n"
	events := readAll(t, sse.NewReader(strings.NewReader(in)))

	require.Len(t, events, 2)
	require.Equal(t, "message_start", events[0].Name)
	require.JSONEq(t, `{"a":1}`, events[0].Data)
	require.Equal(t, "content_block_delta", events[1].Name)
}

func TestReader_DefaultEventName(t *testing.T) {
	// без event: — имя "message" (спецификация SSE)
	events := readAll(t, sse.NewReader(strings.NewReader("data: hi\n\n")))
	require.Len(t, events, 1)
	require.Equal(t, "message", events[0].Name)
	require.Equal(t, "hi", events[0].Data)
}

func TestReader_DoneMarker(t *testing.T) {
	r := sse.NewReader(strings.NewReader("data: chunk\n\ndata: [DONE]\n\n"))
	ev, err := r.Next()
	require.NoError(t, err)
	require.Equal(t, "chunk", ev.Data)

	ev, err = r.Next()
	require.NoError(t, err)
	require.True(t, ev.Done)
}

func TestReader_MultilineData(t *testing.T) {
	events := readAll(t, sse.NewReader(strings.NewReader("data: l1\ndata: l2\n\n")))
	require.Len(t, events, 1)
	require.Equal(t, "l1\nl2", events[0].Data)
}

func TestReader_CRLF(t *testing.T) {
	events := readAll(t, sse.NewReader(strings.NewReader("event: x\r\ndata: y\r\n\r\n")))
	require.Len(t, events, 1)
	require.Equal(t, "x", events[0].Name)
	require.Equal(t, "y", events[0].Data)
}

func TestReader_CommentsSkipped(t *testing.T) {
	events := readAll(t, sse.NewReader(strings.NewReader(": ping\ndata: y\n\n")))
	require.Len(t, events, 1)
	require.Equal(t, "y", events[0].Data)
}

func TestWriter_Format(t *testing.T) {
	rec := httptest.NewRecorder()

	w := sse.NewWriter(rec)
	w.Event("message_start", map[string]any{"type": "message_start"})
	w.Raw([]byte(`{"x":1}`))
	w.Close()

	out := rec.Body.String()
	require.True(t, strings.HasPrefix(out, "event: message_start\ndata: {\"type\":\"message_start\"}\n\n"))
	require.Contains(t, out, "data: {\"x\":1}\n\n")
	require.Equal(t, "text/event-stream", rec.Header().Get("Content-Type"))
}
