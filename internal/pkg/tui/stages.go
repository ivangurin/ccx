package tui

import (
	"bufio"
	"io"
	"os"

	"golang.org/x/term"
)

// Stage — one menu level: header, items and the initial cursor position.
type Stage struct {
	Header     string
	Items      []Item
	DefaultIdx int
}

// NextStage — builds the level that follows the chosen path (one item index
// per visited level); ok=false — the chain is complete, the path is the result.
type NextStage func(chosen []int) (Stage, bool)

// SelectStaged — multi-level in-place selection: Enter descends to the next
// level (its frame replaces the previous one on screen), Esc ascends one
// level back (Esc on the first level cancels the whole session). Returns the
// chosen path — one item index per level. Without a terminal — the numbered
// fallback level by level (numbered input has no way back).
func SelectStaged(next NextStage, in io.Reader, out io.Writer) ([]int, error) {
	stage, ok := next(nil)
	if !ok || len(stage.Items) == 0 {
		return nil, errEmptyList
	}
	f, isFile := in.(*os.File)
	if !isFile || !term.IsTerminal(int(f.Fd())) {
		return fallbackStaged(next, stage, bufio.NewReader(in), out)
	}

	return runSession(next, stage, f, out)
}

// fallbackStaged — numbered selection without a terminal: level by level;
// an interrupted input cancels the whole chain.
func fallbackStaged(next NextStage, stage Stage, in *bufio.Reader, out io.Writer) ([]int, error) {
	var chosen []int
	for ok := true; ok; {
		if len(stage.Items) == 0 {
			return nil, errEmptyList
		}
		idx, err := FallbackSelect(stage.Items, stage.DefaultIdx, in, out)
		if err != nil {
			return nil, err
		}
		chosen = append(chosen, idx)
		stage, ok = next(chosen)
	}

	return chosen, nil
}
