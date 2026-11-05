// Package jsonfile — атомарная запись JSON-файлов: marshal → tmp в том же
// каталоге → chmod под права цели → rename. rename в пределах одного каталога
// атомарен, читатели никогда не видят половину файла (инвариант bash-ccx).
package jsonfile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// WriteAtomicJSON записывает v в path атомарно. perm применяется к новому
// файлу, если цель не существует; существующие права цели сохраняются
// (паритет с bash-ccx: chmod tmp под текущие права цели ДО rename).
func WriteAtomicJSON(path string, v any, perm os.FileMode) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("jsonfile: marshal %s: %w", path, err)
	}
	data = append(data, '\n')

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
		// после успешного rename tmp уже не существует — ошибки игнорируем
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

// ReadJSON читает JSON из path в v.
func ReadJSON(path string, v any) error {
	data, err := os.ReadFile(path) //nolint:gosec // путь — параметр владельца конфига, не пользовательский ввод
	if err != nil {
		return fmt.Errorf("jsonfile: read %s: %w", path, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("jsonfile: unmarshal %s: %w", path, err)
	}

	return nil
}
