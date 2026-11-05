// Package flock — файловые локи на unix flock(2). Файл-мьютекс живёт вечно
// (не удаляется при Unlock): удаление при чужом ожидании — гонка «lock старого
// inode», постоянный файл её исключает by construction.
package flock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

var (
	// ErrLocked — лок уже удерживается другим держателем (LOCK_NB).
	ErrLocked = errors.New("flock: already locked")
	// ErrTimeout — Lock не дождался освобождения за timeout.
	ErrTimeout = errors.New("flock: timeout")
)

// Lock — удерживаемый файловый лок. Смерть процесса-владельца освобождает
// лок автоматически (ядро закрывает fd) — stale-локи невозможны.
type Lock struct {
	file *os.File
}

func acquire(path string, nb bool) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("flock: mkdir %s: %w", filepath.Dir(path), err)
	}
	// O_CREATE: файла может не быть; права 0600 — лок может нести pid-инфу.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // путь — параметр владельца конфига, не пользовательский ввод
	if err != nil {
		return nil, fmt.Errorf("flock: open %s: %w", path, err)
	}
	how := unix.LOCK_EX
	if nb {
		how |= unix.LOCK_NB
	}
	if err := unix.Flock(int(file.Fd()), how); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) {
			_ = file.Close()

			return nil, ErrLocked
		}
		_ = file.Close()

		return nil, fmt.Errorf("flock: flock %s: %w", path, err)
	}

	return &Lock{file: file}, nil
}

// TryLock берёт эксклюзивный лок без ожидания; занято — ErrLocked.
func TryLock(path string) (*Lock, error) {
	return acquire(path, true)
}

// LockPoll — интервал опроса в LockWait (поллинг LOCK_NB вместо блокирующего
// flock: блокирующий flock не прерывается по контексту/таймауту).
const LockPoll = 50 * time.Millisecond

// LockWait берёт лок, ожидая освобождения до timeout; не дождался — ErrTimeout.
// timeout <= 0 — единичная попытка (эквивалент TryLock по ошибкам).
func LockWait(path string, timeout time.Duration) (*Lock, error) {
	if timeout <= 0 {
		return TryLock(path)
	}
	deadline := time.Now().Add(timeout)
	for {
		l, err := TryLock(path)
		if err == nil {
			return l, nil
		}
		if !errors.Is(err, ErrLocked) {
			return nil, err
		}
		if time.Now().After(deadline) {
			return nil, ErrTimeout
		}
		time.Sleep(LockPoll)
	}
}

// Unlock освобождает лок (закрытие fd снимает flock).
func (l *Lock) Unlock() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	if err != nil {
		return fmt.Errorf("flock: unlock: %w", err)
	}

	return nil
}
