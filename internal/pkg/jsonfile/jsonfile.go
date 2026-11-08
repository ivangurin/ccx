// Package jsonfile — atomic writes of JSON files: marshal → tmp in the same
// directory → chmod to the target's perms → rename. A rename within one
// directory is atomic; readers never see half a file (a bash-ccx invariant).
package jsonfile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// WriteAtomicJSON atomically writes v to path. perm is applied to the new
// file if the target does not exist; the target's existing perms are kept
// (parity with bash-ccx: chmod tmp to the target's current perms BEFORE
// rename).
func WriteAtomicJSON(path string, v any, perm os.FileMode) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("jsonfile: marshal %s: %w", path, err)
	}
	data = append(data, '\n')

	return WriteAtomicRaw(path, data, perm)
}

// WriteAtomicRaw atomically writes pre-serialized bytes (settings.json is
// patched as TEXT — the byte-precise patcher's output must not round-trip
// through a marshaler). Same perm policy as WriteAtomicJSON: perm only for
// a target that does not exist yet, the existing perms kept otherwise — the
// one atomic-write implementation and one perm policy for every caller.
func WriteAtomicRaw(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("jsonfile: mkdir %s: %w", filepath.Dir(path), err)
	}

	mode := perm
	if info, statErr := os.Stat(path); statErr == nil {
		mode = info.Mode().Perm()
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("jsonfile: create tmp for %s: %w", path, err)
	}
	tmpName := tmp.Name()
	defer func() {
		// after a successful rename the tmp no longer exists — ignore errors
		_ = os.Remove(tmpName)
	}()

	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()

		return fmt.Errorf("jsonfile: chmod %s: %w", tmpName, err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()

		return fmt.Errorf("jsonfile: write %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("jsonfile: close %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("jsonfile: rename %s → %s: %w", tmpName, path, err)
	}

	return nil
}

// ReadJSON reads JSON from path into v.
func ReadJSON(path string, v any) error {
	data, err := os.ReadFile(path) //nolint:gosec // path is a config-owner parameter, not user input
	if err != nil {
		return fmt.Errorf("jsonfile: read %s: %w", path, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("jsonfile: unmarshal %s: %w", path, err)
	}

	return nil
}

// ReadRaw reads the file's bytes as-is (same error wrapping as ReadJSON).
// Why raw: callers that inspect the raw document for keys the decoder drops
// (legacy-schema detection) must not read the file twice — a second read
// races with a concurrent rewrite.
func ReadRaw(path string) ([]byte, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is a config-owner parameter, not user input
	if err != nil {
		return nil, fmt.Errorf("jsonfile: read %s: %w", path, err)
	}

	return data, nil
}
