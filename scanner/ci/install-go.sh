#!/usr/bin/env bash
# Installs a pinned Go toolchain from go.dev, verified against a committed
# SHA-256, for CI runners and local builds. Prints the
# bin directory and adds it to GITHUB_PATH when running in Actions.
set -euo pipefail

GO_VERSION="1.26.10"
declare -A GO_SHA256=(
  [amd64]="ccfe7ae822d2c2f41f4fe7c956199967a4dac22d0ee0d2b2bfdb440aab3b20c5"
  [arm64]="3f4227ce7b206a513e794f1393adb473a07b46e94d18bd15d1c684bbb7319797"
)

case "$(uname -m)" in
  x86_64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) echo "install-go: unsupported architecture $(uname -m)" >&2; exit 1 ;;
esac

dest="${RUNNER_TEMP:-${TMPDIR:-/tmp}}/go-${GO_VERSION}"
if [[ ! -x "$dest/go/bin/go" ]]; then
  archive="go${GO_VERSION}.linux-${arch}.tar.gz"
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT
  curl -fsSL --retry 3 -o "$tmp/$archive" "https://go.dev/dl/$archive"
  echo "${GO_SHA256[$arch]}  $tmp/$archive" | sha256sum -c --quiet -
  mkdir -p "$dest"
  tar -C "$dest" -xzf "$tmp/$archive"
fi

"$dest/go/bin/go" version
if [[ -n "${GITHUB_PATH:-}" ]]; then
  echo "$dest/go/bin" >>"$GITHUB_PATH"
fi
# Use exactly this toolchain; do not let go.mod trigger a download.
if [[ -n "${GITHUB_ENV:-}" ]]; then
  echo "GOTOOLCHAIN=local" >>"$GITHUB_ENV"
fi
