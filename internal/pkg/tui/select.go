// Package tui — встроенное меню выбора (стрелки/Enter/фильтр) без внешнего
// fzf: единый бинарник самодостаточен. Без терминала — нумерованный fallback
// (порт pick_from_list bash-ccx).
package tui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// Item — строка меню: Label — основная, Detail — вторичная (модель/протокол).
type Item struct {
	Label  string
	Detail string
}

// Filter отбирает элементы по подстроке (case-insensitive, по Label и Detail).
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

// Select — интерактивный выбор; in/out позволяют тестировать fallback.
// Терминал (raw mode): ↑↓/Ctrl-P/N, Enter — выбор, Esc/Ctrl-C — отмена,
// остальной ввод — фильтр. Не-терминал → FallbackSelect.
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

// FallbackSelect — нумерованный выбор без TUI: ввод номера, пусто = default.
func FallbackSelect(items []Item, defaultIdx int, in *bufio.Reader, out io.Writer) (int, error) {
	fmt.Fprintln(out, "Выберите (Enter = по умолчанию):")
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
		fmt.Fprintf(out, "Введите число 1..%d\n", len(items))
	}
}
