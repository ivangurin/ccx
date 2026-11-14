// Package cli — cobra command tree for ccx. Every command is a thin cobra
// shell over a runXxx(args, stdout) int body (argv-style args, args[0] is the
// command name): the bodies keep the bash-ccx exit codes and hint texts that
// the ported tests pin, cobra owns parsing, help and shell completion.
package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// Version is set at build time (-X ...cli.Version=...), dev — locally.
var Version = "dev"

// exitError carries a command body's non-zero exit code through cobra's
// error channel; the body already printed its own message, so Execute must
// not print anything for it.
type exitError struct{ code int }

func (e exitError) Error() string { return fmt.Sprintf("exit status %d", e.code) }

// shortAliases — bash-ccx single-dash command aliases (ccx -l == ccx list).
// cobra has no such notion for subcommands, so the first argument is rewritten
// before parsing.
var shortAliases = map[string]string{"-l": "list", "-s": "status", "-e": "edit"}

// Execute runs the command; returns the process exit code.
func Execute(args []string, stdout io.Writer) int {
	if len(args) > 0 {
		if full, ok := shortAliases[args[0]]; ok {
			args = append([]string{full}, args[1:]...)
		}
	}

	root := newRootCmd(stdout)
	root.SetArgs(args)

	err := root.Execute()
	if err == nil {
		return 0
	}
	if ee, ok := errors.AsType[exitError](err); ok {
		return ee.code
	}
	// cobra-level failure (unknown flag, bad flag value): ccx prints errors
	// to stdout with the "ccx: " prefix like every command body does
	fmt.Fprintf(stdout, "ccx: %v\n", err)

	return 1
}

// runFunc is a command body: argv-style args (args[0] is the command name).
type runFunc func(args []string, stdout io.Writer) int

// bodyCmd wraps a command body into a cobra command. Positional-argument
// counting stays in the body (its messages are part of the pinned behavior),
// so cobra accepts any arguments.
func bodyCmd(stdout io.Writer, use, short string, run runFunc) *cobra.Command {
	name := use
	for i, r := range use {
		if r == ' ' {
			name = use[:i]

			break
		}
	}

	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ArbitraryArgs,
		RunE: func(_ *cobra.Command, args []string) error {
			return toErr(run(append([]string{name}, args...), stdout))
		},
	}
}

func toErr(code int) error {
	if code == 0 {
		return nil
	}

	return exitError{code: code}
}

func newRootCmd(stdout io.Writer) *cobra.Command {
	root := &cobra.Command{
		Use:   "ccx [provider [model]]",
		Short: "ccx — switching LLM providers for Claude Code",
		Long: "ccx — switching LLM providers for Claude Code.\n\n" +
			"Without arguments: interactive menu (provider → model).\n" +
			"`ccx <provider> [model]` is a shortcut for `ccx use <provider> [model]`.",
		Version: Version,
		// an unknown first argument is not an error: it is a provider key
		// (`ccx zai`, `ccx zai glm-5.3` — bash parity)
		Args:          cobra.ArbitraryArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 {
				return toErr(runMenu(nil, stdout))
			}

			return toErr(runUse(append([]string{"use"}, args...), stdout))
		},
	}
	root.SetOut(stdout)
	root.SetErr(stdout)
	root.SetVersionTemplate("ccx {{.Version}}\n")

	use := bodyCmd(stdout, "use <provider> [model]", "switch to a provider (and model)", runUse)
	list := bodyCmd(stdout, "list", "providers and models", runList)
	status := bodyCmd(stdout, "status", "active provider/model, daemon state", runStatus)
	test := bodyCmd(stdout, "test", "probe providers and models (real requests, temp daemons)", runTest)
	// the body parses --provider itself (bash-parity argv handling); cobra
	// flag parsing would strip it before RunE sees the args
	test.DisableFlagParsing = true
	edit := bodyCmd(stdout, "edit", "open providers.json in $EDITOR", runEdit)
	stop := bodyCmd(stdout, "stop", "stop the daemon", runStop)
	restore := bodyCmd(stdout, "restore", "restore the original settings.json from the one-time backup", runRestore)
	auth := bodyCmd(stdout, "auth [provider]", "login flow (copilot/codex); no argument — auth state", runAuth)
	version := bodyCmd(stdout, "version", "binary version", func(_ []string, out io.Writer) int {
		fmt.Fprintf(out, "ccx %s\n", Version)

		return 0
	})

	root.AddCommand(use, list, status, test, newInitCmd(stdout), edit, newServeCmd(), stop, restore, auth, version)

	return root
}
