package config

import (
	"fmt"
	"os"
	"regexp"

	"github.com/ivangurin/ccx/internal/models"
)

// envRef matches a WHOLE-VALUE environment reference: "$NAME" or "${NAME}".
// Inline occurrences ("pre-$NAME", "$NAME-suffix") are deliberately NOT
// references — a partial splice in a credential is a typo waiting to ship a
// half-secret, and the unambiguous rule keeps the config predictable.
var envRef = regexp.MustCompile(`^\$(\{([A-Za-z_][A-Za-z0-9_]*)\}|([A-Za-z_][A-Za-z0-9_]*))$`)

// ExpandRef resolves a whole-value environment reference in v: "$NAME" or
// "${NAME}" becomes the variable's value; anything else is returned as is
// (a literal secret keeps working, migration rewrites nothing). An unset or
// EMPTY variable is an error, not a silent empty credential: the caller is
// about to write this into settings.json / an Authorization header, and a
// blank there fails far away from its cause. One level only — a variable
// whose value is itself a reference comes back as that literal text.
func ExpandRef(v string) (string, error) {
	m := envRef.FindStringSubmatch(v)
	if m == nil {
		return v, nil
	}
	// braced form captures into group 2, bare into group 3 — take whichever matched
	name := m[2]
	if name == "" {
		name = m[3]
	}
	val, ok := os.LookupEnv(name)
	if !ok || val == "" {
		return "", fmt.Errorf("env var %s is empty — set it before switching", name)
	}

	return val, nil
}

// ExpandCredentials expands environment references in the profile's
// credential fields (auth_token, api_key) in place. Called at the points of
// consumption (switcher/tester/router) — never on load: LoadProviders'
// best-effort migration rewrite must keep persisting the reference, not the
// expanded secret. The error names the offending field so the fix points at
// providers.json.
func ExpandCredentials(p *models.Profile) error {
	for _, field := range []struct {
		name string
		ref  *string
	}{
		{"auth_token", &p.AuthToken},
		{"api_key", &p.APIKey},
	} {
		val, err := ExpandRef(*field.ref)
		if err != nil {
			return fmt.Errorf("%s: %w", field.name, err)
		}
		*field.ref = val
	}

	return nil
}
