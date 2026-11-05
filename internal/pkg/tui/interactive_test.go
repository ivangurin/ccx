package tui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// nextTwoLevels — a two-level builder: two items, then two items, then done.
func nextTwoLevels(chosen []int) (Stage, bool) {
	switch len(chosen) {
	case 0:
		return Stage{Header: "providers", Items: []Item{{Label: "p1"}, {Label: "p2"}}}, true
	case 1:
		return Stage{Header: "models", Items: []Item{{Label: "m1"}, {Label: "m2"}}}, true
	default:
		return Stage{}, false
	}
}

func newSession(out *bytes.Buffer) *session {
	s := &session{out: out}
	s.push(Stage{Header: "providers", Items: []Item{{Label: "p1"}, {Label: "p2"}}})

	return s
}

func TestSession_EnterDescendsInPlace(t *testing.T) {
	var buf bytes.Buffer
	s := newSession(&buf)
	s.render()
	first := buf.Len()

	done, path, err := s.handleKey([]byte("\r"), nextTwoLevels)
	require.NoError(t, err)
	require.False(t, done)
	require.Nil(t, path)
	s.render()

	second := buf.String()[first:]
	// the model frame replaces the provider frame: the cursor climbs the
	// whole previous frame (header + 2 items + footer = 4 lines) and erases
	require.True(t, strings.HasPrefix(second, "\r\x1b[3A\x1b[J"),
		"model list must replace the provider list in place: %q", second)
	require.Contains(t, second, "models")
	require.Contains(t, second, "Esc back", "deeper level must hint back, not cancel")
	require.Len(t, s.levels, 2)
}

func TestSession_EscReturnsToProviders(t *testing.T) {
	var buf bytes.Buffer
	s := newSession(&buf)
	s.render()
	_, _, err := s.handleKey([]byte("\r"), nextTwoLevels) // descend
	require.NoError(t, err)
	s.render()

	done, path, err := s.handleKey([]byte{27}, nextTwoLevels)
	require.NoError(t, err)
	require.False(t, done)
	require.Nil(t, path)
	require.Len(t, s.levels, 1)
	require.Equal(t, "providers", s.top().header)

	s.render()
	out := buf.String()
	require.Contains(t, out, "Esc cancel", "the first level must hint cancel again")
	// the provider cursor survived the round trip
	require.Equal(t, 0, s.top().cursor)
}

func TestSession_EscOnFirstLevelCancels(t *testing.T) {
	var buf bytes.Buffer
	s := newSession(&buf)
	s.render()

	done, path, err := s.handleKey([]byte{27}, nextTwoLevels)
	require.True(t, done)
	require.ErrorIs(t, err, errCancelled)
	require.Nil(t, path)
	require.Contains(t, buf.String(), "\r\n")
}

func TestSession_EnterOnLastLevelReturnsPath(t *testing.T) {
	var buf bytes.Buffer
	s := newSession(&buf)
	s.top().cursor = 1 // p2

	done, _, err := s.handleKey([]byte("\r"), nextTwoLevels)
	require.NoError(t, err)
	require.False(t, done)
	s.top().cursor = 1 // m2

	done, path, err := s.handleKey([]byte("\r"), nextTwoLevels)
	require.True(t, done)
	require.NoError(t, err)
	require.Equal(t, []int{1, 1}, path)
}

func TestSession_FilterSurvivesReturn(t *testing.T) {
	var buf bytes.Buffer
	s := newSession(&buf)
	// type "p2" on the provider level, descend, come back
	_, _, err := s.handleKey([]byte("p"), nextTwoLevels)
	require.NoError(t, err)
	_, _, err = s.handleKey([]byte("2"), nextTwoLevels)
	require.NoError(t, err)
	require.Equal(t, "p2", s.top().query)
	_, _, err = s.handleKey([]byte("\r"), nextTwoLevels)
	require.NoError(t, err)
	_, _, err = s.handleKey([]byte{27}, nextTwoLevels)
	require.NoError(t, err)

	require.Equal(t, "p2", s.top().query, "the filter must survive the round trip")
}
