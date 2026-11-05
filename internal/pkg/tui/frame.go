package tui

import (
	"fmt"
	"strings"
)

// buildFrame — a full menu frame: header, filter line (if set), items,
// footer. back switches the Esc hint: "cancel" on the first level, "back"
// on the deeper ones. The last line has no newline: the cursor stays on the
// footer so the next frame can climb back to the top from here (in-place
// repaint).
func buildFrame(header string, visible []Item, query string, cursor int, back bool) (string, int) {
	var sb strings.Builder
	lines := 0
	fmt.Fprintf(&sb, "%s\r\n", header)
	lines++
	if query != "" {
		fmt.Fprintf(&sb, "  filter: %s\r\n", query)
		lines++
	}
	for i, it := range visible {
		mark := "  "
		if i == cursor {
			mark = "> "
		}
		detail := ""
		if it.Detail != "" {
			detail = " - " + it.Detail
		}
		fmt.Fprintf(&sb, "%s%s%s\r\n", mark, it.Label, detail)
		lines++
	}
	escHint := "cancel"
	if back {
		escHint = "back"
	}
	fmt.Fprintf(&sb, "up/down select - Enter confirm - Esc %s\r", escHint)
	lines++

	return sb.String(), lines
}

// repaintPrefix — preparation for in-place repaint: the cursor sits on the
// last line of the previous frame (prevLines lines in total); we move it up
// to the first line and clear everything below (\x1b[J removes tails when
// the filter narrowed the list). A terminal that sends arrows (\x1b[A) also
// understands these sequences. Limitation: a frame taller than the terminal
// window smears — profile lists are short.
func repaintPrefix(prevLines int) string {
	if prevLines <= 1 {
		return ""
	}

	return fmt.Sprintf("\r\x1b[%dA\x1b[J", prevLines-1)
}
