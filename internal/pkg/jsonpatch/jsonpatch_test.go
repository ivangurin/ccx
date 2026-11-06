package jsonpatch_test

import (
	"testing"

	"github.com/ivangurin/ccms/internal/pkg/jsonpatch"
	"github.com/stretchr/testify/require"
)

// The patcher's contract: everything outside the edited entry survives
// byte-for-byte — order, indent, blank lines, trailing newline.

func envPath() []string { return []string{"env"} }

func TestSet_ReplacesValueInPlace(t *testing.T) {
	in := `{
  "permissions": {"allow": ["Bash"]},
  "env": {
    "API_TIMEOUT_MS": "3000000",
    "IDP_TOKEN": "user-token"
  },
  "model": "old-model"
}
`
	want := `{
  "permissions": {"allow": ["Bash"]},
  "env": {
    "API_TIMEOUT_MS": "777",
    "IDP_TOKEN": "user-token"
  },
  "model": "old-model"
}
`

	out, err := jsonpatch.Set([]byte(in), envPath(), "API_TIMEOUT_MS", []byte(`"777"`))
	require.NoError(t, err)
	require.Equal(t, want, string(out))
}

func TestSet_ReplacesNonStringValuesInPlace(t *testing.T) {
	in := `{
  "model": "old",
  "availableModels": ["a", "b"],
  "enforceAvailableModels": false
}
`
	want := `{
  "model": "new",
  "availableModels": ["new"],
  "enforceAvailableModels": true
}
`

	out, err := jsonpatch.Set([]byte(in), nil, "model", []byte(`"new"`))
	require.NoError(t, err)
	out, err = jsonpatch.Set(out, nil, "availableModels", []byte(`["new"]`))
	require.NoError(t, err)
	out, err = jsonpatch.Set(out, nil, "enforceAvailableModels", []byte(`true`))
	require.NoError(t, err)
	require.Equal(t, want, string(out))
}

func TestSet_AppendsNewKeyAtEndOfPrettyObject(t *testing.T) {
	in := `{
  "a": 1,
  "env": {
    "A": "1",
    "B": "2"
  }
}
`
	want := `{
  "a": 1,
  "env": {
    "A": "1",
    "B": "2",
    "C": "3"
  }
}
`

	out, err := jsonpatch.Set([]byte(in), envPath(), "C", []byte(`"3"`))
	require.NoError(t, err)
	require.Equal(t, want, string(out))
}

func TestSet_AppendsTopLevelKeyAtFileEnd(t *testing.T) {
	in := `{
  "env": {
    "A": "1"
  }
}
`
	want := `{
  "env": {
    "A": "1"
  },
  "model": "m"
}
`

	out, err := jsonpatch.Set([]byte(in), nil, "model", []byte(`"m"`))
	require.NoError(t, err)
	require.Equal(t, want, string(out))
}

func TestSet_CompactFileStaysCompact(t *testing.T) {
	in := `{"env":{"A":"1","B":"2"},"model":"x"}`
	want := `{"env":{"A":"1","B":"2","C":"3"},"model":"x"}`

	out, err := jsonpatch.Set([]byte(in), envPath(), "C", []byte(`"3"`))
	require.NoError(t, err)
	require.Equal(t, want, string(out))

	out, err = jsonpatch.Set([]byte(in), nil, "model", []byte(`"y"`))
	require.NoError(t, err)
	require.JSONEq(t, `{"env":{"A":"1","B":"2"},"model":"y"}`, string(out))
}

func TestSet_FourSpaceIndent(t *testing.T) {
	in := "{\n    \"env\": {\n        \"A\": \"1\"\n    }\n}\n"
	want := "{\n    \"env\": {\n        \"A\": \"1\",\n        \"B\": \"2\"\n    }\n}\n"

	out, err := jsonpatch.Set([]byte(in), envPath(), "B", []byte(`"2"`))
	require.NoError(t, err)
	require.Equal(t, want, string(out))
}

func TestSet_EmptyObject(t *testing.T) {
	// compact empty object — the pair lands inside compactly
	out, err := jsonpatch.Set([]byte(`{"env":{},"a":1}`), envPath(), "K", []byte(`"v"`))
	require.NoError(t, err)
	require.JSONEq(t, `{"env":{"K":"v"},"a":1}`, string(out))

	// pretty empty object on its own lines — indent is inherited from the closer
	in := "{\n  \"env\": {\n  },\n  \"a\": 1\n}\n"
	want := "{\n  \"env\": {\n    \"K\": \"v\"\n  },\n  \"a\": 1\n}\n"
	out, err = jsonpatch.Set([]byte(in), envPath(), "K", []byte(`"v"`))
	require.NoError(t, err)
	require.Equal(t, want, string(out))

	// compact empty braces inside a pretty file — the block opens up in the file's style
	in = "{\n  \"env\": {},\n  \"a\": 1\n}\n"
	want = "{\n  \"env\": {\n    \"K\": \"v\"\n  },\n  \"a\": 1\n}\n"
	out, err = jsonpatch.Set([]byte(in), envPath(), "K", []byte(`"v"`))
	require.NoError(t, err)
	require.Equal(t, want, string(out))
}

func TestSet_EscapedKeyAndValue(t *testing.T) {
	in := `{"env":{"A":"1"}}`
	want := `{"env":{"A":"1","we\"ird":"va\"l"}}`

	out, err := jsonpatch.Set([]byte(in), envPath(), `we"ird`, []byte(`"va\"l"`))
	require.NoError(t, err)
	require.Equal(t, want, string(out))
}

func TestSet_MissingPathIsError(t *testing.T) {
	_, err := jsonpatch.Set([]byte(`{"a":1}`), envPath(), "K", []byte(`"v"`))
	require.ErrorContains(t, err, `"env" not found`)
}

func TestDelete_MiddleEntry(t *testing.T) {
	in := `{
  "env": {
    "A": "1",
    "STALE": "x",
    "B": "2"
  }
}
`
	want := `{
  "env": {
    "A": "1",
    "B": "2"
  }
}
`

	out, err := jsonpatch.Delete([]byte(in), envPath(), "STALE")
	require.NoError(t, err)
	require.Equal(t, want, string(out))
}

func TestDelete_LastEntry(t *testing.T) {
	in := `{
  "env": {
    "A": "1",
    "STALE": "x"
  }
}
`
	want := `{
  "env": {
    "A": "1"
  }
}
`

	out, err := jsonpatch.Delete([]byte(in), envPath(), "STALE")
	require.NoError(t, err)
	require.Equal(t, want, string(out))
}

func TestDelete_FirstEntry(t *testing.T) {
	in := `{
  "env": {
    "STALE": "x",
    "B": "2"
  }
}
`
	want := `{
  "env": {
    "B": "2"
  }
}
`

	out, err := jsonpatch.Delete([]byte(in), envPath(), "STALE")
	require.NoError(t, err)
	require.Equal(t, want, string(out))
}

func TestDelete_OnlyEntry(t *testing.T) {
	in := "{\n  \"env\": {\n    \"STALE\": \"x\"\n  }\n}\n"
	want := "{\n  \"env\": {\n  }\n}\n"

	out, err := jsonpatch.Delete([]byte(in), envPath(), "STALE")
	require.NoError(t, err)
	require.Equal(t, want, string(out))
}

func TestDelete_Compact(t *testing.T) {
	in := `{"env":{"A":"1","STALE":"x","B":"2"}}`
	want := `{"env":{"A":"1","B":"2"}}`

	out, err := jsonpatch.Delete([]byte(in), envPath(), "STALE")
	require.NoError(t, err)
	require.Equal(t, want, string(out))

	in = `{"env":{"A":"1","STALE":"x"}}`
	want = `{"env":{"A":"1"}}`
	out, err = jsonpatch.Delete([]byte(in), envPath(), "STALE")
	require.NoError(t, err)
	require.Equal(t, want, string(out))
}

func TestDelete_MissingKeyIsNoOp(t *testing.T) {
	in := "{\n  \"env\": {\n    \"A\": \"1\"\n  }\n}\n"

	out, err := jsonpatch.Delete([]byte(in), envPath(), "NOPE")
	require.NoError(t, err)
	require.Equal(t, in, string(out))
}

func TestDelete_ThenSetKeepsStyle(t *testing.T) {
	// the real switch flow: stale keys leave, fresh keys arrive at the end
	in := `{
  "env": {
    "API_TIMEOUT_MS": "3000000",
    "IDP_TOKEN": "user-token",
    "ANTHROPIC_STALE": "old"
  }
}
`
	want := `{
  "env": {
    "API_TIMEOUT_MS": "3000000",
    "IDP_TOKEN": "user-token",
    "ANTHROPIC_BASE_URL": "http://127.0.0.1:4567",
    "ANTHROPIC_MODEL": "m1"
  }
}
`

	out, err := jsonpatch.Delete([]byte(in), envPath(), "ANTHROPIC_STALE")
	require.NoError(t, err)
	out, err = jsonpatch.Set(out, envPath(), "ANTHROPIC_BASE_URL", []byte(`"http://127.0.0.1:4567"`))
	require.NoError(t, err)
	out, err = jsonpatch.Set(out, envPath(), "ANTHROPIC_MODEL", []byte(`"m1"`))
	require.NoError(t, err)
	require.Equal(t, want, string(out))
}

func TestErrors(t *testing.T) {
	_, err := jsonpatch.Set([]byte(`{"a":1`), nil, "b", []byte(`2`))
	require.Error(t, err)

	_, err = jsonpatch.Delete([]byte(`[1,2]`), nil, "a")
	require.Error(t, err)

	// the path key exists but is not an object
	_, err = jsonpatch.Set([]byte(`{"env":"oops"}`), envPath(), "K", []byte(`"v"`))
	require.ErrorContains(t, err, "not an object")
}
