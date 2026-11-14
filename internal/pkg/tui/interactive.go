package tui

import (
	"bufio"
	"errors"
	"io"
	"os"

	"golang.org/x/term"
)

var (
	errEmptyList   = errors.New("tui: empty list")
	errInterrupted = errors.New("tui: input interrupted")
	errCancelled   = errors.New("tui: cancelled")
)

// level — one visited menu level; cursor and query survive the round trip
// to the deeper level and back.
type level struct {
	header string
	items  []Item
	cursor int
	query  string
	chosen int // item index picked on this level when descending
}

// session — interactive staged selection: the stack of levels (the top one
// is active) plus the shared last-lines counter for in-place repaint —
// every next frame, of any level, climbs back over the previous one.
type session struct {
	out       io.Writer
	levels    []level
	lastLines int
}

// runSession — raw-mode staged loop: render, arrows, filter, Enter/Esc.
func runSession(next NextStage, first Stage, tty *os.File, out io.Writer) ([]int, error) {
	oldState, err := term.MakeRaw(int(tty.Fd()))
	if err != nil {
		return fallbackStaged(next, first, bufio.NewReader(tty), out)
	}
	defer func() { _ = term.Restore(int(tty.Fd()), oldState) }()

	s := &session{out: out}
	s.push(first)
	s.render()

	buf := make([]byte, 16)
	for {
		n, readErr := tty.Read(buf)
		if readErr != nil || n == 0 {
			return nil, errInterrupted
		}
		keys := [][]byte{buf[:n]}
		if n == 1 && buf[0] == 27 {
			keys = escJoin(readEscContinuation(tty))
		}
		var done bool
		var path []int
		var selErr error
		for _, k := range keys {
			done, path, selErr = s.handleKey(k, next)
			if done {
				break
			}
		}
		if done {
			return path, selErr
		}
		s.render()
	}
}

// push — enter a level: the cursor starts at the stage default.
func (s *session) push(st Stage) {
	cursor := st.DefaultIdx
	if cursor < 0 || cursor >= len(st.Items) {
		cursor = 0
	}
	s.levels = append(s.levels, level{header: st.Header, items: st.Items, cursor: cursor, chosen: -1})
}

// top — the active level.
func (s *session) top() *level {
	return &s.levels[len(s.levels)-1]
}
