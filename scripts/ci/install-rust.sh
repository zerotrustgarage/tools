#!/bin/sh
# Installs Rust on a runner that may not have it (the org's self-hosted ephemeral pool).
# rustup-init is a pinned release checked against a digest committed here, not piped from a
# URL. The toolchain itself comes from each tool's rust-toolchain.toml.
#   scripts/ci/install-rust.sh [extra rustup target ...]
set -eu
RUSTUP_VERSION=1.28.2
RUSTUP_SHA256=20a06e644b0d9bd2fbdbfd52d42540bdde820ea7df86e92e533c073da0cdd43c

if ! command -v rustup >/dev/null 2>&1; then
  tmp=$(mktemp -d)
  curl --proto '=https' --tlsv1.2 -sSfL -o "$tmp/rustup-init" \
    "https://static.rust-lang.org/rustup/archive/$RUSTUP_VERSION/x86_64-unknown-linux-gnu/rustup-init"
  echo "$RUSTUP_SHA256  $tmp/rustup-init" | sha256sum --check --strict
  chmod +x "$tmp/rustup-init"
  "$tmp/rustup-init" -y --no-modify-path --profile minimal --default-toolchain none
  rm -rf "$tmp"
  echo "$HOME/.cargo/bin" >> "${GITHUB_PATH:-/dev/null}"
  PATH="$HOME/.cargo/bin:$PATH"
fi
# Run from a directory with rust-toolchain.toml so the pinned toolchain is installed.
rustup toolchain install
for target in "$@"; do rustup target add "$target"; done
rustc --version
