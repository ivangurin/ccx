// ccms — switching LLM providers for Claude Code: CLI + built-in daemon
// with protocol translators (anthropic/openai/copilot/codex).
package main

import (
	"os"

	"github.com/ivangurin/ccms/internal/cli"
)

func main() {
	os.Exit(cli.Execute(os.Args[1:], os.Stdout))
}
