package reqdump

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

var seq atomic.Int64

// dumpLine — one JSONL record of the dump file. Body embeds a valid JSON
// document verbatim (json.RawMessage); anything else becomes a JSON string.
type dumpLine struct {
	Ts   int64           `json:"ts"`
	Seq  int64           `json:"seq"`
	Kind string          `json:"kind"`
	Body json.RawMessage `json:"body"`
}

// Write appends one body to this launch's dump-<launch time>.log. The file
// handle is opened lazily on the first write and held for the process
// lifetime — dumps are rare (one per request at most), the daemon is
// long-lived, and a held handle under mu keeps seq monotonic without
// per-write open/close churn. Failures are swallowed on purpose: diagnostics
// must never break the request path it observes.
func Write(kind string, body []byte) {
	mu.Lock()
	defer mu.Unlock()
	if !enabled {
		return
	}
	if file == nil {
		_ = os.MkdirAll(dir, 0o750)
		path := filepath.Join(dir, dumpFilePrefix+launchTS+".log")
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) //nolint:gosec // fixed permissions, ccx-owned path
		if err != nil {
			return
		}
		file = f
	}
	line := dumpLine{
		Ts:   time.Now().UnixMilli(),
		Seq:  seq.Add(1),
		Kind: kind,
		Body: json.RawMessage(body),
	}
	if !json.Valid(body) {
		// not parseable — embed as an escaped string instead of producing
		// a broken JSONL line
		encoded, merr := json.Marshal(string(body))
		if merr != nil {
			return
		}
		line.Body = json.RawMessage(encoded)
	}
	data, merr := json.Marshal(line)
	if merr != nil {
		return
	}
	_, _ = file.Write(append(data, '\n')) //nolint:gosec // generated content, ccx-owned path
}

// seqForTest exposes the sequence counter (tests only).
func seqForTest() int64 {
	return seq.Load()
}
