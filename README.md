# Claude Code Model Switcher

A single Go binary (`ccms`) for switching Claude Code LLM providers: the CLI
switches profiles, the embedded daemon translates the Anthropic Messages API
into the active provider's protocol. A rewrite of the bash tool
[`ccx`](../ccx) (the behavior reference) with no external proxies: ccr,
copilot-api and copilot-bridge are replaced by built-in translators,
claude-codex by a built-in codex client.

## Protocols (profile `protocol` field)

| protocol | Upstream | Replaces |
|---|---|---|
| `anthropic` | direct Anthropic-compatible API (z.ai, …) | — (passthrough) |
| `openai` | OpenAI chat/completions (api.openai.com, corporate gateways) | claude-code-router |
| `copilot` | GitHub Copilot (OAuth device flow + prefill fix) | copilot-api + copilot-bridge |
| `codex` | ChatGPT Responses API (subscription) | claude-codex |

## Quick start

```bash
make install                      # build + /opt/homebrew/bin/ccms (PREFIX=…)
ccms init                         # v1 config migration or import + templates
ccms list                         # profiles and models
ccms auth copilot                 # GitHub OAuth (for the copilot profile)
ccms use work                     # switch (the daemon starts automatically)
ccms use zai                      # switch to an anthropic profile — direct, no daemon
ccms status                       # profile/model/daemon
```

How `ANTHROPIC_BASE_URL` ends up in `~/.claude/settings.json` depends on the
profile's protocol: translated ones (openai/copilot/codex) point at the local
daemon (`http://127.0.0.1:4567`) and switching re-routes the daemon on the
fly; an anthropic passthrough goes **direct** — the upstream `base_url` and
credential are written straight into the config (bash-ccx semantics) and the
unused daemon is stopped.

## Config (`~/.claude/providers.json`, v2, 0600 permissions)

```jsonc
{
  "version": 2,
  "daemon": {"port": 4567, "auth_token": "<hex>"},
  "profiles": {
    "zai": {
      "name": "z.ai (GLM)",
      "protocol": "anthropic",
      "anthropic": {"base_url": "https://api.z.ai/api/anthropic", "auth_token": "<key>"},
      "models": [{"id": "glm-5.3[1m]", "label": "GLM 5.3", "default": true}]
    },
    "work": {
      "name": "Work gateway",
      "protocol": "openai",
      "openai": {"base_url": "https://…/v1", "api_key": "<token>", "user_agent": "claude-cli/2.0.1"},
      "models": […]
    }
  }
}
```

## Commands

`ccms` (menu) · `ccms use <profile> [model]` (alias `ccms <profile>`) · `list` ·
`status` · `init [--force]` · `edit` · `serve` (daemon in foreground) ·
`stop` · `auth copilot` · `auth codex` · `version` · `help`

## Development

```bash
make test        # tests with coverage (threshold ≥80%)
make lint        # golangci-lint on changed files
make lint-full   # on all files
make generate    # mocks + formatting
```

Structure and conventions — see CLAUDE.md (modeled after idp-port-forward:
one file per method/handler, foo_test.go, suite provider in
internal/pkg/suite/provider).
