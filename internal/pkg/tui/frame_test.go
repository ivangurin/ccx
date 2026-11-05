package tui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildFrame_CountsLinesAndMarksCursor(t *testing.T) {
	items := []Item{{Label: "a"}, {Label: "b"}, {Label: "c"}}

	frame, lines := buildFrame("Заголовок", items, "", 1)
	// header + 3 пункта + footer
	require.Equal(t, 5, lines)
	require.Contains(t, frame, "Заголовок\r\n")
	require.Contains(t, frame, "> b\r\n")
	require.NotContains(t, frame, "> a")
	// последняя строка — без перевода: курсор остаётся на footer
	require.True(t, strings.HasSuffix(frame, "cancel\r"), frame)
}

func TestBuildFrame_FilterLineWhenQuery(t *testing.T) {
	frame, lines := buildFrame("h", []Item{{Label: "a"}}, "q", 0)
	// header + filter + пункт + footer
	require.Equal(t, 4, lines)
	require.Contains(t, frame, "filter: q\r\n")
}

func TestRepaintPrefix(t *testing.T) {
	// первый кадр — нечего очищать
	require.Empty(t, repaintPrefix(0))
	// 4 строки прошлого фрейма: вверх на 3 + стереть всё ниже
	require.Equal(t, "\r\x1b[3A\x1b[J", repaintPrefix(4))
}

func TestRender_RedrawsInPlace(t *testing.T) {
	var buf bytes.Buffer
	u := &ui{header: "h", items: []Item{{Label: "a"}, {Label: "b"}}, out: &buf}

	u.render()
	first := buf.String()
	require.NotContains(t, first, "\x1b[", "первый кадр рисуется без escape-очистки")

	u.cursor = 1
	u.render()
	second := buf.String()[len(first):]
	// header + 2 пункта + footer = 4 строки: курсор поднят на 3 и экран стёрт
	require.True(t, strings.HasPrefix(second, "\r\x1b[3A\x1b[J"), "второй кадр должен перезаписать первый на месте: %q", second)
	require.Contains(t, second, "> b")
}
