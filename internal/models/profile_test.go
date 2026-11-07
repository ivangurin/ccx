package models_test

import (
	"encoding/json"
	"testing"

	"github.com/ivangurin/ccx/internal/models"
	"github.com/stretchr/testify/require"
)

func TestResolveModel(t *testing.T) {
	p := &models.Profile{Models: []models.Model{
		{ID: "glm-5.3[1m]", Name: "glm-5.3", Default: true},
		{ID: "glm-flash"},
	}}

	id, ok := p.ResolveModel("")
	require.True(t, ok)
	require.Equal(t, "glm-5.3[1m]", id, "empty input resolves to the default")

	id, ok = p.ResolveModel("glm-5.3")
	require.True(t, ok)
	require.Equal(t, "glm-5.3[1m]", id, "the alias maps to the real id")

	_, ok = p.ResolveModel("nope")
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
		AuthFile:       "~/.codex/auth.json",
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
