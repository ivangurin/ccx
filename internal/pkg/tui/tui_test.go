package tui_test

import (
	"bufio"
	"strings"
	"testing"

	"github.com/ivangurin/ccx/internal/pkg/tui"
	"github.com/stretchr/testify/require"
)

func TestFilter_CaseInsensitive(t *testing.T) {
	items := []tui.Item{
		{Label: "zai · z.ai (GLM)"},
		{Label: "x5 · Рабочий шлюз"},
		{Label: "copilot · GitHub Copilot"},
	}

	got := tui.Filter(items, "ZAI")
	require.Len(t, got, 1)
	require.Equal(t, items[0], got[0])

	got = tui.Filter(items, "коп")
	require.Empty(t, got) // кириллица не матчится по латинице — и не должна

	got = tui.Filter(items, "")
	require.Len(t, got, 3)
}

func TestFilter_MatchesDetail(t *testing.T) {
	items := []tui.Item{
		{Label: "zai", Detail: "glm-5.3"},
		{Label: "x5", Detail: "copilot-code-large"},
	}

	got := tui.Filter(items, "glm")
	require.Len(t, got, 1)
	require.Equal(t, "zai", got[0].Label)
}

func TestFallbackSelect_ByNumber(t *testing.T) {
	items := []tui.Item{{Label: "a"}, {Label: "b"}, {Label: "c"}}
	in := bufio.NewReader(strings.NewReader("2\n"))

	idx, err := tui.FallbackSelect(items, 0, in, &strings.Builder{})
	require.NoError(t, err)
	require.Equal(t, 1, idx)
}

func TestFallbackSelect_EmptyInputSelectsDefault(t *testing.T) {
	items := []tui.Item{{Label: "a"}, {Label: "b"}}
	in := bufio.NewReader(strings.NewReader("\n"))

	idx, err := tui.FallbackSelect(items, 1, in, &strings.Builder{})
	require.NoError(t, err)
	require.Equal(t, 1, idx)
}

func TestFallbackSelect_ReasksOnGarbage(t *testing.T) {
	items := []tui.Item{{Label: "a"}, {Label: "b"}}
	in := bufio.NewReader(strings.NewReader("9\nxyz\n1\n"))
	var out strings.Builder

	idx, err := tui.FallbackSelect(items, 0, in, &out)
	require.NoError(t, err)
	require.Equal(t, 0, idx)
	require.Contains(t, out.String(), "2") // подсказка диапазона
}

func TestSelect_FallbackWhenNotTTY(t *testing.T) {
	// stdin — ридер, не терминал: Select проваливается в нумерованный выбор
	items := []tui.Item{{Label: "a"}, {Label: "b"}}
	in := strings.NewReader("1\n")

	idx, err := tui.Select("Заголовок", items, 0, in, &strings.Builder{})
	require.NoError(t, err)
	require.Equal(t, 0, idx)
}
