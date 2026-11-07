package flock_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ivangurin/ccx/internal/pkg/flock"
	"github.com/stretchr/testify/require"
)

func TestTryLock_SecondLockFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.lock")

	first, err := flock.TryLock(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = first.Unlock() })

	_, err = flock.TryLock(path)
	require.ErrorIs(t, err, flock.ErrLocked)
}

func TestUnlock_AllowsRelock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.lock")

	first, err := flock.TryLock(path)
	require.NoError(t, err)
	require.NoError(t, first.Unlock())

	second, err := flock.TryLock(path)
	require.NoError(t, err)
	require.NoError(t, second.Unlock())
}

func TestTryLock_CreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.lock")

	l, err := flock.TryLock(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Unlock() })

	_, statErr := os.Stat(path)
	require.NoError(t, statErr)
}

func TestLock_WaitsUntilReleased(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.lock")

	first, err := flock.TryLock(path)
	require.NoError(t, err)

	go func() {
		time.Sleep(100 * time.Millisecond)
		_ = first.Unlock()
	}()

	started := time.Now()
	second, err := flock.LockWait(path, 5*time.Second)
	require.NoError(t, err)
	require.GreaterOrEqual(t, time.Since(started), 100*time.Millisecond)
	require.NoError(t, second.Unlock())
}

func TestLock_TimesOut(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.lock")

	first, err := flock.TryLock(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = first.Unlock() })

	_, err = flock.LockWait(path, 50*time.Millisecond)
	require.ErrorIs(t, err, flock.ErrTimeout)
}

func TestLock_ZeroTimeout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.lock")

	first, err := flock.TryLock(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = first.Unlock() })

	_, err = flock.LockWait(path, 0)
	require.True(t, errors.Is(err, flock.ErrLocked) || errors.Is(err, flock.ErrTimeout), "expected lock error, got %v", err)
}
