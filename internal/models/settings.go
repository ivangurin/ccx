package models

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Settings is Claude Code's settings.json with surgical patching: read into
// a raw representation, only the env/model/availableModels/
// enforceAvailableModels keys are patched — all other fields of the file
// survive a switch byte-for-byte (bash-ccx invariant: ccms does not own
// settings that belong to someone else).
type Settings struct {
	Raw map[string]json.RawMessage
}

// LoadSettings reads settings.json in full (including unknown fields).
func LoadSettings(path string) (*Settings, error) {
	var raw map[string]json.RawMessage
	if err := readRawJSON(path, &raw); err != nil {
		return nil, err
	}

	return &Settings{Raw: raw}, nil
}

// Env returns a copy of the env block (empty map if absent).
func (s *Settings) Env() map[string]string {
	env := map[string]string{}
	if raw, ok := s.Raw["env"]; ok {
		_ = json.Unmarshal(raw, &env) // invalid env block — proceed with an empty one
	}

	return env
}

// SetEnv replaces the env block.
func (s *Settings) SetEnv(env map[string]string) {
	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return
	}
	s.Raw["env"] = data
}

// SetModelPin pins a model: model/availableModels/enforceAvailableModels.
func (s *Settings) SetModelPin(model string) {
	put := func(key string, v any) {
		data, err := json.Marshal(v)
		if err != nil {
			return
		}
		s.Raw[key] = data
	}
	put("model", model)
	put("availableModels", []string{model})
	put("enforceAvailableModels", true)
}

// Marshal emits the file (original key order is not guaranteed — Go map;
// semantic equivalence matters more, Claude Code re-reads the file in full).
func (s *Settings) Marshal() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	first := true
	for key, raw := range s.Raw {
		if !first {
			buf.WriteByte(',')
		}
		first = false
		kb, err := json.Marshal(key)
		if err != nil {
			return nil, fmt.Errorf("settings: marshal key: %w", err)
		}
		buf.Write(kb)
		buf.WriteByte(':')
		buf.Write(raw)
	}
	buf.WriteByte('}')

	return buf.Bytes(), nil
}
