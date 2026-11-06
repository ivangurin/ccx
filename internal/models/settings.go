package models

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/ivangurin/ccms/internal/pkg/jsonpatch"
)

// Settings is Claude Code's settings.json patched byte-precisely: the file's
// own text is edited in place (jsonpatch), so everything ccms does not own
// survives a switch byte-for-byte — key order, indentation, the trailing
// newline. Fresh env keys land at the end of the env block; a replaced key
// keeps its line. This goes beyond bash-ccx, whose jq round-trip reformatted
// the whole file: ccms does not own settings that belong to someone else.
type Settings struct {
	raw []byte
}

// LoadSettings reads settings.json in full (including unknown fields).
func LoadSettings(path string) (*Settings, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // path comes from the owning config
	if err != nil {
		return nil, fmt.Errorf("models: read %s: %w", path, err)
	}
	if !json.Valid(raw) {
		return nil, fmt.Errorf("models: %s is not valid JSON", path)
	}

	return &Settings{raw: raw}, nil
}

// Env returns a copy of the env block (empty map if absent or not an object).
func (s *Settings) Env() map[string]string {
	env := map[string]string{}
	obj, err := jsonpatch.ObjectAt(s.raw, "env")
	if err != nil {
		return env
	}
	for _, e := range obj.Entries {
		var v string
		if err := json.Unmarshal(s.raw[e.ValStart:e.ValEnd], &v); err == nil {
			env[e.Key] = v
		}
	}

	return env
}

// PatchEnv applies the switch to the env block: stale keys (past-profile
// leftovers) are cut out, fresh keys replace existing ones in place or
// append at the end of the block (sorted — stable diffs). A missing env
// block is created at the end of the file in the file's own indentation
// style; an env that is not an object is an error — the file is left alone.
func (s *Settings) PatchEnv(stale []string, fresh map[string]string) error {
	top, err := jsonpatch.ObjectAt(s.raw)
	if err != nil {
		return fmt.Errorf("settings: %w", err)
	}

	if e := top.Find("env"); e == nil {
		blk, err := buildEnvBlock(s.raw, fresh)
		if err != nil {
			return err
		}
		if s.raw, err = jsonpatch.Set(s.raw, nil, "env", blk); err != nil {
			return fmt.Errorf("settings: creating the env block: %w", err)
		}

		return nil
	} else if s.raw[e.ValStart] != '{' {
		return errors.New("settings: env is not an object — settings.json untouched")
	}

	for _, k := range stale {
		if s.raw, err = jsonpatch.Delete(s.raw, []string{"env"}, k); err != nil {
			return fmt.Errorf("settings: dropping env.%s: %w", k, err)
		}
	}
	for _, k := range slices.Sorted(maps.Keys(fresh)) {
		vb, err := json.Marshal(fresh[k])
		if err != nil {
			return fmt.Errorf("settings: marshal env.%s: %w", k, err)
		}
		if s.raw, err = jsonpatch.Set(s.raw, []string{"env"}, k, vb); err != nil {
			return fmt.Errorf("settings: setting env.%s: %w", k, err)
		}
	}

	return nil
}

// SetModelPin pins a model: model/availableModels/enforceAvailableModels,
// each replaced in place or appended at the end of the file when absent.
func (s *Settings) SetModelPin(model string) error {
	avail, err := json.Marshal([]string{model})
	if err != nil {
		return fmt.Errorf("settings: marshal availableModels: %w", err)
	}
	pins := []struct {
		key string
		raw []byte
	}{
		{"model", mustMarshal(model)},
		{"availableModels", avail},
		{"enforceAvailableModels", []byte("true")},
	}
	for _, p := range pins {
		if s.raw, err = jsonpatch.Set(s.raw, nil, p.key, p.raw); err != nil {
			return fmt.Errorf("settings: pinning %s: %w", p.key, err)
		}
	}

	return nil
}

// Bytes returns the patched file text.
func (s *Settings) Bytes() []byte {
	return s.raw
}

// buildEnvBlock renders a brand-new env block in the file's own style: the
// indent unit is taken from the top level, entries sit one unit deeper.
func buildEnvBlock(file []byte, fresh map[string]string) ([]byte, error) {
	unit := "  "
	if top, err := jsonpatch.ObjectAt(file); err == nil && len(top.Entries) > 0 {
		if u := jsonpatch.IndentOf(file, top.Entries[0].KeyStart); u != "" {
			unit = u
		}
	}

	var b strings.Builder
	b.WriteString("{\n")
	inner := unit + unit
	keys := slices.Sorted(maps.Keys(fresh))
	for i, k := range keys {
		kb, err := json.Marshal(k)
		if err != nil {
			return nil, fmt.Errorf("settings: marshal env key %s: %w", k, err)
		}
		b.WriteString(inner)
		b.Write(kb)
		b.WriteString(": ")
		b.Write(mustMarshal(fresh[k]))
		if i < len(keys)-1 {
			b.WriteByte(',')
		}
		b.WriteByte('\n')
	}
	b.WriteString(unit)
	b.WriteByte('}')

	return []byte(b.String()), nil
}

// mustMarshal — strings always marshal; the error is unreachable.
func mustMarshal(v string) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte(`""`)
	}

	return b
}
