package tui

import (
	"fmt"
	"strings"
)

// buildFrame — полный кадр меню: header, строка фильтра (если задан), пункты,
// footer. Последняя строка без перевода: курсор остаётся на footer, чтобы
// следующий кадр отсюда вернулся к началу (перерисовка на месте).
func buildFrame(header string, visible []Item, query string, cursor int) (string, int) {
	var sb strings.Builder
	lines := 0
	fmt.Fprintf(&sb, "%s\r\n", header)
	lines++
	if query != "" {
		fmt.Fprintf(&sb, "  filter: %s\r\n", query)
		lines++
	}
	for i, it := range visible {
		mark := "  "
		if i == cursor {
			mark = "> "
		}
		detail := ""
		if it.Detail != "" {
			detail = " - " + it.Detail
		}
		fmt.Fprintf(&sb, "%s%s%s\r\n", mark, it.Label, detail)
		lines++
	}
	fmt.Fprint(&sb, "up/down select - Enter confirm - Esc cancel\r")
	lines++

	return sb.String(), lines
}

// repaintPrefix — подготовка к перерисовке на месте: курсор стоит на последней
// строке прошлого кадра (всего prevLines строк), поднимаем его к первой и
// стираем всё ниже (\x1b[J убирает хвосты, когда фильтр сузил список).
// Терминал, который шлёт стрелки (\x1b[A), умеет и эти последовательности.
// Ограничение: кадр выше окна терминала размажется — списки профилей короткие.
func repaintPrefix(prevLines int) string {
	if prevLines <= 1 {
		return ""
	}

	return fmt.Sprintf("\r\x1b[%dA\x1b[J", prevLines-1)
}
