// ccx — переключение LLM-провайдеров Claude Code: CLI + встроенный демон
// с трансляторами протоколов (anthropic/openai/copilot/codex).
package main

import (
	"os"

	"github.com/ivangurin/ccx/internal/cli"
)

func main() {
	os.Exit(cli.Execute(os.Args[1:], os.Stdout))
}
