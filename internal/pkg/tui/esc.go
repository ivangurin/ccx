package tui

import (
	"os"
	"time"
)

// escContWindow bounds how long a lone-ESC read waits for a possible
// escape-sequence continuation: slow ptys and SSH split "ESC [ A" across
// reads, and treating the head as a standalone Esc cancels a menu level
// while the "[A" tail lands typed in the filter. Var for tests.
var escContWindow = 25 * time.Millisecond

// escJoin decides how a lone-ESC read and the bytes collected within the
// continuation window combine into keypresses: a CSI head ('[' or 'O')
// belongs to the ESC (the two halves of one split arrow sequence), anything
// else means the ESC was standalone and the follow-up is its own keypress.
func escJoin(rest []byte) [][]byte {
	if len(rest) > 0 && (rest[0] == '[' || rest[0] == 'O') {
		return [][]byte{append([]byte{27}, rest...)}
	}
	keys := [][]byte{{27}}
	if len(rest) > 0 {
		keys = append(keys, rest)
	}

	return keys
}

// readEscContinuation collects the tail of a possibly split escape sequence
// within the continuation window: keep waiting while a CSI head arrived
// without its body (arrows are 3 bytes). A timeout (or a non-pollable tty)
// returns whatever arrived — possibly nothing, which is a real Esc press.
func readEscContinuation(tty *os.File) []byte {
	var out []byte
	deadline := time.Now().Add(escContWindow)
	for {
		wait := time.Until(deadline)
		if wait <= 0 {
			return out
		}
		if err := tty.SetReadDeadline(time.Now().Add(wait)); err != nil {
			return nil // no deadline support: degrade to the old lone-ESC behavior
		}
		buf := make([]byte, 16)
		n, err := tty.Read(buf)
		_ = tty.SetReadDeadline(time.Time{})
		if err != nil || n == 0 {
			return out // window expired (os.ErrDeadlineExceeded) or the tty died
		}
		out = append(out, buf[:n]...)
		if out[0] != '[' && out[0] != 'O' {
			return out // not a CSI tail — the ESC was standalone
		}
		if len(out) >= 3 {
			return out // the sequence body arrived
		}
	}
}
