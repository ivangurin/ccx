package tui_test

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/ivangurin/ccx/internal/pkg/tui"
	"github.com/stretchr/testify/require"
)

// providersStage — the first menu level every session test starts from.
var providersStage = tui.Stage{Header: "providers", Items: []tui.Item{{Label: "p1"}, {Label: "p2"}}}

// nextTwoLevels — a two-level builder: two items, then two items, then done.
func nextTwoLevels(chosen []int) (tui.Stage, bool) {
	switch len(chosen) {
	case 0:
		return providersStage, true
	case 1:
		return tui.Stage{Header: "models", Items: []tui.Item{{Label: "m1"}, {Label: "m2"}}}, true
	default:
		return tui.Stage{}, false
	}
}

// A Cyrillic keystroke arrives as several bytes in one Read; appending only
// the first byte corrupted the query into invalid UTF-8 mojibake that never
// matched anything. The whole chunk is the rune.
func TestHandleKey_MultiByteRuneAppendedWhole(t *testing.T) {
	s := tui.NewSessionForTest(&bytes.Buffer{})
	tui.PushForTest(s, providersStage)

	done, _, _ := tui.HandleKeyForTest(s, []byte("ф"), nil)

	require.False(t, done)
	require.Equal(t, "ф", tui.TopQueryForTest(s))
	require.True(t, utf8.ValidString(tui.TopQueryForTest(s)))
}

// Backspace must drop the whole last rune, not one byte — a byte-sized pop
// leaves half a rune behind and the same mojibake match failure.
func TestHandleKey_BackspaceRemovesWholeRune(t *testing.T) {
	s := tui.NewSessionForTest(&bytes.Buffer{})
	tui.PushForTest(s, providersStage)
	tui.SetTopQueryForTest(s, "пр")

	done, _, _ := tui.HandleKeyForTest(s, []byte{127}, nil)

	require.False(t, done)
	require.Equal(t, "п", tui.TopQueryForTest(s))
}

func TestSession_EnterDescendsInPlace(t *testing.T) {
	var buf bytes.Buffer
	s := tui.NewSessionForTest(&buf)
	tui.PushForTest(s, providersStage)
	tui.RenderForTest(s)
	first := buf.Len()

	done, path, err := tui.HandleKeyForTest(s, []byte("\r"), nextTwoLevels)
	require.NoError(t, err)
	require.False(t, done)
	require.Nil(t, path)
	tui.RenderForTest(s)

	second := buf.String()[first:]
	// the model frame replaces the provider frame: the cursor climbs the
	// whole previous frame (header + 2 items + footer = 4 lines) and erases
	require.True(t, strings.HasPrefix(second, "\r\x1b[3A\x1b[J"),
		"model list must replace the provider list in place: %q", second)
	require.Contains(t, second, "models")
	require.Contains(t, second, "Esc back", "deeper level must hint back, not cancel")
	require.Len(t, tui.LevelsForTest(s), 2)
}

func TestSession_EscReturnsToProviders(t *testing.T) {
	var buf bytes.Buffer
	s := tui.NewSessionForTest(&buf)
	tui.PushForTest(s, providersStage)
	tui.RenderForTest(s)
	_, _, err := tui.HandleKeyForTest(s, []byte("\r"), nextTwoLevels) // descend
	require.NoError(t, err)
	tui.RenderForTest(s)

	done, path, err := tui.HandleKeyForTest(s, []byte{27}, nextTwoLevels)
	require.NoError(t, err)
	require.False(t, done)
	require.Nil(t, path)
	require.Len(t, tui.LevelsForTest(s), 1)
	require.Equal(t, "providers", tui.TopHeaderForTest(s))

	tui.RenderForTest(s)
	out := buf.String()
	require.Contains(t, out, "Esc cancel", "the first level must hint cancel again")
	// the provider cursor survived the round trip
	require.Equal(t, 0, tui.TopCursorForTest(s))
}

func TestSession_EscOnFirstLevelCancels(t *testing.T) {
	var buf bytes.Buffer
	s := tui.NewSessionForTest(&buf)
	tui.PushForTest(s, providersStage)
	tui.RenderForTest(s)

	done, path, err := tui.HandleKeyForTest(s, []byte{27}, nextTwoLevels)
	require.True(t, done)
	require.ErrorIs(t, err, tui.ErrCancelledForTest)
	require.Nil(t, path)
	require.Contains(t, buf.String(), "\r\n")
}

func TestSession_EnterOnLastLevelReturnsPath(t *testing.T) {
	var buf bytes.Buffer
	s := tui.NewSessionForTest(&buf)
	tui.PushForTest(s, providersStage)
	tui.SetTopCursorForTest(s, 1) // p2

	done, _, err := tui.HandleKeyForTest(s, []byte("\r"), nextTwoLevels)
	require.NoError(t, err)
	require.False(t, done)
	tui.SetTopCursorForTest(s, 1) // m2

	done, path, err := tui.HandleKeyForTest(s, []byte("\r"), nextTwoLevels)
	require.True(t, done)
	require.NoError(t, err)
	require.Equal(t, []int{1, 1}, path)
}

func TestSession_FilterSurvivesReturn(t *testing.T) {
	var buf bytes.Buffer
	s := tui.NewSessionForTest(&buf)
	tui.PushForTest(s, providersStage)
	// type "p2" on the provider level, descend, come back
	_, _, err := tui.HandleKeyForTest(s, []byte("p"), nextTwoLevels)
	require.NoError(t, err)
	_, _, err = tui.HandleKeyForTest(s, []byte("2"), nextTwoLevels)
	require.NoError(t, err)
	require.Equal(t, "p2", tui.TopQueryForTest(s))
	_, _, err = tui.HandleKeyForTest(s, []byte("\r"), nextTwoLevels)
	require.NoError(t, err)
	_, _, err = tui.HandleKeyForTest(s, []byte{27}, nextTwoLevels)
	require.NoError(t, err)

	require.Equal(t, "p2", tui.TopQueryForTest(s), "the filter must survive the round trip")
}
