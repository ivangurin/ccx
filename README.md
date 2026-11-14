# ccx

**Switch Claude Code between API providers, GitHub Copilot, and ChatGPT — one binary, no separate proxy to install.**

[![Release](https://img.shields.io/github/v/release/ivangurin/ccx?style=for-the-badge)](https://github.com/ivangurin/ccx/releases)
[![License](https://img.shields.io/github/license/ivangurin/ccx?style=for-the-badge)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.27-00ADD8?style=for-the-badge&logo=go&logoColor=white)](https://go.dev)
[![Platform](https://img.shields.io/badge/platform-macOS%20arm64%20%2B%20amd64-lightgrey?style=for-the-badge)](https://github.com/ivangurin/ccx/releases/latest)

ccx manages provider profiles and model selection for Claude Code. Its built-in
local daemon translates the Anthropic Messages API for OpenAI-compatible APIs,
GitHub Copilot, and ChatGPT/Codex. Anthropic-compatible APIs connect directly,
without the daemon.

Choose a provider and model once; ccx updates Claude Code's settings and manages
the daemon for you. No separate router, Docker container, or Node.js sidecar.

## Why ccx?

- **One binary, no separate services to install.** CLI, daemon, translators,
  and OAuth flows are bundled together.
- **Speaks your provider's language.** The daemon translates the Anthropic
  Messages API into OpenAI chat/completions, the GitHub Copilot API or the
  ChatGPT Responses API. Anthropic-compatible providers are passed through
  untouched — no translation needed.
- **Careful with your setup.** A switch edits only the env block and the model
  pin in Claude Code's `settings.json`; key order, indentation and your own
  variables survive byte-for-byte. If anything goes wrong mid-switch, the file
  is left alone and you stay on the previous provider.
- **No third-party relay.** ccx runs locally and sends requests directly to
  the selected provider. For OpenAI-compatible providers, the daemon forwards
  the token supplied by Claude Code rather than maintaining its own credential.
- **Subscriptions are first-class.** Sign in to GitHub Copilot or
  ChatGPT/Codex with `ccx auth <provider>`; ccx stores credentials per provider.

## Supported providers

| Protocol | Connects to | Authentication |
|---|---|---|
| `anthropic` | Anthropic-compatible APIs (z.ai, …), directly | API key or token |
| `openai` | OpenAI-compatible Chat Completions APIs, including corporate gateways | API key or token |
| `copilot` | GitHub Copilot | `ccx auth <provider>` |
| `codex` | ChatGPT/Codex (subscription) | `ccx auth <provider>` |

<details>
<summary>Provider notes</summary>

- **copilot** — authenticate once with `ccx auth <key>` (GitHub OAuth device
  flow); ccx caches and refreshes the token after that.
- **codex** — authenticate once with `ccx auth <key>` (ChatGPT OAuth: the
  browser opens auth.openai.com, ccx catches the callback). Streaming only,
  and it always sends `store: false`.
- Auth is per-provider: credentials live in `~/.ccx/auth/<key>.json`, and
  `ccx auth` with no argument lists every provider's auth state. Two
  accounts of one protocol is just two providers:

  ```json
  "providers": {
    "gh-personal": {"name": "GitHub Copilot (personal)", "protocol": "copilot", "models": [...]},
    "gh-work":     {"name": "GitHub Copilot (work)",     "protocol": "copilot", "models": [...]}
  }
  ```

  `ccx auth gh-personal` and `ccx auth gh-work` each run their own GitHub
  login into their own store.
- **openai** — works with api.openai.com and corporate OpenAI-compatible
  gateways; your token is pass-through (see above). Gateways that block Go's
  default User-Agent are handled — ccx masquerades as a regular client.

</details>

## Install

macOS (Apple Silicon and Intel) via Homebrew:

```bash
brew install ivangurin/tap/ccx
```

The formula tracks the latest release and is updated automatically on every
`v*` tag. Upgrading: `brew upgrade ccx`. Uninstalling: `brew uninstall ccx`.

Or without Homebrew (Apple Silicon and Intel):

```bash
curl -fsSL https://raw.githubusercontent.com/ivangurin/ccx/main/install.sh | sh
```

Fetches the latest release and installs it to `/usr/local/bin/ccx`. Building
from source works too: clone the repo and `make install` (or `PREFIX=…
make install` for another location).

Uninstall:

```bash
curl -fsSL https://raw.githubusercontent.com/ivangurin/ccx/main/uninstall.sh | sh
```

Stops the daemon (best-effort) and removes the binary only — `~/.ccx` with
your providers, state and logs stays untouched, and so does the one-time
settings backup `~/.claude/settings.json.bak`.

## Quick start

```bash
ccx init            # create ~/.ccx/providers.json: imports + provider templates
ccx edit            # configure your providers and models in $EDITOR
ccx auth <provider> # Copilot / ChatGPT only: replace <provider> with your provider key
ccx                 # interactive menu: pick a provider and model
claude              # start a new Claude Code session
```

`ccx init` creates provider templates (OpenAI, Copilot, ChatGPT Codex) and
imports what it can find in your current setup. Add API credentials with
`ccx edit`, or sign in with `ccx auth <provider>` for Copilot / ChatGPT.
The provider is the key in `providers.json`; `ccx list` shows your choices.
To switch without the menu: `ccx use <provider> [model]`. The daemon starts
automatically when needed.

**Switch before starting Claude Code.** Running sessions may retain the previous
environment, and switching providers interrupts active daemon requests. Start a
new Claude Code session after switching.

## Configuration

Everything lives in `~/.ccx/providers.json`. Connection fields (`base_url`,
`auth_token`, `api_key`, …) sit directly in the provider entry — no nested
protocol blocks — and `protocol` decides which of them are meaningful:

```jsonc
{
  "providers": {
    "zai": {
      "name": "z.ai",
      "protocol": "anthropic",
      "base_url": "https://api.z.ai/api/anthropic",
      "auth_token": "<token>",
      "models": [
        {
          "id": "glm-5.3", 
          "label": "GLM 5.3",   
          "default": true
        },
        {
          "id": "glm-5.3-flash",   
          "label": "GLM 5.3 Flash"
        }
      ]
    },
    "work": {
      "name": "Work gateway",
      "protocol": "openai",
      "base_url": "https://…/v1",
      "auth_token": "<token>",
      "models": [
        {
          "id": "work model", 
          "label": "Work Model", 
          "default": true
        }
      ]
    }
  }
}
```

### Credentials from environment variables

For `anthropic` and `openai`, `auth_token` and `api_key` can reference an
exported environment variable instead of containing the secret:

```json
{"auth_token": "$WORK_API_TOKEN"}
```

`${WORK_API_TOKEN}` also works. The variable must be available to the ccx
process and non-empty; otherwise the switch fails before restarting the daemon
or changing settings. Only whole-value references are expanded, not inline
substitutions.

This keeps the secret out of `providers.json`, **not off disk**: on switch,
the resolved credential is written to Claude Code's `settings.json`.

### Fields

**Provider** — the map key is what you type in `ccx use <key>`:

| Field | Type | Required | Default | Meaning |
|---|---|---|---|---|
| `protocol` | string | **yes** | — | `anthropic` / `openai` / `copilot` / `codex` |
| `name` | string | no | — | display name (menus, `ccx list`) |
| `base_url` | string | for `anthropic` / `openai` | — | upstream URL; `copilot` / `codex` have fixed endpoints and don't use it |
| `auth_token` | string | one of `auth_token` / `api_key` for `anthropic` / `openai` | — | credential; may be a whole-value env reference `$NAME` / `${NAME}`; for `copilot` / `codex` any placeholder works (they own the real one) |
| `api_key` | string | (alternative to `auth_token`) | — | anthropic: sent as `x-api-key`; openai: `Authorization: Bearer`; `auth_token` wins on anthropic |
| `extra_headers` | map | no | — | extra upstream headers (`anthropic` profiles: become `ANTHROPIC_CUSTOM_HEADERS`) |
| `max_tokens_field` | string | no | `max_tokens` | openai: switch to `max_completion_tokens` for gateways that require it |
| `reasoning_to_thinking` | bool | no | `false` | reserved, no effect yet (reasoning deltas already map to thinking blocks unconditionally) |
| `env` | map | no | — | extra env keys written to settings.json on switch (your variables; `ANTHROPIC_*` stays ccx-managed) |
| `models` | list | **yes** | — | the provider's models (see below) |

**Model** — `label` is what menus and `ccx list` show, `id` is what
`ccx use <provider> <model>` accepts (exact match):

| Field | Type | Required | Default | Meaning |
|---|---|---|---|---|
| `id` | string | **yes** | — | the real model id: written to settings.json, accepted by `ccx use` |
| `label` | string | no | the `id` | display text in the menu and `ccx list` |
| `default` | bool | no | `false` | selected when no model is specified (`ccx use <provider>`, menu Enter) |
| `max_context_tokens` | int | no | — | caps the context window Claude Code believes it has (drives the `CLAUDE_CODE_MAX_CONTEXT_TOKENS` env trio); unset → the trio is wiped |
| `effort` | string | no | — | written to `CLAUDE_CODE_EFFORT_LEVEL` verbatim; unset → the key is wiped |

**`daemon` block** — optional, top-level; read by the switcher, which spawns
the daemon with these parameters:

| Field | Type | Required | Default | Meaning |
|---|---|---|---|---|
| `port` | int | no | `4567` | daemon port; actual precedence: `--port` flag > `CCX_PORT` env > `daemon.port` > 4567 |
| `debug` | bool | no | `false` | spawn the daemon with debug logging and request dumps in the daemon log |

## What ccx does with your settings.json

Of Claude Code's home (`~/.claude`) only `settings.json` is ever touched —
edited in place, never moved or reformatted (`CCX_SETTINGS` points it
elsewhere). A switch changes exactly two things:

- **The env block.** `ANTHROPIC_BASE_URL` + the credential (or the upstream
  URL for a direct `anthropic` provider), the model pin `ANTHROPIC_MODEL`
  and the ccx-owned context/effort keys. Stale `ANTHROPIC_*` keys are swept;
  your own variables (`API_TIMEOUT_MS`, MCP tokens, …) survive untouched —
  an always-env key you set by hand keeps its value.
- **The model pin.** `model`, `availableModels=[m]` and
  `enforceAvailableModels` at the top level.

Everything else — key order, indentation, your own blocks, the trailing
newline — survives a switch byte-for-byte: a fresh env key is appended at
the end of the block, a replaced one keeps its line.

**The one-time backup.** The first switch freezes the original file to
`~/.claude/settings.json.bak` (0600) and later switches never overwrite it.
`ccx restore` copies the backup back and stops the daemon.

**Failure handling.** The file is edited only after the new daemon has
passed its health check; if anything fails mid-switch, settings.json stays
as it was and you remain on the previous provider.

## How it works

Claude Code always talks the Anthropic Messages API. ccx makes sure those
requests reach your provider in its own language:

```mermaid
flowchart LR
    CC["Claude Code"] -->|"Anthropic Messages API"| D["ccx daemon<br>127.0.0.1:4567"]
    D -->|"chat/completions"| O["OpenAI-compatible<br>gateway"]
    D -->|"Copilot API"| C["GitHub Copilot"]
    D -->|"Responses API"| X["ChatGPT · codex"]
    CC -.->|"direct, no daemon"| A["Anthropic-compatible API"]
```

Switching providers means `ANTHROPIC_BASE_URL` in `settings.json` changes:
translated protocols (openai/copilot/codex) point at the local daemon, while
an `anthropic` provider goes straight to the upstream URL — no daemon
involved.

<details>
<summary>The fine print: daemon lifecycle, credentials, failure handling</summary>

- **Switching restarts the daemon.** For translated protocols, `ccx use`
  spawns a fresh daemon with the new provider as a launch parameter
  (`ccx serve --daemon --provider <key> --port <port>`) and waits for its
  health check. The daemon builds and verifies the route *before* it starts
  listening — a healthy `/health` means startup checks passed, not that every
  upstream model can generate a response. Only after the health check passes
  is `settings.json` touched; if the daemon never comes up, the switch
  is cancelled, the file stays as it was, and you remain on the previous
  provider.
- **In-flight requests die on a switch.** A restart drops ongoing SSE
  streams — an accepted trade-off of the restart-based switching semantics.
- **anthropic goes direct.** The upstream `base_url` and credential are
  written straight into `settings.json`, and the unused daemon is stopped
  (best-effort).
- **Who holds which credential:** for `openai` the token is pass-through —
  settings.json gets the provider's own token, Claude Code sends it with
  every request, and the daemon forwards it upstream unchanged, holding no
  secret of its own. `copilot` and `codex` own their credentials
  (`~/.ccx/auth/<key>.json`, written by `ccx auth <key>`), so any placeholder
  bearer works from Claude Code's side. The codex store stays
  `auth.json`-compatible: a hand-made symlink of `auth/<key>.json` onto the
  codex CLI's own `~/.codex/auth.json` keeps both tools reading one login.
- **Model pinning.** A switch pins the chosen model: `model`,
  `availableModels=[m]`, `enforceAvailableModels` and `ANTHROPIC_MODEL`
  select it. Legacy `ANTHROPIC_DEFAULT_*_MODEL` overrides are removed on
  the next switch.

</details>

## Troubleshooting

- **Copilot or ChatGPT requires authentication:** run `ccx auth <provider>`
  using the provider key from `ccx list`.
- **Claude Code still uses the previous provider:** start a new Claude Code
  session after switching; an existing session may retain its old environment.
- **The local daemon is unavailable:** check `ccx status` and the latest log
  in `~/.ccx/logs/`. For a translated provider, select it again with
  `ccx use <provider>` to restart the daemon. This interrupts active requests.
- **The daemon is healthy but a model fails:** run
  `ccx test --provider <provider>` to validate that provider and probe its models.
  This sends real requests (usage charges may apply), uses temporary daemons,
  and does not change Claude Code's settings.
- **Return to your original configuration:** run `ccx restore`, then start a
  new Claude Code session. The backup is the snapshot from the first switch,
  not the previous provider selection.

## Commands

| Command | What it does |
|---|---|
| `ccx` | interactive menu: provider → model |
| `ccx init [--force]` | create providers.json (import from settings/env) |
| `ccx edit` | open providers.json in `$EDITOR` (alias: `ccx -e`) |
| `ccx list` | providers and models — model rows show their labels (alias: `ccx -l`) |
| `ccx status` | active provider/model and daemon health (alias: `ccx -s`) |
| `ccx test [--provider <key>]` | validate configuration and probe models with real requests through temporary daemons; usage charges may apply |
| `ccx use <provider> [model]` | switch (alias: `ccx <provider> [model]`) |
| `ccx serve [--daemon] [--debug] --provider <key> [--port N]` | run the daemon in the foreground (`--debug`: debug-level logging — the format stays env-driven — and request dumps into the unified daemon log as `kind=reqdump` lines) |
| `ccx stop` | stop the daemon |
| `ccx restore` | restore the original settings.json from the one-time backup (`~/.claude/settings.json.bak`, frozen at the first switch); stops the daemon |
| `ccx auth [provider]` | login flow for the provider's protocol (copilot device flow / codex OAuth); no argument — auth state of every provider |
| `ccx version` / `ccx help` | version (alias: `ccx -v`) / help |
| `ccx completion <shell>` | shell completion script (zsh/bash/fish/powershell) |

MIT License — see [LICENSE](LICENSE).
