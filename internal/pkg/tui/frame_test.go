package tui_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ivangurin/ccx/internal/pkg/tui"
	"github.com/stretchr/testify/require"
)

func TestBuildFrame_CountsLinesAndMarksCursor(t *testing.T) {
	items := []tui.Item{{Label: "a"}, {Label: "b"}, {Label: "c"}}

	frame, lines := tui.BuildFrameForTest("Header", items, "", 1, false)
	// header + 3 items + footer
	require.Equal(t, 5, lines)
	require.Contains(t, frame, "Header\r\n")
	require.Contains(t, frame, "> b\r\n")
	require.NotContains(t, frame, "> a")
	// the last line has no newline: the cursor stays on the footer
	require.True(t, strings.HasSuffix(frame, "cancel\r"), frame)
}

func TestBuildFrame_FilterLineWhenQuery(t *testing.T) {
	frame, lines := tui.BuildFrameForTest("h", []tui.Item{{Label: "a"}}, "q", 0, false)
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
	frame, _ := tui.BuildFrameForTest("h", []tui.Item{{Label: "a"}}, "", 0, back)

	return frame[strings.LastIndex(frame, "up/down"):]
}

func TestRepaintPrefix(t *testing.T) {
	// first frame — nothing to clear
	require.Empty(t, tui.RepaintPrefixForTest(0))
	// 4 lines of the previous frame: up 3 + clear everything below
	require.Equal(t, "\r\x1b[3A\x1b[J", tui.RepaintPrefixForTest(4))
}

func TestRender_RedrawsInPlace(t *testing.T) {
	var buf bytes.Buffer
	s := tui.NewSessionForTest(&buf)
	tui.PushForTest(s, tui.Stage{Header: "h", Items: []tui.Item{{Label: "a"}, {Label: "b"}}})

	tui.RenderForTest(s)
	first := buf.String()
	require.NotContains(t, first, "\x1b[", "first frame is drawn without escape clearing")

	tui.SetTopCursorForTest(s, 1)
	tui.RenderForTest(s)
	second := buf.String()[len(first):]
	// header + 2 items + footer = 4 lines: cursor moved up 3 and screen cleared
	require.True(t, strings.HasPrefix(second, "\r\x1b[3A\x1b[J"), "second frame must overwrite the first in place: %q", second)
	require.Contains(t, second, "> b")
}
