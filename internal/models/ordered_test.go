package models_test

import (
	"encoding/json"
	"testing"

	"github.com/ivangurin/ccms/internal/models"
	"github.com/stretchr/testify/require"
)

// UnmarshalJSON keeps the file order of the keys (loadProviders relies on it:
// ccms list and the menu show profiles exactly as written in providers.json).
func TestOrderedMap_UnmarshalJSON_KeepsFileOrder(t *testing.T) {
	raw := []byte(`{"zai": {"name": "Z"}, "x5": {"name": "X"}, "openai": {"name": "O"}}`)

	var m models.OrderedMap[*models.Profile]
	require.NoError(t, json.Unmarshal(raw, &m))

	require.Equal(t, []string{"zai", "x5", "openai"}, m.Keys)
	require.Equal(t, "Z", m.Get("zai").Name)
	require.Equal(t, "X", m.Get("x5").Name)
	require.Equal(t, "O", m.Get("openai").Name)
}

func TestOrderedMap_UnmarshalJSON_RejectsNonObject(t *testing.T) {
	var m models.OrderedMap[*models.Profile]
	require.Error(t, json.Unmarshal([]byte(`[1,2]`), &m))
}
