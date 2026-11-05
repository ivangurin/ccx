// Package flock — file locks on unix flock(2). The mutex file lives forever
// (not deleted on Unlock): deleting it while someone else waits is a "lock on
// the old inode" race; a permanent file excludes it by construction.
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
	// ErrLocked — the lock is already held by another holder (LOCK_NB).
	ErrLocked = errors.New("flock: already locked")
	// ErrTimeout — Lock did not get released within the timeout.
	ErrTimeout = errors.New("flock: timeout")
)

// Lock — a held file lock. The owning process's death releases the lock
// automatically (the kernel closes the fd) — stale locks are impossible.
type Lock struct {
	file *os.File
}

func acquire(path string, nb bool) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("flock: mkdir %s: %w", filepath.Dir(path), err)
	}
	// O_CREATE: the file may not exist yet; 0600 perms — the lock may carry pid info.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // path is a config-owner parameter, not user input
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

// TryLock takes an exclusive lock without waiting; if held — ErrLocked.
func TryLock(path string) (*Lock, error) {
	return acquire(path, true)
}

// LockPoll — the polling interval in LockWait (LOCK_NB polling instead of a
// blocking flock: a blocking flock cannot be interrupted by context/timeout).
const LockPoll = 50 * time.Millisecond

// LockWait takes the lock, waiting for release up to timeout; if it does not
// get it — ErrTimeout. timeout <= 0 — a single attempt (error-wise equivalent
// to TryLock).
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

// Unlock releases the lock (closing the fd drops the flock).
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
