// Package closer — LIFO resource quencher on shutdown: last added is closed
// first (http before business goroutines), errors are joined into one,
// a repeated Close is a no-op, Add after Close runs fn immediately (a resource
// does not leak if it appeared between shutdown passes).
package closer

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// Closer collects close functions and runs them in reverse order.
type Closer struct {
	mu       sync.Mutex
	fns      []closeFn
	closed   bool
	closeErr error
}

type closeFn struct {
	name string
	fn   func() error
}

// New creates an empty Closer.
func New() *Closer {
	return &Closer{}
}

// Add registers a close function with a name (for error diagnostics).
func (c *Closer) Add(name string, fn func() error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		// shutdown has already passed — quench immediately, or the resource leaks until exit
		if err := fn(); err != nil {
			c.closeErr = errors.Join(c.closeErr, fmt.Errorf("closer: %s (late): %w", name, err))
		}

		return
	}
	c.fns = append(c.fns, closeFn{name: name, fn: fn})
}

// Close runs all functions in LIFO order. Waiting for each fn is bounded by
// ctx: a blocked fn keeps running in the background (it cannot be interrupted
// from outside), but Close does not hang — this is the graceful shutdown
// boundary.
func (c *Closer) Close(ctx context.Context) error {
	c.mu.Lock()
	if c.closed {
		err := c.closeErr
		c.mu.Unlock()

		return err
	}
	c.closed = true
	fns := c.fns
	c.fns = nil
	c.mu.Unlock()

	var errs []error
	for i := len(fns) - 1; i >= 0; i-- {
		if err := ctx.Err(); err != nil {
			errs = append(errs, fmt.Errorf("closer: canceled before %s: %w", fns[i].name, err))

			break
		}
		type result struct{ err error }
		done := make(chan result, 1)
		go func(fn closeFn) { done <- result{fn.fn()} }(fns[i])
		select {
		case res := <-done:
			if res.err != nil {
				errs = append(errs, fmt.Errorf("closer: %s: %w", fns[i].name, res.err))
			}
		case <-ctx.Done():
			errs = append(errs, fmt.Errorf("closer: %s: %w", fns[i].name, ctx.Err()))
			// the rest are not quenched: shutdown is already broken by timeout
			c.mu.Lock()
			c.closeErr = errors.Join(errs...)
			c.mu.Unlock()

			return c.closeErr
		}
	}
	c.mu.Lock()
	c.closeErr = errors.Join(errs...)
	c.mu.Unlock()

	return c.closeErr
}
