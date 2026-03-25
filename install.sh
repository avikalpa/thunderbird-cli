#!/usr/bin/env sh
set -eu

REPO="${TB_GITHUB_REPO:-avikalpa/thunderbird-cli}"
INSTALL_DIR="${TB_INSTALL_DIR:-$HOME/.local/bin}"
BIN_NAME="${TB_BIN_NAME:-tb}"
VERSION="${TB_VERSION:-latest}"

need() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "install.sh: required command not found: $1" >&2
    exit 1
  }
}

need curl
need tar
need uname
need mktemp

os=$(uname -s)
arch=$(uname -m)
case "$os" in
  Linux) target_os="linux" ;;
  Darwin) target_os="darwin" ;;
  *)
    echo "install.sh: unsupported OS: $os" >&2
    echo "Download a release archive manually from https://github.com/$REPO/releases" >&2
    exit 1
    ;;
esac

case "$arch" in
  x86_64|amd64) target_arch="x86_64" ;;
  aarch64|arm64) target_arch="arm64" ;;
  *)
    echo "install.sh: unsupported architecture: $arch" >&2
    echo "Download a release archive manually from https://github.com/$REPO/releases" >&2
    exit 1
    ;;
esac

if [ "$VERSION" = "latest" ]; then
  VERSION=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | sed -n 's/.*"tag_name": *"v\{0,1\}\([^"]*\)".*/\1/p' | head -n1)
  if [ -z "$VERSION" ]; then
    echo "install.sh: failed to resolve latest release from GitHub" >&2
    exit 1
  fi
fi

asset="thunderbird-cli_${VERSION}_${target_os}_${target_arch}.tar.gz"
url="https://github.com/$REPO/releases/download/v$VERSION/$asset"

tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT INT TERM

mkdir -p "$INSTALL_DIR"
curl -fL "$url" -o "$tmpdir/$asset"
tar -xzf "$tmpdir/$asset" -C "$tmpdir"
install -m 0755 "$tmpdir/tb" "$INSTALL_DIR/$BIN_NAME"

echo "Installed $BIN_NAME $VERSION to $INSTALL_DIR/$BIN_NAME"
"$INSTALL_DIR/$BIN_NAME" version || true

echo
echo "Running doctor (safe, read-only):"
"$INSTALL_DIR/$BIN_NAME" doctor || true

echo
case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *)
    echo "Add $INSTALL_DIR to PATH if needed:"
    echo "  export PATH=\"$INSTALL_DIR:\$PATH\""
    ;;
esac
