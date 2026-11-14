package jsonpatch

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

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
