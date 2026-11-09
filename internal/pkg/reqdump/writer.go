package reqdump

import (
	"encoding/json"
	"sync/atomic"
	"time"
)

var seq atomic.Int64

// Write appends one dump record to the configured logger (kind=reqdump).
// The logger is pre-attributed by Configure; the record carries the dump's
// own kind (anthropic-req, codex-sse, …) as dump_kind. The body travels as
// one attribute: a valid JSON document rides verbatim (slog's JSON handler
// embeds a json.RawMessage as a nested object, not a string), an invalid
// body goes in as a plain string the handler escapes — a broken line is
// impossible on either path. Failures are swallowed on purpose: diagnostics
// must never break the request path it observes.
func Write(kind string, body []byte) {
	mu.Lock()
	defer mu.Unlock()
	if !enabled || log == nil {
		return
	}
	var bodyAttr any = string(body)
	if json.Valid(body) {
		bodyAttr = json.RawMessage(body)
	}
	log.Info(
		"reqdump",
		"ts", time.Now().UnixMilli(),
		"seq", seq.Add(1),
		"dump_kind", kind,
		"body", bodyAttr,
	)
}

// seqForTest exposes the sequence counter (tests only).
func seqForTest() int64 {
	return seq.Load()
}
