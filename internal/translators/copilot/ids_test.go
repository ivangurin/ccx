package copilot

import (
	"testing"

	"github.com/stretchr/testify/require"
)

const sessBody = `{"messages":[{"role":"user","content":"do the thing"}]}`

// Deterministic: the same session + same last user text → the same ids, so
// the gateway aggregates client retries into one interaction instead of
// billing each attempt.
func TestQuotaIDs_Deterministic(t *testing.T) {
	r1, i1 := quotaIDs("sess-1", body(t, sessBody))
	r2, i2 := quotaIDs("sess-1", body(t, sessBody))
	require.Equal(t, r1, r2)
	require.Equal(t, i1, i2)
	require.NotEqual(t, r1, i1)
}

// A different last user text is a different request; the interaction (the
// conversation) stays.
func TestQuotaIDs_RequestTracksLastUserText(t *testing.T) {
	r1, i1 := quotaIDs("sess-1", body(t, sessBody))
	r2, i2 := quotaIDs("sess-1", body(t, `{"messages":[{"role":"user","content":"do the OTHER thing"}]}`))
	require.NotEqual(t, r1, r2)
	require.Equal(t, i1, i2)
}

// Different sessions never share an interaction id.
func TestQuotaIDs_SessionScopesInteraction(t *testing.T) {
	_, i1 := quotaIDs("sess-1", body(t, sessBody))
	_, i2 := quotaIDs("sess-2", body(t, sessBody))
	require.NotEqual(t, i1, i2)
}

// The ids are UUID-shaped (8-4-4-4-12).
func TestQuotaIDs_UUIDShaped(t *testing.T) {
	r, i := quotaIDs("sess-1", body(t, sessBody))
	for _, id := range []string{r, i} {
		require.Len(t, id, 36)
		require.Equal(t, byte('-'), id[8])
		require.Equal(t, byte('-'), id[13])
		require.Equal(t, byte('-'), id[18])
		require.Equal(t, byte('-'), id[23])
	}
}

// No session resolvable → random (non-deterministic) ids, still UUID-shaped.
func TestQuotaIDs_NoSessionRandom(t *testing.T) {
	r1, i1 := quotaIDs("", body(t, sessBody))
	r2, i2 := quotaIDs("", body(t, sessBody))
	require.NotEqual(t, r1, r2)
	require.NotEqual(t, i1, i2)
	require.Len(t, r1, 36)
	require.Len(t, i2, 36)
}

// Distinct tool continuations of one session (different calls, no loose
// text — the shape after the merge) MUST get distinct request ids: the
// gateway aggregates by request id, and two different turns sharing one id
// would collapse into a single interaction.
func TestQuotaIDs_DistinctContinuationsDistinctRequestIDs(t *testing.T) {
	turn1 := `{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"bash","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"one"}]}]}`
	turn2 := `{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"t2","name":"read","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"t2","content":"two"}]}]}`

	r1, _ := quotaIDs("sess-1", body(t, turn1))
	r2, _ := quotaIDs("sess-1", body(t, turn2))
	require.NotEqual(t, r1, r2)

	// the same turn retried stays the same id
	r1again, _ := quotaIDs("sess-1", body(t, turn1))
	require.Equal(t, r1, r1again)
}
