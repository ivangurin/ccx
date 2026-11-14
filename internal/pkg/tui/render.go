package tui

import (
	"io"
	"strings"
)

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
