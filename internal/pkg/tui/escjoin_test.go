package tui_test

import (
	"testing"

	"github.com/ivangurin/ccx/internal/pkg/tui"
	"github.com/stretchr/testify/require"
)

// A split arrow (ESC in one read, "[A" in the next) must recombine into ONE
// keypress — the old behavior cancelled a menu level on the ESC half and
// typed "[A" into the filter.
func TestEscJoin_SplitArrowRecombines(t *testing.T) {
	for _, tail := range []string{"[A", "[B", "[C", "[D", "OA"} {
		keys := tui.EscJoinForTest([]byte(tail))
		require.Len(t, keys, 1, "tail %q", tail)
		require.Equal(t, []byte{27, tail[0], tail[1]}, keys[0])
	}
}

// Nothing following within the window: the ESC was a real Esc press.
func TestEscJoin_LoneEscStaysEsc(t *testing.T) {
	require.Equal(t, [][]byte{{27}}, tui.EscJoinForTest(nil))
}

// A non-CSI follow-up (an Alt-style ESC+byte): the ESC acts as Esc, and the
// byte is its own keypress — the pre-split behavior.
func TestEscJoin_NonCSITailSplitsBack(t *testing.T) {
	keys := tui.EscJoinForTest([]byte("x"))
	require.Len(t, keys, 2)
	require.Equal(t, []byte{27}, keys[0])
	require.Equal(t, "x", string(keys[1]))
}

// The combined keypress is exactly what handleKey's arrow branch expects:
// ESC + '[' + 'A' drives the cursor up end-to-end.
func TestEscJoin_CombinedKeyDrivesArrow(t *testing.T) {
	keys := tui.EscJoinForTest([]byte("[A"))
	s := tui.NewSessionForTest(nil)
	tui.PushForTest(s, tui.Stage{Header: "h", Items: []tui.Item{{Label: "a"}, {Label: "b"}}})
	tui.SetTopCursorForTest(s, 1)

	for _, k := range keys {
		done, _, _ := tui.HandleKeyForTest(s, k, func([]int) (tui.Stage, bool) { return tui.Stage{}, false })
		require.False(t, done)
	}

	require.Equal(t, 0, tui.TopCursorForTest(s), "the recombined sequence moved the cursor up")
	require.Empty(t, tui.TopQueryForTest(s), "nothing was typed into the filter")
}
