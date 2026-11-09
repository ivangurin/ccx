#!/bin/sh
# Installs the latest ccx release to /usr/local/bin — the same destination
# and permissions as `make install` (Makefile PREFIX default). Picks the
# asset by platform: the release publishes darwin/arm64 and darwin/amd64
# as ccx-mac-<arch>-<tag>.zip.
#
# Directly:                sh install.sh
# Piped (README one-liner): curl -fsSL .../install.sh | sh
# Piping is safe: the script never reads stdin, and sudo asks for the
# password on /dev/tty, not on the pipe.
set -eu

repo=ivangurin/ccx
prefix=/usr/local/bin

os=$(uname -s)
arch=$(uname -m)
case "$os/$arch" in
    Darwin/arm64)  asset_arch=arm64 ;;
    Darwin/x86_64) asset_arch=amd64 ;;
    *) echo "error: releases are built for macOS (arm64 and amd64) only (you are $os/$arch)" >&2
       echo "see https://github.com/$repo/releases or build from source: make install" >&2
       exit 1 ;;
esac

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

echo "Downloading ccx $tag (macOS $asset_arch) ..."
curl -fSL -o "$workdir/ccx.zip" \
    "https://github.com/$repo/releases/download/$tag/ccx-mac-$asset_arch-$tag.zip"
unzip -q "$workdir/ccx.zip" -d "$workdir"

# /usr/local/bin does not exist on a fresh Apple Silicon macOS (Homebrew
# lives in /opt/homebrew there) — create it, mirroring the Makefile recipe.
sudo mkdir -p "$prefix"
sudo install -m 0755 "$workdir/ccx" "$prefix/ccx"

echo "Installed $prefix/ccx ($tag)"
