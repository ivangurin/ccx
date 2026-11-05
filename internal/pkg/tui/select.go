// Package tui — a built-in selection menu (arrows/Enter/filter) without an
// external fzf: the single binary is self-sufficient. Without a terminal —
// a numbered fallback (a port of bash-ccx pick_from_list).
package tui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// Item — a menu row: Label is the primary text, Detail the secondary one
// (model/protocol).
type Item struct {
	Label  string
	Detail string
}

// Filter selects items by substring (case-insensitive, over Label and Detail).
func Filter(items []Item, query string) []Item {
	if query == "" {
		return items
	}
	q := strings.ToLower(query)
	out := make([]Item, 0, len(items))
	for _, it := range items {
		if strings.Contains(strings.ToLower(it.Label), q) || strings.Contains(strings.ToLower(it.Detail), q) {
			out = append(out, it)
		}
	}

	return out
}

// Select — interactive selection; in/out make the fallback testable.
// Terminal (raw mode): ↑↓/Ctrl-P/N, Enter — select, Esc/Ctrl-C — cancel,
// any other input — filter. Non-terminal → FallbackSelect.
func Select(header string, items []Item, defaultIdx int, in io.Reader, out io.Writer) (int, error) {
	if len(items) == 0 {
		return -1, errEmptyList
	}
	f, isFile := in.(*os.File)
	if !isFile || !term.IsTerminal(int(f.Fd())) {
		return FallbackSelect(items, defaultIdx, bufio.NewReader(in), out)
	}

	return runInteractive(header, items, defaultIdx, f, out)
}

// FallbackSelect — numbered selection without a TUI: enter a number, empty = default.
func FallbackSelect(items []Item, defaultIdx int, in *bufio.Reader, out io.Writer) (int, error) {
	fmt.Fprintln(out, "Choose (Enter = default):")
	for i, it := range items {
		mark := "  "
		if i == defaultIdx {
			mark = "→ "
		}
		detail := ""
		if it.Detail != "" {
			detail = " · " + it.Detail
		}
		fmt.Fprintf(out, "%s%d) %s%s\n", mark, i+1, it.Label, detail)
	}
	for {
		fmt.Fprint(out, "> ")
		line, err := in.ReadString('\n')
		if err != nil && line == "" {
			return -1, errInterrupted
		}
		line = strings.TrimSpace(line)
		if line == "" {
			return defaultIdx, nil
		}
		var n int
		if _, err := fmt.Sscanf(line, "%d", &n); err == nil && n >= 1 && n <= len(items) {
			return n - 1, nil
		}
		fmt.Fprintf(out, "Enter a number 1..%d\n", len(items))
	}
}
