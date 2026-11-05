package tui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildFrame_CountsLinesAndMarksCursor(t *testing.T) {
	items := []Item{{Label: "a"}, {Label: "b"}, {Label: "c"}}

	frame, lines := buildFrame("Header", items, "", 1, false)
	// header + 3 items + footer
	require.Equal(t, 5, lines)
	require.Contains(t, frame, "Header\r\n")
	require.Contains(t, frame, "> b\r\n")
	require.NotContains(t, frame, "> a")
	// the last line has no newline: the cursor stays on the footer
	require.True(t, strings.HasSuffix(frame, "cancel\r"), frame)
}

func TestBuildFrame_FilterLineWhenQuery(t *testing.T) {
	frame, lines := buildFrame("h", []Item{{Label: "a"}}, "q", 0, false)
	// header + filter + item + footer
	require.Equal(t, 4, lines)
	require.Contains(t, frame, "filter: q\r\n")
}

func TestBuildFrame_EscHintByLevel(t *testing.T) {
	// the first level cancels, the deeper ones go back
	top := buildFrameTail(t, false)
	require.Equal(t, "up/down select - Enter confirm - Esc cancel\r", top)
	deeper := buildFrameTail(t, true)
	require.Equal(t, "up/down select - Enter confirm - Esc back\r", deeper)
}

func buildFrameTail(t *testing.T, back bool) string {
	t.Helper()
	frame, _ := buildFrame("h", []Item{{Label: "a"}}, "", 0, back)

	return frame[strings.LastIndex(frame, "up/down"):]
}

func TestRepaintPrefix(t *testing.T) {
	// first frame — nothing to clear
	require.Empty(t, repaintPrefix(0))
	// 4 lines of the previous frame: up 3 + clear everything below
	require.Equal(t, "\r\x1b[3A\x1b[J", repaintPrefix(4))
}

func TestRender_RedrawsInPlace(t *testing.T) {
	var buf bytes.Buffer
	s := &session{out: &buf}
	s.push(Stage{Header: "h", Items: []Item{{Label: "a"}, {Label: "b"}}})

	s.render()
	first := buf.String()
	require.NotContains(t, first, "\x1b[", "first frame is drawn without escape clearing")

	s.top().cursor = 1
	s.render()
	second := buf.String()[len(first):]
	// header + 2 items + footer = 4 lines: cursor moved up 3 and screen cleared
	require.True(t, strings.HasPrefix(second, "\r\x1b[3A\x1b[J"), "second frame must overwrite the first in place: %q", second)
	require.Contains(t, second, "> b")
}
