package switcher_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestState_UpdatedFormat(t *testing.T) {
	sw, _, sp := newTestSwitcher(t)

	_, err := sw.Apply(context.Background(), "zai", "")
	require.NoError(t, err)

	st := readState(t, sp.Config.StatePath())
	_, err = time.Parse(time.RFC3339, st.Updated)
	require.NoError(t, err)
}
