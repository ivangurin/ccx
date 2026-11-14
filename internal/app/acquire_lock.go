package app

import (
	"os"
	"path/filepath"
	"time"

	"github.com/ivangurin/ccx/internal/pkg/flock"
)

// lockRetry is the acquireLock retry window (a var: tests shorten it — the
// default makes a lock-held test wait out the full 5s).
var lockRetry = 5 * time.Second

// SetLockRetryForTest overrides the lock retry window; ResetLockRetryForTest
// restores it.
func SetLockRetryForTest(d time.Duration) { lockRetry = d }

// ResetLockRetryForTest restores the default lock retry window.
func ResetLockRetryForTest() { lockRetry = 5 * time.Second }

// acquireLock takes daemon.lock with a bounded retry: the previous daemon may
// hold it for a moment while its graceful shutdown drains in-flight requests.
func (a *App) acquireLock() (*flock.Lock, error) {
	home := a.sp.GetConfig().Home
	// a fresh install may not have the ccx home yet — the lock lives there
	_ = os.MkdirAll(home, 0o750)

	deadline := time.Now().Add(lockRetry)
	for {
		lock, err := flock.TryLock(filepath.Join(home, "daemon.lock"))
		if err == nil {
			return lock, nil
		}
		if time.Now().After(deadline) {
			return nil, err
		}
		time.Sleep(100 * time.Millisecond)
	}
}
