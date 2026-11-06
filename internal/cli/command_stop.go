package cli

import (
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/ivangurin/ccms/internal/config"
)

// runStop — ccms stop: POST /admin/stop (graceful shutdown of the daemon;
// the surface is localhost-only, no token).
func runStop(_ []string, stdout io.Writer) int {
	cfg, err := config.NewFromEnv()
	if err != nil {
		fmt.Fprintf(stdout, "ccms: %v\n", err)

		return 1
	}
	base := fmt.Sprintf("http://127.0.0.1:%d", config.ResolveDaemonPort(cfg, 0))

	if !alive(base) {
		fmt.Fprintln(stdout, "Daemon is not running.")

		return 0
	}

	req, _ := http.NewRequest(http.MethodPost, base+"/admin/stop", nil) //nolint:gosec // local address
	req.Header.Set("X-Ccms", "1")                                       // the admin marker (browser drive-by defense)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		fmt.Fprintf(stdout, "ccms: failed to stop the daemon: %v\n", err)

		return 1
	}
	defer func() { _ = resp.Body.Close() }()

	// wait for the port to actually close (otherwise "stopped" while the process is alive)
	for range 50 {
		if !alive(base) {
			fmt.Fprintln(stdout, "Daemon stopped.")

			return 0
		}
		time.Sleep(100 * time.Millisecond)
	}
	fmt.Fprintf(stdout, "ccms: daemon is still responding (log: %s)\n", cfg.LogDir()+"/daemon.log")

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
