package cli_test

import (
	"bytes"
	"testing"

	"github.com/ivangurin/ccx/internal/cli"
	"github.com/stretchr/testify/require"
)

func TestExecute_Version(t *testing.T) {
	var out bytes.Buffer

	code := cli.Execute([]string{"version"}, &out)

	require.Equal(t, 0, code)
	require.Equal(t, "ccx dev\n", out.String())
}

func TestExecute_Help(t *testing.T) {
	var out bytes.Buffer

	code := cli.Execute([]string{"help"}, &out)

	require.Equal(t, 0, code)
	for _, want := range []string{"use", "list", "status", "init", "serve", "stop"} {
		require.Contains(t, out.String(), want)
	}
}

func TestExecute_NoArgs_ShowsHelp(t *testing.T) {
	var out bytes.Buffer

	code := cli.Execute(nil, &out)

	require.Equal(t, 0, code)
	require.Contains(t, out.String(), "ccx")
}

func TestExecute_UnknownCommand(t *testing.T) {
	var out bytes.Buffer

	code := cli.Execute([]string{"definitely-not-a-command"}, &out)

	require.NotEqual(t, 0, code)
	require.Contains(t, out.String(), "definitely-not-a-command")
}

func TestExecute_HelpFlag(t *testing.T) {
	var out bytes.Buffer

	code := cli.Execute([]string{"--help"}, &out)

	require.Equal(t, 0, code)
	require.Contains(t, out.String(), "use")
}
