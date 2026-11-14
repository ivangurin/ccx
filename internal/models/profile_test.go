package models_test

import (
	"encoding/json"
	"testing"

	"github.com/ivangurin/ccx/internal/models"
	"github.com/stretchr/testify/require"
)

func TestResolveModel(t *testing.T) {
	p := &models.Profile{Models: []models.Model{
		{ID: "glm-5.3[1m]", Default: true},
		{ID: "glm-flash"},
	}}

	id, ok := p.ResolveModel("")
	require.True(t, ok)
	require.Equal(t, "glm-5.3[1m]", id, "empty input resolves to the default")

	id, ok = p.ResolveModel("glm-flash")
	require.True(t, ok)
	require.Equal(t, "glm-flash", id, "the id is matched exactly")

	_, ok = p.ResolveModel("glm-5.3")
	require.False(t, ok, "a bare id without the [1m] suffix is not an alias — models match by id only")

	_, ok = p.ResolveModel("nope")
	require.False(t, ok)
}

// ResolveModelEntry returns the whole entry (id + max_context_tokens), not
// just the id — the switcher derives the context-window env from it.
func TestResolveModelEntry_MaxContextTokens(t *testing.T) {
	raw := `{"models": [
		{"id": "big", "max_context_tokens": 200000, "default": true},
		{"id": "std"}
	]}`
	var p models.Profile
	require.NoError(t, json.Unmarshal([]byte(raw), &p))

	m, ok := p.ResolveModelEntry("")
	require.True(t, ok)
	require.Equal(t, "big", m.ID)
	require.Equal(t, 200000, m.MaxContextTokens)

	m, ok = p.ResolveModelEntry("std")
	require.True(t, ok)
	require.Zero(t, m.MaxContextTokens, "absent max_context_tokens is zero, not an error")

	_, ok = p.ResolveModelEntry("nope")
	require.False(t, ok)
}

func TestDefaultModel(t *testing.T) {
	id, ok := (&models.Profile{Models: []models.Model{{ID: "m"}}}).DefaultModel()
	require.False(t, ok)
	require.Empty(t, id)

	id, ok = (&models.Profile{Models: []models.Model{{ID: "a"}, {ID: "b", Default: true}}}).DefaultModel()
	require.True(t, ok)
	require.Equal(t, "b", id)
}

func TestOrderedMap_HasGet(t *testing.T) {
	var m models.OrderedMap[*models.Profile]
	require.False(t, m.Has("x"))
	require.Nil(t, m.Get("x"))

	m.Set("x", &models.Profile{Name: "X"})
	require.True(t, m.Has("x"))
	require.NotNil(t, m.Get("x"))
	require.Equal(t, []string{"x"}, m.Keys)

	// Set with an existing key keeps the position
	m.Set("x", &models.Profile{Name: "X2"})
	require.Equal(t, []string{"x"}, m.Keys)
	require.Equal(t, "X2", m.Get("x").Name)
}

// A flat profile round-trips through JSON: connection fields on the profile
// root, no protocol-named block, no masquerade (it lives in code constants).
func TestProfile_FlatJSONRoundTrip(t *testing.T) {
	in := &models.Profile{
		Name:           "work",
		Protocol:       models.ProtocolCopilot,
		BaseURL:        "https://gw/v1",
		APIKey:         "k",
		MaxTokensField: "max_completion_tokens",
		Headers:        map[string]string{"X-Org": "acme"},
		Env:            map[string]string{"A": "1"},
		Models:         []models.Model{{ID: "m", Default: true}},
	}

	raw, err := json.Marshal(in) //nolint:gosec // test fixture, no real secrets
	require.NoError(t, err)
	require.NotContains(t, string(raw), `"anthropic"`, "no protocol blocks in the flat layout")
	require.NotContains(t, string(raw), `"copilot":{`)
	require.NotContains(t, string(raw), "user_agent", "the masquerade is not schema")

	var out models.Profile
	require.NoError(t, json.Unmarshal(raw, &out))
	require.Equal(t, in, &out)
}
