package switcher

import (
	"context"
	"fmt"

	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/pkg/flock"
)

// Apply switches the profile; modelArg is a name or id (empty = default).
// Returns the summary line for the user.
func (s *Service) Apply(ctx context.Context, profileKey, modelArg string) (string, error) {
	return s.underSwitchLock(func() (string, error) {
		return s.applyLocked(ctx, profileKey, modelArg)
	})
}

// underSwitchLock — the one copy of the flock discipline (acquire with the
// lock-timeout wait, deferred release): Apply and RestoreOriginal both edit
// the settings pair and must not interleave with each other.
func (s *Service) underSwitchLock(fn func() (string, error)) (string, error) {
	lock, err := flock.LockWait(lockPath(s.cfg), s.cfg.LockTimeout)
	if err != nil {
		return "", fmt.Errorf("ccx: failed to acquire the switcher lock: %w", err)
	}
	defer func() { _ = lock.Unlock() }()

	return fn()
}

// lockPath is the switcher's flock file. It pairs with the daemon's
// daemon.lock: a visible <what-it-guards>.lock inside ~/.ccx. The bash-ccx
// name .ccx.lock (a hidden file with the tool's prefix) died with the rename
// of the data root — inside its own home the prefix says nothing; the old
// file stays on disk unread (the clean-break precedent).
func lockPath(cfg *config.Config) string {
	return cfg.Home + "/switch.lock"
}
