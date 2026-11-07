#!/bin/sh
# Removes the ccx binary installed by install.sh or `make install`
# (PREFIX=/usr/local). Config files (~/.claude/providers.json, the env block
# in ~/.claude/settings.json) are deliberately left in place — only the
# binary goes away, same scope as `make uninstall`.
#
# Directly:                  sh uninstall.sh
# Piped (README one-liner):  curl -fsSL .../uninstall.sh | sh
set -eu

target=/usr/local/bin/ccx

if [ ! -e "$target" ]; then
    echo "ccx is not installed at $target"
    exit 0
fi

# Stop the daemon before deleting its binary — otherwise an orphaned daemon
# keeps running with no way to manage it. Best effort only: a stopped or
# non-default-port daemon (CCX_PORT) must not block the removal.
"$target" stop >/dev/null 2>&1 || true

sudo rm -f "$target"
echo "Removed $target"
