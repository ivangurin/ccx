// Package sse — reading SSE streams from upstreams and writing Anthropic SSE
// to clients.
package sse

import (
	"bufio"
	"io"
	"strings"
)

// Event — a single SSE event (default name "message" per the spec).
type Event struct {
	Name string
	Data string
	Done bool // [DONE] marker received (OpenAI convention)
}

// Reader — a line-by-line SSE parser: accumulates data: lines up to an empty
// line.
type Reader struct {
	sc *bufio.Scanner
}

// NewReader builds a parser over a stream.
func NewReader(r io.Reader) *Reader {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024) // large SSE deltas

	return &Reader{sc: sc}
}

// Next returns the next event; io.EOF — end of stream.
func (r *Reader) Next() (Event, error) {
	var (
		name string
		data []string
	)
	for r.sc.Scan() {
		line := strings.TrimRight(r.sc.Text(), "\r")
		if ev, emit := parseLine(line, &name, &data); emit {
			return ev, nil
		}
	}
	if err := r.sc.Err(); err != nil {
		return Event{}, err
	}
	if len(data) > 0 || name != "" {
		if name == "" {
			name = "message"
		}

		return Event{Name: name, Data: strings.Join(data, "\n")}, nil
	}

	return Event{}, io.EOF
}

// parseLine handles one SSE line; emit=true — the event has ended.
func parseLine(line string, name *string, data *[]string) (Event, bool) {
	switch {
	case line == "":
		if len(*data) == 0 && *name == "" {
			return Event{}, false // empty lines between events
		}
		joined := strings.Join(*data, "\n")
		eventName := *name
		*data, *name = nil, ""
		if joined == "[DONE]" {
			return Event{Done: true}, true
		}
		if eventName == "" {
			eventName = "message"
		}

		return Event{Name: eventName, Data: joined}, true
	case strings.HasPrefix(line, ":"):
		return Event{}, false // comment/heartbeat
	case strings.HasPrefix(line, "event:"):
		*name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))

		return Event{}, false
	case strings.HasPrefix(line, "data:"):
		*data = append(*data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))

		return Event{}, false
	default:
		return Event{}, false // unknown fields (id, retry) are ignored
	}
}
