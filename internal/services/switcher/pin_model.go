package switcher

import (
	"strconv"

	"github.com/ivangurin/ccx/internal/models"
)

// pinnedKeys — the model pin env vars. Deliberately a single mapping: the
// legacy ANTHROPIC_DEFAULT_*_MODEL / ANTHROPIC_SMALL_FAST_MODEL siblings are
// not written anymore, and a leftover one in settings.json dies on the next
// switch via the ANTHROPIC_* stale sweep in writeSettings.
var pinnedKeys = []string{
	"ANTHROPIC_MODEL",
}

// pinModel writes the model pin; a model with max_context_tokens additionally
// caps the window Claude Code believes it has: the 1M context is disabled,
// the window pinned, the auto-compact threshold set 10% below the cap (room
// to compact BEFORE the hard limit). A model with effort additionally pins
// CLAUDE_CODE_EFFORT_LEVEL verbatim (no validation — the value passes
// through). Unset facts ⇒ the corresponding keys are not written;
// writeSettings wipes any stale copy (the keys are ccx-owned).
func pinModel(env map[string]string, model *models.Model) {
	for _, key := range pinnedKeys {
		env[key] = model.ID
	}
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
