package models

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// OrderedMap — JSON-объект как map с сохранением порядка ключей файла:
// порядок = порядок меню и ccx list (jq в bash-ccx порядок сохранял, Go map — нет).
type OrderedMap[V any] struct {
	Keys []string
	m    map[string]V
}

// Get возвращает значение по ключу (zero value, если нет).
func (om *OrderedMap[V]) Get(key string) V {
	if om.m == nil {
		var zero V

		return zero
	}

	return om.m[key]
}

// Has сообщает о наличии ключа.
func (om *OrderedMap[V]) Has(key string) bool {
	_, ok := om.m[key]

	return ok
}

// Set добавляет/заменяет значение; новый ключ попадает в конец порядка.
func (om *OrderedMap[V]) Set(key string, v V) {
	if om.m == nil {
		om.m = map[string]V{}
	}
	if _, exists := om.m[key]; !exists {
		om.Keys = append(om.Keys, key)
	}
	om.m[key] = v
}

// Len — число элементов.
func (om *OrderedMap[V]) Len() int {
	return len(om.Keys)
}

// MarshalJSON пишет объект в порядке Keys (стабильный файл — чистые диффы).
func (om OrderedMap[V]) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, key := range om.Keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, err := json.Marshal(key)
		if err != nil {
			return nil, fmt.Errorf("ordered map: marshal key: %w", err)
		}
		buf.Write(kb)
		buf.WriteByte(':')
		vb, err := json.Marshal(om.m[key])
		if err != nil {
			return nil, fmt.Errorf("ordered map: marshal %s: %w", key, err)
		}
		buf.Write(vb)
	}
	buf.WriteByte('}')

	return buf.Bytes(), nil
}

// UnmarshalJSON читает объект, извлекая ключи в порядке следования в файле.
func (om *OrderedMap[V]) UnmarshalJSON(b []byte) error {
	om.m = map[string]V{}
	om.Keys = nil
	if string(b) == "null" {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	tok, err := dec.Token()
	if err != nil {
		return fmt.Errorf("ordered map: %w", err)
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return fmt.Errorf("ordered map: expected object, got %v", tok)
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return fmt.Errorf("ordered map: %w", err)
		}
		key, ok := tok.(string)
		if !ok {
			return fmt.Errorf("ordered map: expected key, got %v", tok)
		}
		var v V
		if err := dec.Decode(&v); err != nil {
			return fmt.Errorf("ordered map: decode %s: %w", key, err)
		}
		om.Set(key, v)
	}
	if _, err := dec.Token(); err != nil {
		return fmt.Errorf("ordered map: %w", err)
	}

	return nil
}
