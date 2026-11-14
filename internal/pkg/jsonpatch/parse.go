package jsonpatch

import (
	"encoding/json"
	"fmt"
)

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
