package models

import (
	"encoding/json"
	"fmt"
	"os"
)

// readRawJSON is the shared reader for raw models (Settings and the like).
func readRawJSON(path string, v any) error {
	data, err := os.ReadFile(path) //nolint:gosec // path comes from the owning config
	if err != nil {
		return fmt.Errorf("models: read %s: %w", path, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("models: unmarshal %s: %w", path, err)
	}

	return nil
}
