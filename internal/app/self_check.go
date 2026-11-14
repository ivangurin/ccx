package app

import (
	"log/slog"
	"net/http"
	"time"
)

// selfCheckInterval — the self-check cadence (a var: tests shorten it).
var selfCheckInterval = 10 * time.Second

// selfCheck dials the daemon's own /health every selfCheckInterval while the
// process is alive: an answering process with a failing self-check means the
// listener is broken, not the process dead — the distinction the 2026-11-09
// silent-death hunt could not make. Every failure is one Warn line, nothing
// else (no restart, no exit): the daemon never self-heals by design — only
// a switch (ccx use) respawns it.
func selfCheck(log *slog.Logger, baseURL string, done <-chan struct{}) {
	client := &http.Client{Timeout: 2 * time.Second}
	ticker := time.NewTicker(selfCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			resp, err := client.Get(baseURL + "/health") //nolint:gosec // the daemon's own address
			if err != nil {
				log.Warn("self-check: listener not answering", "addr", baseURL, "err", err)

				continue
			}
			_ = resp.Body.Close()
		}
	}
}

// SetSelfCheckIntervalForTest overrides the self-check cadence; the returned
// func restores it.
func SetSelfCheckIntervalForTest(d time.Duration) func() {
	selfCheckInterval = d

	return func() { selfCheckInterval = 10 * time.Second }
}

// SelfCheckForTest exports selfCheck for direct tests.
var SelfCheckForTest = selfCheck
