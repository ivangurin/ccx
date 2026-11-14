package tui

import "io"

// Test-only re-exports of the unexported helpers the external tui_test
// package asserts on (escContWindow/readEscContinuation stay unexported: no
// test references them), plus errCancelled — the var holds an error value,
// so the tests compare through this alias.
var (
	EscJoinForTest       = escJoin
	BuildFrameForTest    = buildFrame
	RepaintPrefixForTest = repaintPrefix
	ErrCancelledForTest  = errCancelled
)

// The session struct and the level type are unexported down to their fields —
// an external test package can neither construct the literal nor touch the
// state directly. Every internal the moved tests use gets a wrapper here;
// the wrappers take *session, whose type the callers never name.

// NewSessionForTest builds a session writing frames to out (tests only);
// nil out is fine for tests that never render.
func NewSessionForTest(out io.Writer) *session { return &session{out: out} }

// HandleKeyForTest drives the session key handler (tests only).
func HandleKeyForTest(s *session, key []byte, next NextStage) (bool, []int, error) {
	return s.handleKey(key, next)
}

// PushForTest enters a stage level (tests only).
func PushForTest(s *session, st Stage) { s.push(st) }

// RenderForTest draws the current frame to out (tests only).
func RenderForTest(s *session) { s.render() }

// TopCursorForTest reads the active level's cursor (tests only).
func TopCursorForTest(s *session) int { return s.top().cursor }

// SetTopCursorForTest moves the active level's cursor (tests only).
func SetTopCursorForTest(s *session, v int) { s.top().cursor = v }

// TopQueryForTest reads the active level's filter (tests only).
func TopQueryForTest(s *session) string { return s.top().query }

// SetTopQueryForTest rewrites the active level's filter (tests only).
func SetTopQueryForTest(s *session, q string) { s.top().query = q }

// TopHeaderForTest reads the active level's header (tests only).
func TopHeaderForTest(s *session) string { return s.top().header }

// LevelsForTest exposes the visited level stack (tests only); the callers
// can only len() it — the level element type stays unexported.
func LevelsForTest(s *session) []level { return s.levels }
