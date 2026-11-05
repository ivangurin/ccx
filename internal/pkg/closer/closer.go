// Package closer — LIFO-гаситель ресурсов на shutdown: последние добавленные
// гасятся первыми (http раньше бизнес-горутин), ошибки собираются в одну,
// повторный Close — no-op, Add после Close выполняет fn немедленно (ресурс
// не утекает, если появился между shutdown-проходами).
package closer

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// Closer собирает функции закрытия и гасит их в обратном порядке.
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

// New создаёт пустой Closer.
func New() *Closer {
	return &Closer{}
}

// Add регистрирует функцию закрытия с именем (для диагностики ошибок).
func (c *Closer) Add(name string, fn func() error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		// shutdown уже прошёл — гасим немедленно, иначе ресурс утечёт до выхода
		if err := fn(); err != nil {
			c.closeErr = errors.Join(c.closeErr, fmt.Errorf("closer: %s (late): %w", name, err))
		}

		return
	}
	c.fns = append(c.fns, closeFn{name: name, fn: fn})
}

// Close гасит все функции в LIFO-порядке. Ожидание каждой fn ограничено ctx:
// блокирующаяся fn остаётся работать в фоне (прервать её извне нельзя), но
// Close не зависает — это граница graceful shutdown.
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
			// оставшиеся не гасим: shutdown уже сорван по таймауту
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
