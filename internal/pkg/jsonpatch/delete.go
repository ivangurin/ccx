package jsonpatch

import (
	"bytes"
)

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
