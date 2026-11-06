#!/bin/sh
# gitreview installer: downloads a release binary for this OS/architecture,
# verifies its SHA-256 checksum and installs it.
#
#   curl -fsSL https://raw.githubusercontent.com/JanVrana/gitreview/main/install.sh | sh
#
# Environment:
#   GITREVIEW_VERSION      release tag to install (default: latest), e.g. v0.2.0
#   GITREVIEW_INSTALL_DIR  target directory (default: ~/.local/bin)
set -eu

REPO="JanVrana/gitreview"
VERSION="${GITREVIEW_VERSION:-latest}"
DIR="${GITREVIEW_INSTALL_DIR:-$HOME/.local/bin}"

err() { echo "gitreview install: $*" >&2; exit 1; }

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) err "unsupported OS $(uname -s) (Linux and macOS only; on other systems use: go install github.com/$REPO@latest)" ;;
esac
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) err "unsupported architecture $(uname -m) (amd64 and arm64 only)" ;;
esac

if command -v curl >/dev/null 2>&1; then
  fetch() { curl -fsSL -o "$2" "$1"; }
elif command -v wget >/dev/null 2>&1; then
  fetch() { wget -q -O "$2" "$1"; }
else
  err "curl or wget is required"
fi

if command -v sha256sum >/dev/null 2>&1; then
  sha256() { sha256sum "$1" | cut -d' ' -f1; }
elif command -v shasum >/dev/null 2>&1; then
  sha256() { shasum -a 256 "$1" | cut -d' ' -f1; }
else
  err "sha256sum or shasum is required to verify the download"
fi

if [ "$VERSION" = latest ]; then
  base="https://github.com/$REPO/releases/latest/download"
else
  base="https://github.com/$REPO/releases/download/$VERSION"
fi
file="gitreview-$os-$arch"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "Downloading $file ($VERSION)…"
fetch "$base/$file" "$tmp/$file" || err "download failed: $base/$file"
fetch "$base/SHA256SUMS" "$tmp/SHA256SUMS" || err "download failed: $base/SHA256SUMS"

want="$(grep " $file\$" "$tmp/SHA256SUMS" | cut -d' ' -f1)"
[ -n "$want" ] || err "$file is not listed in SHA256SUMS"
got="$(sha256 "$tmp/$file")"
[ "$want" = "$got" ] || err "checksum mismatch for $file (expected $want, got $got)"

mkdir -p "$DIR"
chmod +x "$tmp/$file"
mv "$tmp/$file" "$DIR/gitreview"
echo "Installed $("$DIR/gitreview" -version) to $DIR/gitreview"

case ":$PATH:" in
  *":$DIR:"*) ;;
  *) echo "Note: $DIR is not on your PATH. Add it, e.g.: echo 'export PATH=\"$DIR:\$PATH\"' >> ~/.bashrc" ;;
esac
command -v git >/dev/null 2>&1 || echo "Note: gitreview needs git, which was not found on PATH."
