package tui

import (
	"fmt"
	"unicode/utf8"
)

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
		s.backspace()
	case key[0] >= 32:
		s.appendRune(key)
	default:
		// an unhandled control byte — ignore
	}

	return false, nil, nil
}

// backspace — BS/DEL: drop the whole last rune, not a byte: multi-byte UTF-8
// input (Cyrillic labels) must not decay into invalid halves.
func (s *session) backspace() {
	if top := s.top(); top.query != "" {
		_, size := utf8.DecodeLastRuneInString(top.query)
		top.query = top.query[:len(top.query)-size]
	}
}

// appendRune — the whole read chunk is printable runes (escape sequences
// were matched in handleKey); appending a single byte corrupted multi-byte
// UTF-8 input — the filter then never matched the typed label.
func (s *session) appendRune(key []byte) {
	if utf8.Valid(key) {
		s.top().query += string(key)
	}
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
