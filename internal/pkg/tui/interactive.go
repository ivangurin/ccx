package tui

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"

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

// handleKey processes a keypress; done=true — the session is over (path or error).
func (s *session) handleKey(key []byte, next NextStage) (done bool, path []int, selErr error) {
	switch {
	case key[0] == '\r' || key[0] == '\n':
		return s.confirm(next)
	case (key[0] == 27 && len(key) == 1) || key[0] == 3 || key[0] == 4:
		return s.cancel()
	case key[0] == 27 && len(key) >= 3 && (key[1] == '[' || key[1] == 'O'):
		s.arrow(key[2])
	case key[0] == 127 || key[0] == 8:
		if top := s.top(); top.query != "" {
			// drop the whole last rune, not a byte: multi-byte UTF-8 input
			// (Cyrillic labels) must not decay into invalid halves
			_, size := utf8.DecodeLastRuneInString(top.query)
			top.query = top.query[:len(top.query)-size]
		}
	case key[0] >= 32:
		// the whole read chunk is printable runes (escape sequences were
		// matched above); appending a single byte corrupted multi-byte UTF-8
		// input — the filter then never matched the typed label
		if utf8.Valid(key) {
			s.top().query += string(key)
		}
	}

	return false, nil, nil
}

// cancel — Esc/Ctrl-C: one level up; from the first level — cancel the session.
func (s *session) cancel() (bool, []int, error) {
	if len(s.levels) > 1 {
		s.levels = s.levels[:len(s.levels)-1]

		return false, nil, nil
	}
	fmt.Fprint(s.out, "\r\n")

	return true, nil, errCancelled
}

// confirm — Enter: record the choice and descend to the next level, or
// finish when the builder has no next level (ok=false).
func (s *session) confirm(next NextStage) (bool, []int, error) {
	top := s.top()
	visible := Filter(top.items, top.query)
	if len(visible) == 0 {
		return false, nil, nil
	}
	chosenItem := visible[top.cursor]
	idx := -1
	for i, it := range top.items {
		if it == chosenItem {
			idx = i

			break
		}
	}
	if idx < 0 {
		return true, nil, errCancelled
	}
	top.chosen = idx

	path := make([]int, len(s.levels))
	for i, lv := range s.levels {
		path[i] = lv.chosen
	}

	stage, ok := next(path)
	if !ok {
		fmt.Fprint(s.out, "\r\n")

		return true, path, nil
	}
	if len(stage.Items) == 0 {
		fmt.Fprint(s.out, "\r\n")

		return true, nil, errEmptyList
	}
	s.push(stage)

	return false, nil, nil
}

func (s *session) arrow(code byte) {
	top := s.top()
	switch code {
	case 'A':
		if top.cursor > 0 {
			top.cursor--
		}
	case 'B':
		if top.cursor < len(Filter(top.items, top.query))-1 {
			top.cursor++
		}
	default:
		// other escape codes are ignored
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

func (s *session) render() {
	top := s.top()
	visible := Filter(top.items, top.query)
	if top.cursor >= len(visible) {
		top.cursor = len(visible) - 1
	}
	if top.cursor < 0 {
		top.cursor = 0
	}
	var sb strings.Builder
	// in-place repaint: climb up to the first frame line and erase it — the
	// next level (or the level returned to) is drawn over the old one
	sb.WriteString(repaintPrefix(s.lastLines))
	frame, lines := buildFrame(top.header, visible, top.query, top.cursor, len(s.levels) > 1)
	sb.WriteString(frame)
	s.lastLines = lines
	_, _ = io.WriteString(s.out, sb.String())
}
