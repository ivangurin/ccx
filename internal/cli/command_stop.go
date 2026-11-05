package cli

import (
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/ivangurin/ccx/internal/config"
)

// runStop — ccx stop: POST /admin/stop (graceful shutdown демона).
func runStop(_ []string, stdout io.Writer) int {
	cfg, err := config.NewFromEnv()
	if err != nil {
		fmt.Fprintf(stdout, "ccx: %v\n", err)

		return 1
	}
	port := cfg.DaemonPort
	if port == 0 {
		providers, perr := config.LoadProviders(cfg.ProvidersPath())
		if perr != nil {
			fmt.Fprintln(stdout, "ccx: providers.json недоступен — сначала ccx init")

			return 1
		}
		port = providers.Daemon.Port
	}
	base := fmt.Sprintf("http://127.0.0.1:%d", port)

	if !alive(base) {
		fmt.Fprintln(stdout, "Демон не запущен.")

		return 0
	}

	var token string
	if providers, err := config.LoadProviders(cfg.ProvidersPath()); err == nil {
		token = providers.Daemon.AuthToken
	}
	req, _ := http.NewRequest(http.MethodPost, base+"/admin/stop", nil) //nolint:gosec // локальный адрес
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		fmt.Fprintf(stdout, "ccx: не удалось остановить демон: %v\n", err)

		return 1
	}
	defer func() { _ = resp.Body.Close() }()

	// дождаться фактического закрытия порта (иначе «остановлен», а процесс жив)
	for range 50 {
		if !alive(base) {
			fmt.Fprintln(stdout, "Демон остановлен.")

			return 0
		}
		time.Sleep(100 * time.Millisecond)
	}
	fmt.Fprintf(stdout, "ccx: демон всё ещё отвечает (лог: %s)\n", cfg.LogDir()+"/daemon.log")

	return 0
}

func alive(base string) bool {
	client := &http.Client{Timeout: time.Second}
	resp, err := client.Get(base + "/health") //nolint:gosec // локальный адрес
	if err != nil {
		return false
	}
	_ = resp.Body.Close()

	return true
}
