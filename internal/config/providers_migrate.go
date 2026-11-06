package config

import (
	"encoding/json"

	"github.com/ivangurin/ccms/internal/models"
)

// hasLegacyProvidersResidue reports whether the RAW bytes carry pre-rename
// markers: a top-level "profiles" key, a "version" field, or the dead
// daemon.auth_token (the v3 daemon secret — stripping it from disk on sight
// is the point: the daemon holds no secret of its own since openai went
// pass-through). The decoder silently tolerates all three, so only a raw
// scan can see them.
func hasLegacyProvidersResidue(data []byte) bool {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return false // unreachable after a successful struct decode; defensive
	}
	if _, ok := top["profiles"]; ok {
		return true
	}
	if _, ok := top["version"]; ok {
		return true
	}
	if raw, ok := top["daemon"]; ok {
		var d map[string]json.RawMessage
		if json.Unmarshal(raw, &d) == nil {
			_, ok := d["auth_token"]

			return ok
		}
	}

	return false
}

// migrateProvidersFile rewrites providers.json in the current schema
// ("providers" key, no version, no daemon.auth_token, no empty daemon
// block), preserving profile order and fields. Best-effort BY DESIGN: a
// write failure (read-only dir, full disk) is swallowed — the legacy layout
// loads forever, so a failed migration costs only a stale file, while
// failing the load would break switching over cosmetics; it retries for
// free on every next load. Concurrency (CLI + daemon migrating at once)
// needs no flock: SaveProviders renames a unique tmp into place, so readers
// always see a complete file and both migrators derive identical content
// from the same legacy bytes (last rename wins; after the first success the
// file is clean and nothing migrates again). The one residual window — a
// ccms edit/init --force landing between our read and rename — is a NEW
// pairing (read paths never wrote this file before) at pre-existing
// severity: last-writer-wins between two atomic writes, and the loser is
// a re-runnable init or the editor's own save.
func migrateProvidersFile(path string, p *models.Providers) {
	_ = SaveProviders(path, p)
}
