package models

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Settings — settings.json Claude Code с точечной правкой: читается в
// raw-представление, патчатся только ключи env/model/availableModels/
// enforceAvailableModels — остальные поля файла переживают свитч байт-в-байт
// (инвариант bash-ccx: ccx не владеет чужими настройками).
type Settings struct {
	Raw map[string]json.RawMessage
}

// LoadSettings читает settings.json целиком (включая неизвестные поля).
func LoadSettings(path string) (*Settings, error) {
	var raw map[string]json.RawMessage
	if err := readRawJSON(path, &raw); err != nil {
		return nil, err
	}

	return &Settings{Raw: raw}, nil
}

// Env возвращает копию env-блока (пустая карта, если нет).
func (s *Settings) Env() map[string]string {
	env := map[string]string{}
	if raw, ok := s.Raw["env"]; ok {
		_ = json.Unmarshal(raw, &env) // невалидный env-блок — работаем с пустым
	}

	return env
}

// SetEnv заменяет env-блок.
func (s *Settings) SetEnv(env map[string]string) {
	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return
	}
	s.Raw["env"] = data
}

// SetModelPin запинняет модель: model/availableModels/enforceAvailableModels.
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

// Marshal выдаёт файл (ключи в исходном порядке не гарантируются — Go map;
// semantic-эквивалентность важнее, Claude Code файл перечитывает целиком).
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
