// envref tests: auth_token/api_key in providers.json may hold a whole-value
// environment reference ($NAME / ${NAME}) instead of the secret itself —
// the secret then lives in the owner's environment, never in providers.json.
// Expansion happens at the points of consumption (switch/test/serve), never
// on load: LoadProviders→SaveProviders (legacy migration) must keep writing
// the reference, not the expanded secret.
package config_test

import (
	"os"
	"testing"

	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/models"
	suite_provider "github.com/ivangurin/ccx/internal/pkg/suite/provider"
	"github.com/stretchr/testify/require"
)

func TestExpandRef(t *testing.T) {
	t.Setenv("CCX_REF_TOKEN", "sk-live")

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"literal passes through", "sk-literal", "sk-literal"},
		{"empty is not a reference", "", ""},
		{"dollar alone is literal", "$", "$"},
		{"$NAME expands", "$CCX_REF_TOKEN", "sk-live"},
		{"${NAME} expands", "${CCX_REF_TOKEN}", "sk-live"},
		{"inline suffix stays literal", "$CCX_REF_TOKEN-x", "$CCX_REF_TOKEN-x"},
		{"inline prefix stays literal", "pre-$CCX_REF_TOKEN", "pre-$CCX_REF_TOKEN"},
		{"unclosed brace stays literal", "${CCX_REF_TOKEN", "${CCX_REF_TOKEN"},
		{"dollar digit is literal", "$1BAD", "$1BAD"},
		{"dollar dash is literal", "$-NO", "$-NO"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := config.ExpandRef(tc.in)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestExpandRef_MissingVarFails(t *testing.T) {
	_, err := config.ExpandRef("$CCX_REF_MISSING")
	require.ErrorContains(t, err, "CCX_REF_MISSING")
	require.ErrorContains(t, err, "empty")
}

func TestExpandRef_EmptyVarValueFails(t *testing.T) {
	t.Setenv("CCX_REF_EMPTY", "")

	_, err := config.ExpandRef("${CCX_REF_EMPTY}")
	require.ErrorContains(t, err, "CCX_REF_EMPTY")
}

// One level only: a variable whose value is itself a reference comes back as
// that literal text — no recursive expansion, no surprises.
func TestExpandRef_NoRecursion(t *testing.T) {
	t.Setenv("CCX_REF_OUTER", "$CCX_REF_INNER")

	got, err := config.ExpandRef("$CCX_REF_OUTER")
	require.NoError(t, err)
	require.Equal(t, "$CCX_REF_INNER", got)
}

func TestExpandCredentials(t *testing.T) {
	t.Setenv("CCX_REF_AUTH", "tok")

	p := &models.Profile{BaseURL: "https://up"}
	p.AuthToken = "$CCX_REF_AUTH"
	require.NoError(t, config.ExpandCredentials(p))
	require.Equal(t, "tok", p.AuthToken)
}

func TestExpandCredentials_LiteralUntouched(t *testing.T) {
	p := &models.Profile{BaseURL: "https://up"}
	p.AuthToken = "tok"
	require.NoError(t, config.ExpandCredentials(p))
	require.Equal(t, "tok", p.AuthToken)
}

func TestExpandCredentials_MissingVarNamesTheField(t *testing.T) {
	p := &models.Profile{}
	p.AuthToken = "$CCX_REF_MISSING"

	err := config.ExpandCredentials(p)
	require.ErrorContains(t, err, "auth_token")
	require.ErrorContains(t, err, "CCX_REF_MISSING")
}

// The removed api_key field is schema history: a raw providers.json still
// carrying it (an un-migrated user file) loads cleanly and expands without
// touching the dead field — the unset $VAR it references must not fail the
// switch.
func TestExpandCredentials_RemovedAPIKeyFieldIgnored(t *testing.T) {
	sp := suite_provider.NewProvider(t)
	require.NoError(t, os.WriteFile(sp.Config.ProvidersPath(), []byte(
		`{"providers": {"zai": {"name": "z.ai", "protocol": "anthropic",
		"base_url": "https://z.example", "auth_token": "$CCX_REF_AUTH",
		"api_key": "$CCX_REF_MISSING"}}}`,
	), 0o600))
	t.Setenv("CCX_REF_AUTH", "tok")

	p, err := config.LoadProviders(sp.Config.ProvidersPath())
	require.NoError(t, err)
	require.NoError(t, config.ExpandCredentials(p.Providers.Get(zaiKey)))
	require.Equal(t, "tok", p.Providers.Get(zaiKey).AuthToken)
}
