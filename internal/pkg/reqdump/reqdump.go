// Package reqdump — request dumping for live diagnostics. Some bugs
// only reproduce with a real client's request (Claude Code's full payload),
// which synthetic tests cannot replicate. Dumping is enabled ONLY by the
// daemon's --debug flag: normal runs must never write request bodies
// anywhere (the "bodies only on error" convention). CCX_DUMP_DIR is gone
// along with the separate dump-<launch time>.log file: dumps ride the
// daemon's unified log as kind=reqdump JSONL lines, each carrying the dump
// kind (anthropic-req, codex-sse, …), a sequence number and the verbatim
// body. Bodies hold no secrets — credentials travel in headers. State is
// package-level on purpose (usagelog.Default precedent): a debug switch
// must not grow DI signatures across handlers/translators.
package reqdump

import (
	"log/slog"
	"sync"
)

// Options configures the dump sink (set once at daemon startup).
type Options struct {
	// Enabled turns dumping on (the daemon's --debug flag).
	Enabled bool
	// Logger is the daemon's unified-log logger; dumps are emitted through
	// it (pre-attributed with kind=reqdump by Configure). nil drops every
	// dump — a debug run without a logger writes nothing.
	Logger *slog.Logger
}

var (
	mu      sync.Mutex
	enabled bool
	log     *slog.Logger
)

// Configure installs the package-level sink; call once at daemon startup,
// before any request can be served. Without Enabled it installs a disabled
// sink: Enabled() returns false and Write is a no-op.
func Configure(opts Options) {
	mu.Lock()
	defer mu.Unlock()
	enabled = opts.Enabled
	if opts.Logger != nil {
		log = opts.Logger.With("kind", "reqdump")
	} else {
		log = nil
	}
}

// Enabled — whether this run dumps requests (--debug only).
func Enabled() bool {
	mu.Lock()
	defer mu.Unlock()

	return enabled
}

// ResetForTest restores the zero state (tests only).
func ResetForTest() {
	mu.Lock()
	defer mu.Unlock()
	enabled = false
	log = nil
}
