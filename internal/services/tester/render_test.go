package tester_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/ivangurin/ccx/internal/services/tester"
	"github.com/stretchr/testify/require"
)

func newLive() (*tester.Live, *tester.ProviderResult) {
	p := &tester.ProviderResult{Key: "zai", Protocol: "anthropic"}
	live := &tester.Live{Providers: []*tester.ProviderResult{p}, Total: 1}

	return live, p
}

func TestRender_TTY_RunningFrame(t *testing.T) {
	live, _ := newLive()
	var out bytes.Buffer
	r := tester.NewRenderer(&out, true)
	r.Update(live)

	s := out.String()
	require.Contains(t, s, "… zai [anthropic]")
	// yellow running glyph, the progress bar at the bottom
	require.Contains(t, s, "\x1b[33m")
	// nothing done yet — the bar is empty
	require.Contains(t, s, "[░░░░░░░░░░░░░░░░░░░░░░░░] 0/1 providers")
	// the frame ends with \r (the cursor stays on the bar line for repaint)
	require.True(t, strings.HasSuffix(s, "providers\r"))
}

func TestRender_TTY_RepaintClimbsAndCompletes(t *testing.T) {
	live, p := newLive()
	var out bytes.Buffer
	r := tester.NewRenderer(&out, true)
	r.Update(live)

	// the provider finishes ok with one passing model
	p.Status = tester.StatusOK
	p.Models = []tester.ModelResult{{ID: "glm", Status: tester.StatusOK}}
	live.Done = 1
	out.Reset()
	r.Update(live)

	s := out.String()
	// the repaint climbs back to the frame's first line and clears below
	require.Contains(t, s, "\r\x1b[")
	require.Contains(t, s, "\x1b[J")
	require.Contains(t, s, "\x1b[32m✓ zai [anthropic]\x1b[0m")
	require.Contains(t, s, "\x1b[32m✓ glm\x1b[0m")
	require.Contains(t, s, "1/1 providers")
}

func TestRender_TTY_FailLineCarriesCause(t *testing.T) {
	live, p := newLive()
	var out bytes.Buffer
	r := tester.NewRenderer(&out, true)
	r.Update(live)

	p.Status = tester.StatusFail
	p.Err = errors.New("base_url is empty")
	live.Done = 1
	out.Reset()
	r.Update(live)

	s := out.String()
	// the failure line: red glyph + red cause on the SAME line
	require.Contains(t, s, "\x1b[31m✗ zai [anthropic]\x1b[0m — base_url is empty")
	require.NotContains(t, s, "\x1b[32m")
}

func TestRender_TTY_ModelFailLineCarriesCause(t *testing.T) {
	live, p := newLive()
	p.Models = []tester.ModelResult{{ID: "glm", Status: tester.StatusFail, Err: errors.New("upstream 400: nope")}}
	p.Status = tester.StatusOK
	live.Done = 1
	var out bytes.Buffer
	r := tester.NewRenderer(&out, true)
	r.Update(live)

	s := out.String()
	require.Contains(t, s, "  \x1b[31m✗ glm\x1b[0m — upstream 400: nope")
}

func TestRender_Finish_Summary(t *testing.T) {
	live, _ := newLive()
	_ = live
	var out bytes.Buffer
	r := tester.NewRenderer(&out, false)
	r.Finish(&tester.Report{
		TotalProviders: 2, ProvidersOK: 1,
		TotalModels: 3, ModelsOK: 2,
	})

	require.Equal(t, "1/2 providers ok, 2/3 models ok\n", out.String())
}

func TestRender_ProgressBar_Fractional(t *testing.T) {
	live := &tester.Live{Total: 3, Done: 1}
	var out bytes.Buffer
	r := tester.NewRenderer(&out, true)
	r.Update(live)

	// 1/3 of 24 cells = 8 filled
	require.Contains(t, out.String(), "[████████░░░░░░░░░░░░░░░░] 1/3 providers")
}

func TestRender_ProgressBar_FirstTickNotZero(t *testing.T) {
	live := &tester.Live{Total: 100, Done: 1}
	var out bytes.Buffer
	r := tester.NewRenderer(&out, true)
	r.Update(live)

	// 1/100 rounds to 0 cells — clamped to 1 so the first tick is visible
	require.Contains(t, out.String(), "[█")
}
