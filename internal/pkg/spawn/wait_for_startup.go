package spawn

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"
)

// waitForStartupPoll is the health/death poll cadence; the per-probe HTTP
// timeout never exceeds it (a probe running the full old 1s made the budget
// overshoot and left ctx cancellation blind for up to a second).
const (
	waitForStartupPoll  = 200 * time.Millisecond
	waitForStartupProbe = time.Second
)

// WaitForStartup polls /health until it answers, the child process dies, or
// the budget runs out. A child that dies mid-wait fails FAST — the reason is
// lifted from its log (the "daemon exited with error" line, the child's own
// last word) so the switch error names WHY the daemon did not come up; the
// generic "did not respond" (with the log-glob hint as the only pointer)
// stays reserved for a child that lives but never answers. Health is
// trusted only AFTER the Done check: an answer from a foreign server that
// grabbed the machine-global port must not mask our child's death.
func WaitForStartup(ctx context.Context, baseURL string, child *Child, logDir string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-child.Done():
			return childFailure(child)
		default:
		}
		probe := min(time.Until(deadline), waitForStartupProbe)
		if probe > 0 && HealthCheck(baseURL, probe) {
			select {
			case <-child.Done():
				return childFailure(child)
			default:
			}

			return nil
		}
		select {
		case <-child.Done():
			return childFailure(child)
		case <-ctx.Done():
			return fmt.Errorf("cancelled waiting for %s: %w", baseURL, ctx.Err())
		case <-time.After(waitForStartupPoll):
		}
	}

	return fmt.Errorf("daemon did not respond within %s (logs: %s/daemon-*.log)", timeout, logDir)
}

// childFailure builds the dead-child error: the log's own diagnosis first
// (the "daemon exited with error" line the child wrote before exiting), the
// process wait status as the fallback, the log path always — the user reads
// the reason without opening the file.
func childFailure(child *Child) error {
	reason := startupFailureReason(child.LogPath())
	if reason == "" {
		if err := child.ExitErr(); err != nil {
			reason = err.Error()
		}
	}
	if reason == "" {
		reason = "unknown reason"
	}

	return fmt.Errorf("daemon exited during startup: %s (log: %s)", reason, child.LogPath())
}

// startupFailureReason scans the child's log BACKWARD for its last word —
// the newest msg="daemon exited with error" JSONL line (the log is opened in
// APPEND mode and shared by same-second relaunches, so the file may hold
// several launches' failure lines) — and returns its err attribute; any
// unreadable/non-JSON line is skipped, no match means no reason (empty).
func startupFailureReason(logPath string) string {
	data, err := os.ReadFile(logPath) //nolint:gosec // the path we gave the child
	if err != nil {
		return ""
	}
	var line struct {
		Msg string `json:"msg"`
		Err string `json:"err"`
	}
	for _, raw := range slices.Backward(strings.Split(string(data), "\n")) {
		raw = strings.TrimSpace(raw)
		if !strings.Contains(raw, DaemonExitedWithErrorMsg) {
			continue
		}
		if json.Unmarshal([]byte(raw), &line) == nil && line.Err != "" {
			return line.Err
		}
	}

	return ""
}
