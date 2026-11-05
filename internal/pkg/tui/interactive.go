package tui

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

var (
	errEmptyList   = errors.New("tui: empty list")
	errInterrupted = errors.New("tui: input interrupted")
	errCancelled   = errors.New("tui: cancelled")
)

// ui \u2014 state of the interactive selection.
type ui struct {
	header    string
	items     []Item
	cursor    int
	query     string
	tty       *os.File
	out       io.Writer
	lastLines int // lines in the previous frame \u2014 needed for in-place repaint
}

// runInteractive \u2014 raw-mode loop: render, arrows, filter, Enter/Esc.
func runInteractive(header string, items []Item, defaultIdx int, tty *os.File, out io.Writer) (int, error) {
	oldState, err := term.MakeRaw(int(tty.Fd()))
	if err != nil {
		return FallbackSelect(items, defaultIdx, bufio.NewReader(tty), out)
	}
	defer func() { _ = term.Restore(int(tty.Fd()), oldState) }()

	u := &ui{header: header, items: items, cursor: defaultIdx, tty: tty, out: out}
	u.render()

	buf := make([]byte, 16)
	for {
		n, readErr := tty.Read(buf)
		if readErr != nil || n == 0 {
			return -1, errInterrupted
		}
		done, idx, selErr := u.handleKey(buf[:n])
		if done {
			return idx, selErr
		}
		u.render()
	}
}

// handleKey processes a keypress; done=true means selection finished.
func (u *ui) handleKey(key []byte) (done bool, idx int, selErr error) {
	switch {
	case key[0] == '\r' || key[0] == '\n':
		return u.confirm()
	case key[0] == 27 && len(key) == 1:
		fmt.Fprint(u.out, "\r\n")

		return true, -1, errCancelled
	case key[0] == 3 || key[0] == 4:
		fmt.Fprint(u.out, "\r\n")

		return true, -1, errCancelled
	case key[0] == 27 && len(key) >= 3 && (key[1] == '[' || key[1] == 'O'):
		u.arrow(key[2])

		return false, 0, nil
	case key[0] == 127 || key[0] == 8:
		if u.query != "" {
			u.query = u.query[:len(u.query)-1]
		}

		return false, 0, nil
	case key[0] >= 32:
		u.query += string(key[0])

		return false, 0, nil
	default:
		return false, 0, nil
	}
}

func (u *ui) arrow(code byte) {
	switch code {
	case 'A':
		if u.cursor > 0 {
			u.cursor--
		}
	case 'B':
		if u.cursor < len(u.visibleItems())-1 {
			u.cursor++
		}
	default:
		// other escape codes are ignored
	}
}

func (u *ui) confirm() (bool, int, error) {
	visible := u.visibleItems()
	if len(visible) == 0 {
		return false, 0, nil
	}
	chosen := visible[u.cursor]
	fmt.Fprint(u.out, "\r\n")
	for i, it := range u.items {
		if it == chosen {
			return true, i, nil
		}
	}

	return true, -1, errCancelled
}

func (u *ui) visibleItems() []Item {
	return Filter(u.items, u.query)
}

func (u *ui) render() {
	visible := u.visibleItems()
	if u.cursor >= len(visible) {
		u.cursor = len(visible) - 1
	}
	if u.cursor < 0 {
		u.cursor = 0
	}
	var sb strings.Builder
	// in-place repaint: climb up to the first frame and erase it
	sb.WriteString(repaintPrefix(u.lastLines))
	frame, lines := buildFrame(u.header, visible, u.query, u.cursor)
	sb.WriteString(frame)
	u.lastLines = lines
	_, _ = io.WriteString(u.out, sb.String())
}
