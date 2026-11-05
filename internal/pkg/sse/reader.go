// Package sse — чтение SSE-стримов апстримов и запись Anthropic-SSE клиентам.
package sse

import (
	"bufio"
	"io"
	"strings"
)

// Event — одно SSE-событие (имя по умолчанию "message" по спецификации).
type Event struct {
	Name string
	Data string
	Done bool // получен [DONE]-маркер (OpenAI-конвенция)
}

// Reader — строчный SSE-парсер: накапливает data:-строки до пустой строки.
type Reader struct {
	sc *bufio.Scanner
}

// NewReader строит парсер над потоком.
func NewReader(r io.Reader) *Reader {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024) // большие SSE-дельты

	return &Reader{sc: sc}
}

// Next возвращает следующее событие; io.EOF — конец потока.
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

// parseLine обрабатывает строку SSE; emit=true — событие закончилось.
func parseLine(line string, name *string, data *[]string) (Event, bool) {
	switch {
	case line == "":
		if len(*data) == 0 && *name == "" {
			return Event{}, false // пустые строки между событиями
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
		return Event{}, false // комментарий/heartbeat
	case strings.HasPrefix(line, "event:"):
		*name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))

		return Event{}, false
	case strings.HasPrefix(line, "data:"):
		*data = append(*data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))

		return Event{}, false
	default:
		return Event{}, false // неизвестные поля (id, retry) игнорируем
	}
}
