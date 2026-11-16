package switcher

import (
	"maps"
	"strconv"

	"github.com/ivangurin/ccx/internal/models"
)

// pinnedKeys — the env vars pinning the SELECTED model. ANTHROPIC_DEFAULT_
// MODEL is written separately (defaultModelPin): it follows the provider's
// default-flagged model, not the selection. The rest of the
// ANTHROPIC_DEFAULT_*_MODEL family is CONFIG-driven role pins (rolePins
// below) — explicit flags on model entries; the historical DERIVED
// multi-mapping (computed from the selection, plus the deprecated
// ANTHROPIC_SMALL_FAST_MODEL) stays unwritten and a leftover dies on the
// next switch via the ANTHROPIC_* stale sweep in writeSettings.
var pinnedKeys = []string{
	"ANTHROPIC_MODEL",
}

// defaultModelPin writes ANTHROPIC_DEFAULT_MODEL — Claude Code's default of
// new sessions and its /model picker's Default row (verified via docs fetch:
// that row resolves the account RUNTIME default — the settings model key and
// ANTHROPIC_MODEL do NOT feed it — and this variable redirects it): the
// provider's default-flagged model when it has one, the selected model
// otherwise.
func defaultModelPin(env map[string]string, profile *models.Profile, model *models.Model) {
	defaultID, ok := profile.DefaultModel()
	if !ok {
		defaultID = model.ID
	}
	env["ANTHROPIC_DEFAULT_MODEL"] = defaultID
}

// rolePins scans the profile's whole model list and maps each
// ANTHROPIC_DEFAULT_*_MODEL role (the Claude Code family: haiku — background
// traffic, sonnet/opus/fable — model aliases) to the id of the FIRST model
// in list order carrying the flag. Roles with no flagged model are absent
// from the result (no var written; a stale one is wiped as ANTHROPIC_*).
func rolePins(profile *models.Profile) map[string]string {
	roles := map[string]string{}
	for i := range profile.Models {
		m := &profile.Models[i]
		for _, role := range []struct {
			key string
			on  bool
		}{
			{"ANTHROPIC_DEFAULT_HAIKU_MODEL", m.Haiku},
			{"ANTHROPIC_DEFAULT_SONNET_MODEL", m.Sonnet},
			{"ANTHROPIC_DEFAULT_OPUS_MODEL", m.Opus},
			{"ANTHROPIC_DEFAULT_FABLE_MODEL", m.Fable},
		} {
			if role.on {
				if _, ok := roles[role.key]; !ok {
					roles[role.key] = m.ID
				}
			}
		}
	}

	return roles
}

// pinModel writes the model pin; a model with max_context_tokens additionally
// caps the window Claude Code believes it has: the 1M context is disabled,
// the window pinned, the auto-compact threshold set 10% below the cap (room
// to compact BEFORE the hard limit). A model with effort additionally pins
// CLAUDE_CODE_EFFORT_LEVEL verbatim (no validation — the value passes
// through). Role flags anywhere in the profile pin the
// ANTHROPIC_DEFAULT_*_MODEL family to the flagged models' ids (rolePins).
// Unset facts ⇒ the corresponding keys are not written;
// writeSettings wipes any stale copy (the keys are ccx-owned).
func pinModel(env map[string]string, profile *models.Profile, model *models.Model) {
	for _, key := range pinnedKeys {
		env[key] = model.ID
	}
	defaultModelPin(env, profile, model)
	maps.Copy(env, rolePins(profile))
	if model.MaxContextTokens > 0 {
		window := model.MaxContextTokens
		env["CLAUDE_CODE_DISABLE_1M_CONTEXT"] = "1"
		env["CLAUDE_CODE_MAX_CONTEXT_TOKENS"] = strconv.Itoa(window)
		env["CLAUDE_CODE_AUTO_COMPACT_WINDOW"] = strconv.Itoa(window - window/10)
	}
	// after the context block, and independent of it: a model may carry
	// effort without a context cap (the trio used to early-return here,
	// which made this write dead code for uncapped models — keep the
	// blocks independent)
	if model.Effort != "" {
		env[effortKey] = model.Effort
	}
}
