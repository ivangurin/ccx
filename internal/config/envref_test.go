// envref tests: auth_token/api_key in providers.json may hold a whole-value
// environment reference ($NAME / ${NAME}) instead of the secret itself —
// the secret then lives in the owner's environment, never in providers.json.
// Expansion happens at the points of consumption (switch/test/serve), never
// on load: LoadProviders→SaveProviders (legacy migration) must keep writing
// the reference, not the expanded secret.
package config_test

import (
	"testing"

	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/models"
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
	t.Setenv("CCX_REF_KEY", "key")

	p := &models.Profile{BaseURL: "https://up"}
	p.AuthToken, p.APIKey = "$CCX_REF_AUTH", "${CCX_REF_KEY}"
	require.NoError(t, config.ExpandCredentials(p))
	require.Equal(t, "tok", p.AuthToken)
	require.Equal(t, "key", p.APIKey)
}

func TestExpandCredentials_LiteralUntouched(t *testing.T) {
	p := &models.Profile{BaseURL: "https://up"}
	p.AuthToken, p.APIKey = "tok", "key"
	require.NoError(t, config.ExpandCredentials(p))
	require.Equal(t, "tok", p.AuthToken)
	require.Equal(t, "key", p.APIKey)
}

func TestExpandCredentials_MissingVarNamesTheField(t *testing.T) {
	p := &models.Profile{}
	p.AuthToken, p.APIKey = "$CCX_REF_MISSING", "$CCX_REF_ALSO_MISSING"

	err := config.ExpandCredentials(p)
	require.ErrorContains(t, err, "auth_token")
	require.ErrorContains(t, err, "CCX_REF_MISSING")
}

func TestExpandCredentials_APIKeyFieldNamed(t *testing.T) {
	t.Setenv("CCX_REF_AUTH", "tok")

	p := &models.Profile{}
	p.AuthToken, p.APIKey = "$CCX_REF_AUTH", "$CCX_REF_MISSING"
	err := config.ExpandCredentials(p)
	require.ErrorContains(t, err, "api_key")
	require.ErrorContains(t, err, "CCX_REF_MISSING")
}
