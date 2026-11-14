package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/provider"
)

// runStop — ccx stop: graceful /admin/stop with the switcher's shutdown
// ladder (port wait → SIGTERM/SIGKILL escalation by the pidfile — a hung SSE
// stream can hold the graceful drain forever). Reusing the daemon client's
// Stop keeps one implementation of the ladder; a daemon that survives it
// fails the command: exit 0 "still responding" told scripts the port was
// free while the next spawn then lost the lock race.
func runStop(_ []string, stdout io.Writer) int {
	p, err := provider.NewEnv()
	if err != nil {
		fmt.Fprintf(stdout, "ccx: %v\n", err)

		return 1
	}
	base := fmt.Sprintf("http://127.0.0.1:%d", config.ResolveDaemonPort(p.GetConfig(), 0))

	if !alive(base) {
		fmt.Fprintln(stdout, "Daemon is not running.")

		return 0
	}

	if err := p.GetDaemon().Stop(context.Background()); err != nil {
		fmt.Fprintf(stdout, "ccx: %v\n", err)

		return 1
	}
	fmt.Fprintln(stdout, "Daemon stopped.")

	return 0
}

func alive(base string) bool {
	client := &http.Client{Timeout: time.Second}
	resp, err := client.Get(base + "/health") //nolint:gosec // local daemon address
	if err != nil {
		return false
	}
	_ = resp.Body.Close()

	return true
}
