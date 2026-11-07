// ccx — switching LLM providers for Claude Code: CLI + built-in daemon
// with protocol translators (anthropic/openai/copilot/codex).
package main

import (
	"os"

	"github.com/ivangurin/ccx/internal/cli"
)

func main() {
	migrateLegacy()

	os.Exit(cli.Execute(os.Args[1:], os.Stdout))
}
