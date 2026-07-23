#!/usr/bin/env sh
# Build the working tree and install it over the canonical binary.
#
# `tb update` only replaces the copy it is running from, and `go build` leaves
# the result in ./bin/tb. Without a defined step from repo to installed path,
# a machine ends up running a different tb than the one you just built.
#
# Canonical path is /usr/local/bin/tb: it is on the non-interactive PATH, so
# `ssh host 'tb ...'` and an interactive shell resolve to the same file.
set -eu

INSTALL_DIR="${TB_INSTALL_DIR:-/usr/local/bin}"
BIN_NAME="${TB_BIN_NAME:-tb}"

cd "$(dirname "$0")/.."
echo "building..."
go build -o bin/tb ./...

SUDO=""
if [ ! -w "$INSTALL_DIR" ]; then
  SUDO="sudo"
fi

$SUDO install -m 0755 bin/tb "$INSTALL_DIR/$BIN_NAME"
echo "installed $("$INSTALL_DIR/$BIN_NAME" version | head -1) to $INSTALL_DIR/$BIN_NAME"

# Surfaces any other tb still shadowing this one.
"$INSTALL_DIR/$BIN_NAME" doctor | sed -n '1,/^Thunderbird root:/p' | head -n -1
