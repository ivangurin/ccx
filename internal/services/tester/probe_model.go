package tester

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/ivangurin/ccx/internal/models"
)

// probeTimeout bounds one model probe (the request includes the daemon
// startup budget too — the probe is the only thing that waits).
const probeTimeout = 30 * time.Second

// probeModel fires one short non-stream messages request through the live
// daemon (the same path Claude Code uses, credential included) and returns
// nil on an HTTP 200.
func probeModel(ctx context.Context, addr string, profile *models.Profile, modelID string) error {
	pctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	body, err := json.Marshal(map[string]any{
		"model":      modelID,
		"max_tokens": 16,
		"messages":   []map[string]string{{"role": "user", "content": "hi"}},
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(pctx, http.MethodPost,
		"http://"+addr+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+credential(profile))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("probe failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("upstream %d: %s", resp.StatusCode, extractMessage(raw))
	}

	return nil
}
