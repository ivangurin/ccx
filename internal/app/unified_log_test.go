package app_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ivangurin/ccx/internal/app"
	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/models"
	suite_provider "github.com/ivangurin/ccx/internal/pkg/suite/provider"
	"github.com/stretchr/testify/require"
)

// The unified daemon log: a usage record rides the daemon logger as one
// JSONL line with kind=usage — no separate usage-YYYY-MM-DD.jsonl file is
// created anymore. In-process Run has no file sink — the daemon-*.log FILE
// is the spawned daemon's stderr redirect (spawn.go); the writer Run is
// given IS the stream that redirect captures.
func TestRun_UsageRidesUnifiedLog(t *testing.T) {
	sp := suite_provider.NewProvider(t)

	var upstreamHit sync.Once
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamHit.Do(func() {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"m","type":"message","role":"assistant","model":"glm","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":2}}`))
		})
	}))
	t.Cleanup(up.Close)
	upURL, err := url.Parse(up.URL)
	require.NoError(t, err)

	p := &models.Providers{}
	p.Providers.Set("zai", &models.Profile{
		Name:     "z.ai",
		Protocol: models.ProtocolAnthropic,
		BaseURL:  upURL.String(),
		Models:   []models.Model{{ID: "glm", Label: "GLM", Default: true}},
	})
	require.NoError(t, config.SaveProviders(sp.Config.ProvidersPath(), p))

	capture := &suite_provider.Capture{}
	log := slog.New(slog.NewJSONHandler(capture, &slog.HandlerOptions{Level: slog.LevelWarn}))
	// the usage stream: what buildServeLogger returns as the second logger —
	// info+json pinned, pre-attributed kind=usage (usage is data, not
	// diagnostics; a warn-level diagnostics logger must not silence it)
	usageLog := slog.New(slog.NewJSONHandler(capture, &slog.HandlerOptions{Level: slog.LevelInfo})).With("kind", "usage")

	ctx, cancel := context.WithCancel(sp.Context())
	port := freePort(t)
	addr := "127.0.0.1:" + strconv.Itoa(port)

	errCh := make(chan error, 1)
	go func() {
		errCh <- app.New(sp.Config, log, usageLog).Run(ctx, app.ServeOptions{Provider: "zai", Port: port})
	}()
	// LIFO: cancel first, THEN drain Run's result — Run returns only after
	// the ctx cancel is observed
	t.Cleanup(func() {
		cancel()
		<-errCh //nolint:errcheck // drain: the error value is irrelevant here
	})

	// wait for /health, then serve one message through the passthrough
	require.Eventually(t, func() bool {
		resp, err := http.Get("http://" + addr + "/health") //nolint:gosec // test's own daemon
		if err != nil {
			return false
		}
		_ = resp.Body.Close()

		return resp.StatusCode == http.StatusOK
	}, 5*time.Second, 50*time.Millisecond, "daemon never became healthy")

	msgReq, err := http.NewRequest(http.MethodPost, "http://"+addr+"/v1/messages",
		strings.NewReader(`{"model":"glm","max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`))
	require.NoError(t, err)
	msgReq.Header.Set("Content-Type", "application/json")
	msgResp, err := http.DefaultClient.Do(msgReq)
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, msgResp.Body)
	_ = msgResp.Body.Close()
	require.Equal(t, http.StatusOK, msgResp.StatusCode)

	// the usage line rides the daemon logger with the kind discriminator and
	// the full Entry schema
	require.Eventually(t, func() bool {
		return strings.Contains(capture.String(), `"kind":"usage"`)
	}, 5*time.Second, 50*time.Millisecond, "no usage line in the unified daemon log stream")

	var entry map[string]any
	found := false
	for _, line := range strings.Split(strings.TrimRight(capture.String(), "\n"), "\n") {
		if !strings.Contains(line, `"kind":"usage"`) {
			continue
		}
		require.NoError(t, json.Unmarshal([]byte(line), &entry), "line: %s", line)
		found = true
	}
	require.True(t, found, "usage line present")
	require.Equal(t, "usage", entry["msg"])
	require.Equal(t, "z.ai", entry["provider"])
	require.Equal(t, "anthropic", entry["protocol"])
	require.Equal(t, "glm", entry["model"])
	_, hasError := entry["error"]
	require.False(t, hasError, "clean request carries no error field")

	// and no per-day usage file is created in the log dir
	entries, err := os.ReadDir(sp.Config.LogDir())
	if err == nil {
		for _, e := range entries {
			require.NotContains(t, e.Name(), "usage-", "usage-*.jsonl must be gone: %s", e.Name())
		}
	}
}
