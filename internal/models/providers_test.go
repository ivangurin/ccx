package models_test

import (
	"encoding/json"
	"testing"

	"github.com/ivangurin/ccms/internal/models"
	"github.com/stretchr/testify/require"
)

func TestProviders_UnmarshalJSON_ProvidersKey(t *testing.T) {
	var p models.Providers
	err := json.Unmarshal([]byte(`{
		"daemon": {"port": 4567},
		"providers": {
			"x5": {"name": "X5", "protocol": "openai", "base_url": "https://up.example", "auth_token": "t"},
			"zai": {"name": "z.ai", "protocol": "anthropic", "base_url": "https://z.example"}
		}
	}`), &p)

	require.NoError(t, err)
	require.Equal(t, 4567, p.DaemonPort())
	require.Equal(t, []string{"x5", "zai"}, p.Profiles.Keys)
	require.Equal(t, models.ProtocolOpenAI, p.Profiles.Get("x5").Protocol)
	require.Equal(t, "t", p.Profiles.Get("x5").AuthToken)
}

func TestProviders_UnmarshalJSON_LegacyProfilesKey(t *testing.T) {
	// Files from older releases carry the map under "profiles" (+ version,
	// daemon.auth_token); they must keep loading forever.
	var p models.Providers
	err := json.Unmarshal([]byte(`{
		"version": 3,
		"daemon": {"port": 4567, "auth_token": "dead"},
		"profiles": {
			"zai": {"name": "z.ai", "protocol": "anthropic"},
			"x5": {"name": "X5", "protocol": "openai"}
		}
	}`), &p)

	require.NoError(t, err)
	require.Equal(t, 4567, p.DaemonPort())
	require.Equal(t, []string{"zai", "x5"}, p.Profiles.Keys)
	require.Equal(t, models.ProtocolAnthropic, p.Profiles.Get("zai").Protocol)
}

func TestProviders_UnmarshalJSON_ProvidersKeyWins(t *testing.T) {
	// Key presence decides, not emptiness: an explicit empty "providers": {}
	// must not be overridden by a populated legacy "profiles".
	var p models.Providers
	err := json.Unmarshal([]byte(`{
		"providers": {},
		"profiles": {"x5": {"name": "X5", "protocol": "openai"}}
	}`), &p)

	require.NoError(t, err)
	require.Empty(t, p.Profiles.Keys)
}

func TestProviders_UnmarshalJSON_UnusedKeyGarbageIgnored(t *testing.T) {
	// Garbage under the key that lost the selection must not fail the load.
	var p models.Providers
	err := json.Unmarshal([]byte(`{
		"providers": {"zai": {"name": "z.ai", "protocol": "anthropic"}},
		"profiles": "not an object"
	}`), &p)

	require.NoError(t, err)
	require.Equal(t, []string{"zai"}, p.Profiles.Keys)
}

func TestProviders_UnmarshalJSON_DaemonBlockNormalized(t *testing.T) {
	for name, raw := range map[string]string{
		"absent":     `{}`,
		"null":       `{"daemon": null}`,
		"empty":      `{"daemon": {}}`,
		"zero port":  `{"daemon": {"port": 0}}`,
		"dead token": `{"daemon": {"port": 0, "auth_token": "dead"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			var p models.Providers
			require.NoError(t, json.Unmarshal([]byte(raw), &p))
			require.Nil(t, p.Daemon)
			require.Equal(t, 0, p.DaemonPort())
		})
	}

	var p models.Providers
	require.NoError(t, json.Unmarshal([]byte(`{"daemon": {"port": 4567, "auth_token": "dead"}}`), &p))
	require.NotNil(t, p.Daemon)
	require.Equal(t, 4567, p.DaemonPort())
}

func TestProviders_MarshalJSON_NoEmptyDaemonBlock(t *testing.T) {
	p := models.Providers{}
	p.Profiles.Set("x5", &models.Profile{Name: "X5", Protocol: models.ProtocolOpenAI})

	raw, err := json.Marshal(&p)
	require.NoError(t, err)
	require.NotContains(t, string(raw), `"daemon"`)
	require.Contains(t, string(raw), `"providers"`)

	p.Daemon = &models.Daemon{Port: 4567}
	raw, err = json.Marshal(&p)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"daemon":{"port":4567}`)
	require.NotContains(t, string(raw), `"profiles"`)
}
