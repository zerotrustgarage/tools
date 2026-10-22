#!/bin/sh
# Cross-compiles a Tauri tool's Windows x64 executable on Linux, with clang/lld and the MSVC
# CRT and Windows SDK that cargo-xwin downloads from Microsoft. The organization has no Windows
# runner, and building here keeps third-party build code off the machine that signs.
#
# It goes through the Tauri CLI (not plain cargo) because the CLI enables the production
# custom-protocol build and builds the frontend with TAURI_ENV_PLATFORM set; plain cargo would
# produce a development build that loads its UI from localhost.
#
# XWIN_ACCEPT_LICENSE=1 accepts Microsoft's license for the downloaded CRT and SDK files
# (see docs/signing.md).
#   scripts/ci/build-windows.sh <tool dir>
set -eu
CARGO_XWIN_VERSION=0.23.1
here=$(cd "$(dirname "$0")" && pwd)
tool_dir=$(cd "$1" && pwd)
(cd "$tool_dir/src-tauri" && "$here/install-rust.sh" x86_64-pc-windows-msvc)
PATH="$HOME/.cargo/bin:$PATH"
command -v cargo-xwin >/dev/null 2>&1 || cargo install cargo-xwin --version "=$CARGO_XWIN_VERSION" --locked
cd "$tool_dir"
XWIN_ACCEPT_LICENSE=1 npx tauri build --runner cargo-xwin --target x86_64-pc-windows-msvc --no-bundle -- --locked
