# Claude Code Model Switcher

A single Go binary (`ccms`) for switching Claude Code LLM providers: the CLI
switches profiles, the embedded daemon translates the Anthropic Messages API
into the active provider's protocol. A rewrite of the bash tool
[`ccx`](../ccx) (the behavior reference) with no external proxies: ccr,
copilot-api and copilot-bridge are replaced by built-in translators,
claude-codex by a built-in codex client.

## Install (macOS, Apple Silicon)

```bash
curl -fsSL https://raw.githubusercontent.com/ivangurin/ccms/main/install.sh | sh
```

Downloads the latest release and installs it to `/usr/local/bin/ccms` (same
destination and permissions as `make install`). Uninstall:

```bash
curl -fsSL https://raw.githubusercontent.com/ivangurin/ccms/main/uninstall.sh | sh
```

Removes the binary only — `~/.claude/providers.json` and the rest of the
setup stay untouched.

## Protocols (profile `protocol` field)

| protocol | Upstream | Replaces |
|---|---|---|
| `anthropic` | direct Anthropic-compatible API (z.ai, …) | — (passthrough) |
| `openai` | OpenAI chat/completions (api.openai.com, corporate gateways) | claude-code-router |
| `copilot` | GitHub Copilot (OAuth device flow + prefill fix) | copilot-api + copilot-bridge |
| `codex` | ChatGPT Responses API (subscription) | claude-codex |

## Quick start

```bash
make install                      # build + /usr/local/bin/ccms (PREFIX=…)
ccms init                         # import from settings/env.json + templates
ccms list                         # profiles and models
ccms auth copilot                 # GitHub OAuth (for the copilot profile)
ccms use work                     # switch (the daemon starts automatically)
ccms use zai                      # switch to an anthropic profile — direct, no daemon
ccms status                       # profile/model/daemon
```

How `ANTHROPIC_BASE_URL` ends up in `~/.claude/settings.json` depends on the
profile's protocol: translated ones (openai/copilot/codex) point at the local
daemon (`http://127.0.0.1:4567` by default) and switching restarts the daemon
with the new provider as a launch parameter (`ccms serve --daemon --provider
<key> --port <port>`); an anthropic passthrough goes **direct** — the upstream
`base_url` and credential are written straight into the config (bash-ccx
semantics) and the unused daemon is stopped. For openai the credential is
pass-through: settings.json gets the provider's own token, Claude Code sends
it with every request, and the daemon forwards it upstream unchanged — it
holds no secret of its own.

## Config (`~/.claude/providers.json`, 0600 permissions)

Connection fields live directly in the provider entry — no protocol-named
block; `protocol` determines which of them are meaningful. The map lives
under the `providers` key; files from older releases (top-level `profiles`
key, `version`, `daemon.auth_token`) keep loading and are rewritten in the
current schema on the next load. The client masquerade (UA, editor
versions, integration id, originator, prefill fix) is not config: it lives
in code constants next to the translators. The optional `daemon.port`
defaults to 4567 (precedence: `--port` flag > `CCMS_PORT` > `daemon.port` >
4567).

```jsonc
{
  "daemon": {"port": 4567},
  "providers": {
    "zai": {
      "name": "z.ai (GLM)",
      "protocol": "anthropic",
      "base_url": "https://api.z.ai/api/anthropic",
      "auth_token": "<key>",
      "models": [{"id": "glm-5.3[1m]", "label": "GLM 5.3", "default": true}]
    },
    "work": {
      "name": "Work gateway",
      "protocol": "openai",
      "base_url": "https://…/v1",
      "auth_token": "<token>",
      "models": […]
    }
  }
}
```

## Commands

`ccms` (menu) · `ccms use <profile> [model]` (alias `ccms <profile>`) · `list` ·
`status` · `init [--force]` · `edit` · `serve --provider <key> [--port N]`
(daemon in foreground) · `stop` · `auth copilot` · `auth codex` · `version` ·
`help`

## Development

```bash
make test        # tests with coverage (threshold ≥80%)
make lint        # golangci-lint on changed files
make lint-full   # on all files
make generate    # mocks + formatting
```

Structure and conventions — see CLAUDE.md: one file per method/handler,
foo_test.go, suite provider in internal/pkg/suite/provider.
