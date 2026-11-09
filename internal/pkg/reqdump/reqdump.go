// Package reqdump — request dumping for live diagnostics. Some bugs
// only reproduce with a real client's request (Claude Code's full payload),
// which synthetic tests cannot replicate. Dumping is enabled ONLY by the
// daemon's --debug flag: normal runs must never write request bodies
// anywhere (the "bodies only on error" convention). CCX_DUMP_DIR no longer
// turns dumping on — it only overrides the dump directory (default:
// <CCX_HOME>/logs). All dumps of one daemon launch append to a single JSONL
// file <dir>/dump-<launch time>.log; each line is
// {"ts":<unixmilli>,"seq":N,"kind":"...","body":<verbatim json or string>}.
// Bodies hold no secrets — credentials travel in headers — but 0600 keeps
// conversations private. State is package-level on purpose (usagelog.Default
// precedent): a debug switch must not grow DI signatures across
// handlers/translators.
package reqdump

import (
	"os"
	"sync"
	"time"
)

// Options configures the dump sink (set once at daemon startup).
type Options struct {
	// Enabled turns dumping on (the daemon's --debug flag).
	Enabled bool
	// Dir is the default dump directory (config.LogDir()); the CCX_DUMP_DIR
	// env var overrides it.
	Dir string
	// Now is the launch-timestamp source; nil means time.Now (test seam).
	Now func() time.Time
}

var (
	mu       sync.Mutex
	enabled  bool
	dir      string
	launchTS string
	file     *os.File
)

// Configure installs the package-level sink; call once at daemon startup,
// before any request can be served. Without Enabled it installs a disabled
// sink: Enabled() returns false and Write is a no-op — the env var alone
// must never enable dumping.
func Configure(opts Options) {
	now := opts.Now
	if now == nil {
		now = time.Now
	}

	mu.Lock()
	defer mu.Unlock()
	enabled = opts.Enabled
	dir = os.Getenv("CCX_DUMP_DIR")
	if dir == "" {
		dir = opts.Dir
	}
	launchTS = now().Format("20060102-150405")
	file = nil
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
	dir = ""
	launchTS = ""
	file = nil
}

// dumpFilePrefix keeps the dump file name disjoint from the daemon log
// names (daemon-*.log): the default dump directory IS the log directory,
// and both file kinds must never collide on one name.
const dumpFilePrefix = "dump-"

// LaunchTimeForTest — the fixed launch timestamp external test packages can
// build dump file names with after configuring with the matching Now func.
// Mirrors the format Configure applies ("20060102-150405").
const LaunchTimeForTest = "20260102-150405"
