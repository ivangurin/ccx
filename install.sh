#!/bin/sh
# Installs the latest ccms release to /usr/local/bin — the same destination
# and permissions as `make install` (Makefile PREFIX default). Supports only
# what the release workflow publishes: darwin/arm64.
#
# Directly:                sh install.sh
# Piped (README one-liner): curl -fsSL .../install.sh | sh
# Piping is safe: the script never reads stdin, and sudo asks for the
# password on /dev/tty, not on the pipe.
set -eu

repo=ivangurin/ccms
prefix=/usr/local/bin

os=$(uname -s)
arch=$(uname -m)
if [ "$os" != "Darwin" ] || [ "$arch" != "arm64" ]; then
    echo "error: releases are built for macOS Apple Silicon only (you are $os/$arch)" >&2
    echo "see https://github.com/$repo/releases or build from source: make install" >&2
    exit 1
fi

# The asset name embeds the tag, so the tag must be resolved first. Parsed
# with plain sed — jq is not preinstalled on macOS. Unauthenticated GitHub
# API is rate-limited (60/h per IP); retry later if this starts failing.
tag=$(curl -fsSL "https://api.github.com/repos/$repo/releases/latest" \
    | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -1)
if [ -z "$tag" ]; then
    echo "error: could not resolve the latest release tag (API rate limit?)" >&2
    exit 1
fi

workdir=$(mktemp -d)
trap 'rm -rf "$workdir"' EXIT

echo "Downloading ccms $tag ..."
curl -fSL -o "$workdir/ccms.zip" \
    "https://github.com/$repo/releases/download/$tag/ccms-macos-$tag.zip"
unzip -q "$workdir/ccms.zip" -d "$workdir"

# /usr/local/bin does not exist on a fresh Apple Silicon macOS (Homebrew
# lives in /opt/homebrew there) — create it, mirroring the Makefile recipe.
sudo mkdir -p "$prefix"
sudo install -m 0755 "$workdir/ccms" "$prefix/ccms"

echo "Installed $prefix/ccms ($tag)"
