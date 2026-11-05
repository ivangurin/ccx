package models

import (
	"encoding/json"
	"fmt"
	"os"
)

// readRawJSON — общий ридер для raw-моделей (Settings и подобные).
func readRawJSON(path string, v any) error {
	data, err := os.ReadFile(path) //nolint:gosec // путь — из конфига владельца
	if err != nil {
		return fmt.Errorf("models: read %s: %w", path, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("models: unmarshal %s: %w", path, err)
	}

	return nil
}
