// Package readguard arms read deadlines on an upstream response body: a
// first-byte deadline until the first byte arrives, an idle deadline between
// reads. Without them a hung gateway (connection accepted, nothing ever
// sent — corporate proxies do this) leaves the client request open forever:
// the SSE client has no timeout of its own, and a global http.Client.Timeout
// is not an option either — it would cap whole generations.
//
// Client-side response bodies expose no net.Conn to SetReadDeadline on, so
// the guard is a watchdog: a timer whose expiry cancels the request context
// (unblocking the in-flight Read); every successful read re-arms it.
package readguard

import (
	"context"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// Deadlines used by the translators. Vars (not consts) so tests can shrink
// them. FirstByte covers a streamed request's time to the first token (a
// queued 200k-token prompt legitimately takes over a minute); a NON-stream
// response's first body byte only arrives when the generation is COMPLETE,
// so its deadline is a total-time cap instead — NonStream is therefore a
// whole-generation budget (10 minutes), not a first-token patience.
var (
	FirstByte = 120 * time.Second
	Idle      = 120 * time.Second
	NonStream = 600 * time.Second
)

// FirstByteFor picks the first-byte deadline for a request: the first-token
// patience when the client streams, the whole-generation budget when it does
// not (a non-stream response's first byte arrives only when the generation
// is complete).
func FirstByteFor(stream bool) time.Duration {
	if stream {
		return FirstByte
	}

	return NonStream
}

// Guard is the watchdog for one upstream request. Arm it before building the
// request (the returned context must travel on it), attach the body after the
// response, and Stop when the exchange is over — including the paths that
// never get a response.
type Guard struct {
	cancel    context.CancelFunc
	resetCh   chan time.Duration
	done      chan struct{}
	stopOnce  sync.Once
	timedOut  atomic.Bool
	firstByte time.Duration
	idle      time.Duration
}

// Arm starts the watchdog and returns the request context to use upstream.
func Arm(parent context.Context, firstByte, idle time.Duration) (context.Context, *Guard) {
	ctx, cancel := context.WithCancel(parent)
	g := &Guard{
		cancel:    cancel,
		resetCh:   make(chan time.Duration, 1),
		done:      make(chan struct{}),
		firstByte: firstByte,
		idle:      idle,
	}
	go g.watch(firstByte)

	return ctx, g
}

// watch runs the deadline loop: the timer starts at firstByte and each reset
// channel value re-arms it (the read side sends the next phase's deadline —
// firstByte until the first byte, idle afterwards). The channel is buffered
// and coalescing: a stale larger deadline may extend one wait by at most the
// first-byte span, which is still bounded.
func (g *Guard) watch(firstByte time.Duration) {
	timer := time.NewTimer(firstByte)
	defer timer.Stop()
	for {
		select {
		case d := <-g.resetCh:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(d)
		case <-timer.C:
			g.timedOut.Store(true)
			g.cancel()

			return
		case <-g.done:
			return
		}
	}
}

// Err wraps a transport error from the armed request: a watchdog expiry
// becomes the named first-byte timeout (headers never arrived — the deadline
// could only have been the first-byte one), anything else passes through.
func (g *Guard) Err(err error) error {
	if err != nil && g.timedOut.Load() {
		return fmt.Errorf("upstream first byte timeout after %s: %w", g.firstByte, err)
	}

	return err
}

// Body attaches the response body to the guard: reads pass through, each
// successful read re-arms the idle deadline, and a watchdog expiry surfaces
// as a named timeout error instead of a bare context.Canceled.
func (g *Guard) Body(body io.ReadCloser) io.ReadCloser {
	return &guardedBody{g: g, body: body}
}

// Stop ends the guard: the watchdog goroutine exits and the armed context is
// canceled. Safe to call more than once, from any goroutine.
func (g *Guard) Stop() {
	g.stopOnce.Do(func() {
		g.cancel()
		close(g.done)
	})
}

type guardedBody struct {
	g        *Guard
	body     io.ReadCloser
	gotFirst bool
}

func (b *guardedBody) Read(p []byte) (int, error) {
	n, err := b.body.Read(p)
	if n > 0 {
		b.gotFirst = true
		// re-arm: the idle deadline replaces the first-byte one after the
		// first byte; the coalescing channel drops redundant resets
		select {
		case b.g.resetCh <- b.nextDeadline():
		default:
		}
	}
	if err != nil && b.g.timedOut.Load() {
		return n, b.timeoutError()
	}

	return n, err
}

func (b *guardedBody) Close() error {
	return b.body.Close()
}

func (b *guardedBody) nextDeadline() time.Duration {
	if b.gotFirst {
		return b.g.idle
	}

	return b.g.firstByte
}

func (b *guardedBody) timeoutError() error {
	if !b.gotFirst {
		return fmt.Errorf("upstream first byte timeout after %s", b.g.firstByte)
	}

	return fmt.Errorf("upstream idle timeout after %s", b.g.idle)
}
