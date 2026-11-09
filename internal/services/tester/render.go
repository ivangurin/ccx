// render.go — the live view of a running probe: providers with their models
// repaint in place (ANSI cursor-up + clear, the tui frame trick) while the
// run progresses; a progress bar rides at the bottom. A non-TTY writer gets
// the same picture WITHOUT any repaint or color: lines append as results
// land (piping ccx test stays clean text).
package tester

import (
	"fmt"
	"io"
	"strings"
)

// ANSI color codes (SGR): green for pass, red for fail, yellow for running.
const (
	sgrGreen   = "\x1b[32m"
	sgrRed     = "\x1b[31m"
	sgrYellow  = "\x1b[33m"
	sgrReset   = "\x1b[0m"
	barWidth   = 24
	glyphRun   = "…"
	glyphOK    = "✓"
	glyphFail  = "✗"
	linePrefix = "  "
)

// Renderer paints the Live state to w. TTY and non-TTY writers share one
// state machine: a TTY repaints the whole frame on every update, a pipe
// appends only the lines that became final.
type Renderer struct {
	w        io.Writer
	tty      bool
	prevHi   int                      // lines painted by the previous TTY frame (repaint distance)
	printed  map[*ProviderResult]bool // non-TTY: provider headers already on paper
	printedM map[ModelKey]bool        // non-TTY: model lines already on paper
}

// ModelKey identifies a model line across updates (pointer identity of the
// result is stable — the slice never reallocates after append).
type ModelKey struct {
	p *ProviderResult
	i int
}

// NewRenderer builds the renderer; tty must reflect whether w is a terminal
// (caller detects it — the tester stays tty-agnostic for tests).
func NewRenderer(w io.Writer, tty bool) *Renderer {
	return &Renderer{
		w:        w,
		tty:      tty,
		printed:  map[*ProviderResult]bool{},
		printedM: map[ModelKey]bool{},
	}
}

// Update repaints the frame (TTY) or appends newly final lines (pipe).
func (r *Renderer) Update(live *Live) {
	if r.tty {
		r.repaint(live)

		return
	}
	r.append(live)
}

// Finish prints the summary line (both modes; TTY prints after the last
// frame so the cursor lands below it).
func (r *Renderer) Finish(report *Report) {
	if r.tty {
		fmt.Fprint(r.w, "\r\n")
	}
	fmt.Fprintf(r.w, "%d/%d providers ok, %d/%d models ok\n",
		report.ProvidersOK, report.TotalProviders, report.ModelsOK, report.TotalModels)
}

// repaint — the TTY frame: climb to the frame's first line and clear below,
// then paint the whole current state. The first paint has nothing above.
func (r *Renderer) repaint(live *Live) {
	var sb strings.Builder
	if r.prevHi > 0 {
		fmt.Fprintf(&sb, "\r\x1b[%dA\x1b[J", r.prevHi)
	}
	r.prevHi = paint(&sb, live)
	_, _ = io.WriteString(r.w, sb.String())
}

// append — the non-TTY flow: only final lines, in order, no escape codes.
// A running provider header is not final, but its line is stable (key +
// protocol are known at start) — pipe readers see progress immediately.
func (r *Renderer) append(live *Live) {
	var sb strings.Builder
	for _, p := range live.Providers {
		if !r.printed[p] {
			// pipe flow: a line prints once, in its FINAL state — the
			// provider header waits for the provider to finish (running →
			// ✓/✗), models wait for their probe
			if p.Status == StatusRunning {
				continue
			}
			fmt.Fprintf(&sb, "%s%s\n", r.line(p), errDash(p.Err))
			r.printed[p] = true
		}
		for i := range p.Models {
			key := ModelKey{p: p, i: i}
			if r.printedM[key] {
				continue
			}
			m := &p.Models[i]
			if m.Status == StatusRunning {
				continue
			}
			fmt.Fprintf(&sb, "%s%s%s\n", linePrefix, r.line(m), errDash(m.Err))
			r.printedM[key] = true
		}
	}
	_, _ = io.WriteString(r.w, sb.String())
}

// Line is the provider's stable label: key [protocol].
func (p *ProviderResult) Line() string {
	return fmt.Sprintf("%s [%s]", p.Key, p.Protocol)
}

// errDash formats a non-nil error as " — text" (empty otherwise).
func errDash(err error) string {
	if err == nil {
		return ""
	}

	return fmt.Sprintf(" — %v", err)
}

// paint writes the whole frame; returns the line count (for the next
// repaint's climb). Statuses are SGR-colored — the TTY frame is the only
// caller (the pipe path appends per-line instead).
func paint(sb *strings.Builder, live *Live) int {
	lines := 0
	for _, p := range live.Providers {
		fmt.Fprintf(sb, "%s%s\n", ttyLine(p), errDash(p.Err))
		lines++
		for i := range p.Models {
			m := &p.Models[i]
			fmt.Fprintf(sb, "%s%s%s\n", linePrefix, ttyLine(m), errDash(m.Err))
			lines++
		}
	}
	// a blank line separates the list from the bar (the final summary prints
	// under the bar's last frame)
	fmt.Fprint(sb, "\n")
	lines++
	fmt.Fprintf(sb, "%s %d/%d providers\r", progressBar(live), live.Done, live.Total)
	lines++

	return lines
}

// line — glyph + text, NO color (the pipe path; escape codes in a
// redirected stream are noise for grep/less).
func (r *Renderer) line(res interface{ status() (Status, string) }) string {
	s, text := res.status()

	return glyph(s) + " " + text
}

// ttyLine — glyph + colored text for the live frame (glyph inside the
// color: a red ✗ and a green ✓ must read as one token).
func ttyLine(res interface{ status() (Status, string) }) string {
	s, text := res.status()

	return sgrFor(s) + glyph(s) + " " + text + sgrReset
}

// sgrFor picks the status color: running yellow, pass green, fail red.
func sgrFor(s Status) string {
	switch s {
	case StatusOK:
		return sgrGreen
	case StatusFail:
		return sgrRed
	}

	return sgrYellow
}

// statusModel adapts ModelResult to the status/text pair.
func (m *ModelResult) status() (Status, string) { return m.Status, m.ID }

// statusProvider adapts ProviderResult to the status/text pair.
func (p *ProviderResult) status() (Status, string) { return p.Status, p.Line() }

// glyph — the text icon per status: all single-width, consistent.
func glyph(s Status) string {
	switch s {
	case StatusOK:
		return glyphOK
	case StatusFail:
		return glyphFail
	}

	return glyphRun
}

// progressBar — a filled/empty ASCII bar, no color (it repaints fast).
func progressBar(live *Live) string {
	if live.Total == 0 {
		return "[" + strings.Repeat("░", barWidth) + "]"
	}
	done := live.Done * barWidth / live.Total
	if live.Done > 0 && done == 0 {
		done = 1
	}

	return "[" + strings.Repeat("█", done) + strings.Repeat("░", barWidth-done) + "]"
}
