// Package reqdump — opt-in request dumping for live diagnostics. Some bugs
// only reproduce with a real client's request (Claude Code's full payload),
// which synthetic tests cannot replicate; CCX_DUMP_DIR captures those bodies
// verbatim. Off unless the env var is set: normal runs must not write request
// bodies anywhere (the "bodies only on error" convention). Bodies hold no
// secrets — credentials travel in headers — but 0600 keeps conversations
// private. Env is read here directly instead of config.Config on purpose:
// a debug switch must not grow DI signatures across handlers/translators.
package reqdump

import (
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"
)

var seq atomic.Int64

// Enabled — whether CCX_DUMP_DIR is set (dump calls below become no-ops).
func Enabled() bool {
	return os.Getenv("CCX_DUMP_DIR") != ""
}

// Write saves one request/response body as <unixmilli>-<seq>-<kind>.json.
// Failures are swallowed on purpose: diagnostics must never break the request
// path it observes.
func Write(kind string, body []byte) {
	dir := os.Getenv("CCX_DUMP_DIR")
	if dir == "" {
		return
	}
	// created lazily so a fresh dir works without a manual mkdir -p
	if err := os.MkdirAll(dir, 0o700); err != nil { //nolint:gosec // operator-set CCX_DUMP_DIR, never request data
		return
	}
	name := strconv.FormatInt(time.Now().UnixMilli(), 10) + "-" +
		strconv.FormatInt(seq.Add(1), 10) + "-" + kind + ".json"
	_ = os.WriteFile(filepath.Join(dir, name), body, 0o600) //nolint:gosec // operator-set dir, generated name
}
