// Package cli — stdlib command dispatcher for ccms. Commands are simple verbs
// with 0–2 arguments; cobra is not needed (why: minimal dependencies, commands
// are ported from bash-ccx 1:1 along with their tests).
package cli

import (
	"fmt"
	"io"
	"strings"
)

// Version is set at build time (-X ...cli.Version=...), dev — locally.
var Version = "dev"

const usage = `ccms — switching LLM providers for Claude Code

Usage:
  ccms                    interactive menu: profile → model
  ccms use <profile> [model]
  ccms list               profiles and models
  ccms status             active profile/model, daemon state
  ccms init [--force]     create providers.json (import/migration)
  ccms edit               open providers.json in $EDITOR
  ccms serve              daemon (foreground, debugging)
  ccms stop               the daemon
  ccms auth copilot       GitHub OAuth device flow
  ccms auth codex         check codex CLI login
  ccms version            binary version
  ccms help               this help
`

// Execute runs the command; returns the process exit code.
func Execute(args []string, stdout io.Writer) int {
	if len(args) == 0 {
		return runMenu(args, stdout)
	}
	switch args[0] {
	case "version", "--version", "-v":
		fmt.Fprintf(stdout, "ccms %s\n", Version)

		return 0
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usage)

		return 0
	case "init":
		return runInit(args, stdout)
	case "list", "-l":
		return runList(args, stdout)
	case "status", "-s":
		return runStatus(args, stdout)
	case "edit", "-e":
		return runEdit(args, stdout)
	case "use":
		return runUse(args, stdout)
	case "serve":
		return runServe(args, stdout)
	case "stop":
		return runStop(args, stdout)
	case "auth":
		return runAuth(args, stdout)
	default:
		// not a command — likely a profile: ccms zai / ccms zai glm-5.3 (bash parity)
		if !strings.HasPrefix(args[0], "-") {
			useArgs := append([]string{"use", args[0]}, args[1:]...)

			return runUse(useArgs, stdout)
		}
		fmt.Fprintf(stdout, "ccms: unknown command %q\n\n%s", args[0], usage)

		return 1
	}
}
