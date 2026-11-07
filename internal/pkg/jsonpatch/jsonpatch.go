// Package jsonpatch — byte-precise edits of JSON object entries: a value is
// replaced in place, a new key is appended to the end of its object, a key is
// cut out with its separators. Everything outside the edited entry — key
// order, indentation, blank lines, the trailing newline — survives
// byte-for-byte. Unlike a full unmarshal→marshal round-trip (or sjson, which
// inserts compactly with a hanging comma), the untouched parts of the file
// are never reformatted; ccx does not own settings.json, so its edits must
// be invisible in a diff.
package jsonpatch

import (
	"bytes"
	"encoding/json"
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

// Set replaces the value of key in the object at path in place, or appends
// the pair to the end of that object when the key is absent (inheriting the
// object's indentation and the ": " style of its first pair). rawValue is
// raw JSON (e.g. `"x"`, `1`).
func Set(data []byte, path []string, key string, rawValue []byte) ([]byte, error) {
	obj, err := ObjectAt(data, path...)
	if err != nil {
		return nil, err
	}
	if e := obj.Find(key); e != nil {
		return splice(data, e.ValStart, e.ValEnd, rawValue)
	}

	return obj.appendEntry(data, key, rawValue)
}

// Delete removes the key and its separators from the object at path; a
// missing key is a no-op.
func Delete(data []byte, path []string, key string) ([]byte, error) {
	obj, err := ObjectAt(data, path...)
	if err != nil {
		return nil, err
	}
	idx := -1
	for i := range obj.Entries {
		if obj.Entries[i].Key == key {
			idx = i

			break
		}
	}
	if idx < 0 {
		return data, nil
	}

	e := obj.Entries[idx]
	if idx+1 < len(obj.Entries) { // a follower exists: cut up to its key start
		// (the entry's own leader before KeyStart stays and becomes the
		// follower's — no doubled blank runs)
		return splice(data, e.KeyStart, obj.Entries[idx+1].KeyStart, nil)
	}

	// last entry: cut the comma before the key (or the whole gap when it is
	// the only one, so `{ \n "K": "v" \n }` collapses to `{ \n }`)
	prevEnd := obj.Open + 1
	if idx > 0 {
		prevEnd = obj.Entries[idx-1].ValEnd
	}
	delStart := e.KeyStart
	if rel := bytes.LastIndexByte(data[prevEnd:e.KeyStart], ','); rel >= 0 {
		delStart = prevEnd + rel
	} else if idx == 0 {
		delStart = prevEnd
	}

	return splice(data, delStart, e.ValEnd, nil)
}

// splice replaces data[start:end) with repl and validates the result — a
// broken edit must surface as an error, never as a corrupt file.
func splice(data []byte, start, end int, repl []byte) ([]byte, error) {
	out := make([]byte, 0, len(data)+len(repl))
	out = append(out, data[:start]...)
	out = append(out, repl...)
	out = append(out, data[end:]...)
	if !json.Valid(out) {
		return nil, fmt.Errorf("jsonpatch: edit at %d:%d produced invalid JSON", start, end)
	}

	return out, nil
}

func (o *Object) appendEntry(data []byte, key string, rawValue []byte) ([]byte, error) {
	pair := o.newPair(data, key, rawValue)

	if len(o.Entries) == 0 {
		// empty object: rebuild the gap between the braces around the pair.
		// The style follows the FILE: a pretty file gets the pair on its own
		// lines (even if the empty braces were inline, e.g. "env": {}), a
		// compact file stays compact.
		gap := string(data[o.Open+1 : o.Close])
		if bytes.IndexByte(data, '\n') >= 0 { // pretty file
			closeIndent := IndentOf(data, o.Close)
			if strings.TrimLeft(closeIndent, " \t") != "" { // inline braces: use the line's indent
				closeIndent = lineIndent(data, o.Open)
			}
			entryIndent := closeIndent + strings.Repeat(" ", detectUnit(data))
			newGap := "\n" + entryIndent + string(pair) + "\n" + closeIndent

			return splice(data, o.Open+1, o.Close, []byte(newGap))
		}

		return splice(data, o.Open+1, o.Close, []byte(gap+string(pair)+gap))
	}

	// non-empty: insert right after the last value, reusing the leader of the
	// first pair (",\n    " style) so the new line matches its siblings
	leader := string(data[o.Open+1 : o.Entries[0].KeyStart])
	last := o.Entries[len(o.Entries)-1]
	ins := append([]byte(","+leader), pair...)

	return splice(data, last.ValEnd, last.ValEnd, ins)
}

// newPair renders `"key"<colon>value`; the colon style (`": "` vs `":"`) is
// inherited from the object's first pair.
func (o *Object) newPair(data []byte, key string, rawValue []byte) []byte {
	colon := ":"
	if len(o.Entries) > 0 {
		colon = string(data[o.Entries[0].KeyEnd:o.Entries[0].ValStart])
	} else if bytes.IndexByte(data, '\n') >= 0 {
		colon = ": " // no sample pair in an empty object; follow the file's pretty style
	}
	kb, err := json.Marshal(key)
	if err != nil {
		// string keys always marshal; unreachable, kept for robustness
		kb = []byte(`"?"`)
	}

	return append(append(kb, colon...), rawValue...)
}

// detectUnit guesses the file's indent unit (used only when filling a
// previously empty object): the indent of the top-level first key, else 2.
func detectUnit(data []byte) int {
	obj, err := ObjectAt(data)
	if err != nil || len(obj.Entries) == 0 {
		return 2
	}
	if n := len(IndentOf(data, obj.Entries[0].KeyStart)); n > 0 {
		return n
	}

	return 2
}

// IndentOf returns the spaces/tabs between the line start before pos and pos.
func IndentOf(data []byte, pos int) string {
	start := pos
	for start > 0 && data[start-1] != '\n' {
		start--
	}

	return string(data[start:pos])
}

// lineIndent returns the leading whitespace of the line containing pos.
func lineIndent(data []byte, pos int) string {
	start := pos
	for start > 0 && data[start-1] != '\n' {
		start--
	}

	return string(data[start:skipWS(data, start)])
}

// ParseAt parses the object whose '{' sits at data[openIdx]; entry spans are
// absolute indexes into data. The input is expected to be valid JSON
// (LoadSettings validates); the scanner is structural, not a validator.
func ParseAt(data []byte, openIdx int) (*Object, error) {
	if openIdx < 0 || openIdx >= len(data) || data[openIdx] != '{' {
		return nil, fmt.Errorf("jsonpatch: data[%d] is not the start of an object", openIdx)
	}

	obj := &Object{Open: openIdx}
	i := skipWS(data, openIdx+1)
	for i < len(data) && data[i] != '}' {
		e, next, err := parseEntry(data, i)
		if err != nil {
			return nil, err
		}
		obj.Entries = append(obj.Entries, *e)
		i = next
	}
	if i >= len(data) {
		return nil, fmt.Errorf("jsonpatch: object starting at %d is not closed", openIdx)
	}
	obj.Close = i

	return obj, nil
}

// parseEntry reads one key/value pair at i; spans land in the Entry, next
// points past the entry's separator (',' → next entry, '}' → object close).
func parseEntry(data []byte, i int) (*Entry, int, error) {
	if data[i] != '"' {
		return nil, 0, fmt.Errorf("jsonpatch: expected a key at offset %d", i)
	}
	e := &Entry{KeyStart: i}
	e.KeyEnd = skipString(data, i)
	if e.KeyEnd < 0 {
		return nil, 0, fmt.Errorf("jsonpatch: unterminated key at offset %d", e.KeyStart)
	}
	if err := json.Unmarshal(data[e.KeyStart:e.KeyEnd], &e.Key); err != nil {
		return nil, 0, fmt.Errorf("jsonpatch: bad key at offset %d: %w", e.KeyStart, err)
	}

	i = skipWS(data, e.KeyEnd)
	if i >= len(data) || data[i] != ':' {
		return nil, 0, fmt.Errorf("jsonpatch: expected ':' after key %q", e.Key)
	}
	e.ValStart = skipWS(data, i+1)
	e.ValEnd = skipValue(data, e.ValStart)
	if e.ValEnd < 0 || e.ValStart == e.ValEnd {
		return nil, 0, fmt.Errorf("jsonpatch: bad value for key %q", e.Key)
	}

	next := skipWS(data, e.ValEnd)
	if next < len(data) && data[next] == ',' {
		next = skipWS(data, next+1)
	} else if next < len(data) && data[next] != '}' {
		return nil, 0, fmt.Errorf("jsonpatch: expected ',' or '}' after the value of %q", e.Key)
	}

	return e, next, nil
}

func skipWS(data []byte, i int) int {
	for i < len(data) {
		switch data[i] {
		case ' ', '\t', '\n', '\r':
			i++
		default:
			return i
		}
	}

	return i
}

// skipString returns the index after the closing quote, or -1.
func skipString(data []byte, i int) int {
	i++ // opening quote
	for i < len(data) {
		switch data[i] {
		case '\\':
			i += 2
		case '"':
			return i + 1
		default:
			i++
		}
	}

	return -1
}

// skipValue returns the index after the value starting at i, or -1.
func skipValue(data []byte, i int) int {
	switch data[i] {
	case '"':
		return skipString(data, i)
	case '{', '[':
		return skipContainer(data, i)
	default: // number, true, false, null — up to the next delimiter
		return skipLiteral(data, i)
	}
}

// skipContainer walks a nested object/array, honoring strings inside.
func skipContainer(data []byte, i int) int {
	depth := 0
	for i < len(data) {
		switch data[i] {
		case '"':
			if j := skipString(data, i); j > 0 {
				i = j

				continue
			}

			return -1
		case '{', '[':
			depth++
		case '}', ']':
			depth--
		default:
		}
		i++
		if depth == 0 {
			return i
		}
	}

	return -1
}

// skipLiteral walks a bare token to its end.
func skipLiteral(data []byte, i int) int {
	for i < len(data) {
		switch data[i] {
		case ' ', '\t', '\n', '\r', ',', '}', ']':
			return i
		default:
			i++
		}
	}

	return i
}
