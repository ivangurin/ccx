package billing_test

import (
	"testing"

	"github.com/ivangurin/ccx/internal/translators/billing"
	"github.com/stretchr/testify/require"
)

func TestStripLeadingLine(t *testing.T) {
	in := "x-anthropic-billing-header: {\"tier\":\"free\"}\nYou are Claude Code."
	require.Equal(t, "You are Claude Code.", billing.Strip(in))
}

func TestStripIsCaseInsensitive(t *testing.T) {
	in := "X-Anthropic-Billing-Header: tier=pro\nrest of the prompt"
	require.Equal(t, "rest of the prompt", billing.Strip(in))
}

// The marker is Claude Code's local accounting line and only ever leads the
// system prompt; mid-text occurrences are the user's own text and stay.
func TestStripOnlyLeading(t *testing.T) {
	in := "Some text\nx-anthropic-billing-header: x\nmore"
	require.Equal(t, in, billing.Strip(in))
}

func TestStripNoMarker(t *testing.T) {
	in := "You are Claude Code."
	require.Equal(t, in, billing.Strip(in))
}

func TestStripMarkerOnly(t *testing.T) {
	require.Empty(t, billing.Strip("x-anthropic-billing-header: x"))
	// no trailing newline either
	require.Empty(t, billing.Strip("x-anthropic-billing-header: x\n"))
}
