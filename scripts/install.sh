#!/bin/sh
# mailhook installer for Linux and macOS.
#
#   curl -fsSL https://github.com/PasinduYeshan/mailhook/releases/latest/download/install.sh | sh
#
# Environment variables:
#   MAILHOOK_VERSION      release tag to install, e.g. v0.1.0 (default: latest)
#   MAILHOOK_INSTALL_DIR  where to put the binary (default: ~/.local/bin)
#
# Run it again to upgrade.

set -eu

REPO="PasinduYeshan/mailhook"
INSTALL_DIR="${MAILHOOK_INSTALL_DIR:-$HOME/.local/bin}"
VERSION="${MAILHOOK_VERSION:-}"

say() { printf '%s\n' "$*"; }
err() { printf 'error: %s\n' "$*" >&2; exit 1; }

have() { command -v "$1" >/dev/null 2>&1; }

download() { # url dest
  if have curl; then
    curl -fsSL --retry 3 -o "$2" "$1"
  elif have wget; then
    wget -q -O "$2" "$1"
  else
    err "need curl or wget"
  fi
}

latest_tag() {
  url="https://github.com/$REPO/releases/latest"
  if have curl; then
    final=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "$url")
  else
    final=$(wget -q -S --spider "$url" 2>&1 | sed -n 's/^ *[Ll]ocation: *//p' | tail -n 1 | tr -d '\r')
  fi
  tag=${final##*/}
  case "$tag" in
    v*) printf '%s' "$tag" ;;
    *) err "could not find the latest release (got '$final')" ;;
  esac
}

sha256() {
  if have sha256sum; then
    sha256sum "$1" | cut -d ' ' -f 1
  elif have shasum; then
    shasum -a 256 "$1" | cut -d ' ' -f 1
  else
    err "need sha256sum or shasum to verify the download"
  fi
}

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) err "unsupported OS: $(uname -s) (mailhook releases cover Linux and macOS)" ;;
esac

case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) err "unsupported CPU: $(uname -m)" ;;
esac

[ -n "$VERSION" ] || VERSION=$(latest_tag)
case "$VERSION" in v*) ;; *) VERSION="v$VERSION" ;; esac

archive="mailhook_${VERSION#v}_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/$VERSION"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

say "Downloading mailhook $VERSION ($os/$arch)"
download "$base/$archive" "$tmp/$archive" || err "download failed: $base/$archive"
download "$base/checksums.txt" "$tmp/checksums.txt" || err "download failed: $base/checksums.txt"

expected=$(awk -v f="$archive" '$2 == f { print $1 }' "$tmp/checksums.txt")
[ -n "$expected" ] || err "$archive is not listed in checksums.txt"
actual=$(sha256 "$tmp/$archive")
[ "$expected" = "$actual" ] || err "checksum mismatch for $archive (expected $expected, got $actual)"
say "Checksum OK"

tar -xzf "$tmp/$archive" -C "$tmp" mailhook
mkdir -p "$INSTALL_DIR"

# If a daemon from the old binary is running, restart it on the new one below.
was_running=0
if [ -x "$INSTALL_DIR/mailhook" ] &&
  "$INSTALL_DIR/mailhook" status 2>/dev/null | grep -q '^mailhook: running'; then
  was_running=1
fi

# Install via a temp name + rename so a running binary is never half-written.
cp "$tmp/mailhook" "$INSTALL_DIR/.mailhook.new"
chmod 0755 "$INSTALL_DIR/.mailhook.new"
mv -f "$INSTALL_DIR/.mailhook.new" "$INSTALL_DIR/mailhook"

say "Installed $("$INSTALL_DIR/mailhook" version) to $INSTALL_DIR/mailhook"

if [ "$was_running" = 1 ]; then
  # restart keeps the running daemon's ports.
  if "$INSTALL_DIR/mailhook" restart >/dev/null 2>&1; then
    say "Restarted the mailhook daemon on the new version"
  else
    say "Could not restart the daemon; run: mailhook restart"
  fi
fi

case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *)
    say ""
    say "$INSTALL_DIR is not on your PATH. Add this to your shell rc:"
    say "  export PATH=\"$INSTALL_DIR:\$PATH\""
    ;;
esac

say ""
say "Next: mailhook start    # then open http://127.0.0.1:7791/"
