// Package jsonpatch — byte-precise edits of JSON object entries: a value is
// replaced in place, a new key is appended to the end of its object, a key is
// cut out with its separators. Everything outside the edited entry — key
// order, indentation, blank lines, the trailing newline — survives
// byte-for-byte. Unlike a full unmarshal→marshal round-trip (or sjson, which
// inserts compactly with a hanging comma), the untouched parts of the file
// are never reformatted; ccx does not own settings.json, so its edits must
// be invisible in a diff.
//
// One operation per file: jsonpatch.go (the Entry/Object model + ObjectAt),
// set.go, delete.go, parse.go (the structural scanner + its skip family).
package jsonpatch

import (
	"errors"
	"fmt"
	"strings"
)

// Entry is one key/value pair of an object with exact spans in the source
// bytes (ValEnd is exclusive).
type Entry struct {
	Key      string
	KeyStart int
	KeyEnd   int // after the closing quote of the key
	ValStart int
	ValEnd   int
}

// Object is a parsed JSON object located at data[Open]=='{' and data[Close]=='}'.
type Object struct {
	Open    int
	Close   int
	Entries []Entry
}

// Find returns the entry for key, or nil if the object has none.
func (o *Object) Find(key string) *Entry {
	for i := range o.Entries {
		if o.Entries[i].Key == key {
			return &o.Entries[i]
		}
	}

	return nil
}

// ObjectAt resolves a chain of object keys starting at the file's top object
// and parses the object that the chain ends in (ObjectAt(data) is the top
// object itself; ObjectAt(data, "env") is the env block).
func ObjectAt(data []byte, path ...string) (*Object, error) {
	top := skipWS(data, 0)
	if top >= len(data) || data[top] != '{' {
		return nil, errors.New("jsonpatch: the document is not a JSON object")
	}
	obj, err := ParseAt(data, top)
	if err != nil {
		return nil, err
	}
	for _, key := range path {
		e := obj.Find(key)
		if e == nil {
			return nil, fmt.Errorf("jsonpatch: key %q not found", key)
		}
		if data[e.ValStart] != '{' {
			return nil, fmt.Errorf("jsonpatch: %s is not an object", strings.Join(append(append([]string{}, path...), key), "."))
		}
		if obj, err = ParseAt(data, e.ValStart); err != nil {
			return nil, err
		}
	}

	return obj, nil
}
