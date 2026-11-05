package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/services/switcher"
)

// runUse — ccx use <профиль> [модель] (алиас: ccx <профиль> [модель]).
func runUse(args []string, stdout io.Writer) int {
	if len(args) < 2 || args[1] == "" {
		fmt.Fprintln(stdout, "ccx: укажите профиль: ccx use <профиль> [модель] (см. ccx list)")

		return 1
	}
	cfg, err := config.NewFromEnv()
	if err != nil {
		fmt.Fprintf(stdout, "ccx: %v\n", err)

		return 1
	}
	model := ""
	if len(args) > 2 {
		model = args[2]
	}

	sw := switcher.New(cfg, switcher.NewHTTPDaemon(cfg))
	out, err := sw.Apply(context.Background(), args[1], model)
	if err != nil {
		fmt.Fprintf(stdout, "%v\n", err)

		return 1
	}
	fmt.Fprintln(stdout, out)

	return 0
}
