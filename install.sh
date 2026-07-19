#!/bin/sh
# Downloads and installs the latest (or a pinned) mailhook release.
#
#   curl -fsSL https://raw.githubusercontent.com/PasinduYeshan/mailhook/main/install.sh | sh
#
# Env overrides:
#   VERSION=1.2.3   install a specific version instead of latest
#   PREFIX=/opt/x   install dir defaults to $PREFIX/bin ($HOME/.local by default)
set -e

REPO="PasinduYeshan/mailhook"
BIN_NAME="mailhook"
PREFIX="${PREFIX:-$HOME/.local}"
INSTALL_DIR="$PREFIX/bin"

need() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "error: '$1' is required but not found on PATH" >&2
    exit 1
  }
}

need curl
need tar

detect_os() {
  case "$(uname -s)" in
    Linux) echo linux ;;
    Darwin) echo darwin ;;
    *)
      echo "error: unsupported OS: $(uname -s)" >&2
      echo "Download a build manually: https://github.com/$REPO/releases" >&2
      exit 1
      ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64 | amd64) echo amd64 ;;
    arm64 | aarch64) echo arm64 ;;
    *)
      echo "error: unsupported architecture: $(uname -m)" >&2
      echo "Download a build manually: https://github.com/$REPO/releases" >&2
      exit 1
      ;;
  esac
}

GOOS="$(detect_os)"
GOARCH="$(detect_arch)"

if [ -n "$VERSION" ]; then
  TAG="v${VERSION#v}"
else
  TAG="$(curl -fsSL -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest" | sed 's#.*/tag/##')"
fi

if [ -z "$TAG" ]; then
  echo "error: could not resolve the latest release; pass VERSION=x.y.z to pin one" >&2
  exit 1
fi

VER="${TAG#v}"
ASSET="${BIN_NAME}_${VER}_${GOOS}_${GOARCH}.tar.gz"
URL="https://github.com/$REPO/releases/download/$TAG/$ASSET"

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

echo "Downloading $ASSET ($TAG)..."
if ! curl -fsSL "$URL" -o "$TMP_DIR/$ASSET"; then
  echo "error: failed to download $URL" >&2
  echo "Check available builds: https://github.com/$REPO/releases" >&2
  exit 1
fi

tar -xzf "$TMP_DIR/$ASSET" -C "$TMP_DIR"

mkdir -p "$INSTALL_DIR"
install -m 0755 "$TMP_DIR/$BIN_NAME" "$INSTALL_DIR/$BIN_NAME"
echo "installed: $INSTALL_DIR/$BIN_NAME ($TAG)"

case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *)
    echo ""
    echo "WARNING: $INSTALL_DIR is not on PATH. Add this to your shell rc:"
    echo "         export PATH=\"$INSTALL_DIR:\$PATH\""
    ;;
esac

echo ""
echo "next:  mailhook start     # then open http://127.0.0.1:8091/"
