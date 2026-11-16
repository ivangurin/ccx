package switcher

import "slices"

// PinnedKeysForTest exposes the production pinnedKeys list to the external
// test package — a copy, so tests can never mutate the real var. The tests
// must follow the production list, not restate it (the list moved
// historically; a hand-copied test list drifts silently).
func PinnedKeysForTest() []string {
	return slices.Clone(pinnedKeys)
}
