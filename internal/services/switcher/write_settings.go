package switcher

import (
	"fmt"
	"slices"
	"strings"

	"github.com/ivangurin/ccx/internal/models"
	"github.com/ivangurin/ccx/internal/pkg/jsonfile"
)

// writeSettings: backup → surgical env patch (managed keys) → model pinning.
// env is the fresh block content (daemonEnv or directEnv). The patcher edits
// the file's own text, so everything outside env/the pin — order, indent,
// user blocks — survives byte-for-byte; fresh keys land at the end of the
// env block.
func (s *Switcher) writeSettings(env map[string]string, model string) error {
	settings, err := models.LoadSettings(s.cfg.SettingsPath())
	if err != nil {
		return fmt.Errorf("ccx: no %s — it is created by the first run of Claude Code", s.cfg.SettingsPath())
	}

	if err := backupSettings(s.cfg, settings); err != nil {
		return err
	}

	currentEnv := settings.Env()
	stale := staleEnvKeys(currentEnv, env)

	// always-env: present in every block ccx writes, but added ONLY when the
	// final env would lack the key — a value already in the file (the user's
	// own) wins and is never overwritten. staleEnvKeys never lists an
	// always-env key (they are neither managed nor ANTHROPIC_*), so a stale
	// wipe can never mask a default here.
	for k, v := range alwaysEnv {
		if _, ok := env[k]; ok {
			continue
		}
		if _, ok := currentEnv[k]; ok {
			continue
		}
		env[k] = v
	}

	if err := settings.PatchEnv(stale, env); err != nil {
		return fmt.Errorf("ccx: %w", err)
	}
	if err := settings.SetModelPin(model); err != nil {
		return fmt.Errorf("ccx: %w", err)
	}
	// WriteAtomicRaw (not a local copy): settings.json is patched as TEXT and
	// the shared helper's perm policy keeps the file's existing perms — the
	// file belongs to Claude Code, not to ccx, and a switch must not
	// silently tighten it.
	if err := jsonfile.WriteAtomicRaw(s.cfg.SettingsPath(), settings.Bytes(), 0o600); err != nil {
		return fmt.Errorf("ccx: writing settings.json: %w", err)
	}

	return nil
}

// alwaysEnv — flags ccx guarantees on every switch: nonessential telemetry
// traffic off, agent teams on, the auto-mode server off. Add-if-missing only
// (see writeSettings): ccx never overwrites a value it does not own.
var alwaysEnv = map[string]string{
	"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
	"CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS":     "1",
	"CLAUDE_CODE_AUTO_MODE_SERVER":             "0",
}

// contextKeys — the CLAUDE_CODE_* trio derived from max_context_tokens:
// written only for a capped model, wiped otherwise (a manual value dies too —
// the window cap is a per-model fact, not a user preference).
var contextKeys = []string{
	"CLAUDE_CODE_DISABLE_1M_CONTEXT",
	"CLAUDE_CODE_MAX_CONTEXT_TOKENS",
	"CLAUDE_CODE_AUTO_COMPACT_WINDOW",
}

// effortKey — CLAUDE_CODE_EFFORT_LEVEL, derived from the per-model effort
// field: written verbatim (no allow-list) only for a model that carries it,
// wiped otherwise (ccx-owned like the context trio — a manual value dies
// too).
var effortKey = "CLAUDE_CODE_EFFORT_LEVEL"

// staleEnvKeys — the keys to wipe: the managed (ccx-owned) context trio and
// effort key plus every ANTHROPIC_* key that is present now but absent from
// the fresh block; all ANTHROPIC_* are wiped, not only managed ones (past
// profile keys and manual values must not survive a switch — parity with
// bash with_entries). Profile env keys are gone — a user key is simply never
// touched.
func staleEnvKeys(current, fresh map[string]string) []string {
	managed := map[string]bool{}
	for _, k := range contextKeys {
		managed[k] = true
	}
	managed[effortKey] = true

	var stale []string
	for k := range current {
		if _, keep := fresh[k]; keep {
			continue
		}
		if managed[k] || strings.HasPrefix(k, "ANTHROPIC_") {
			stale = append(stale, k)
		}
	}
	slices.Sort(stale)

	return stale
}
