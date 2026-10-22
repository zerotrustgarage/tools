#!/usr/bin/env bash
# Keep Scanner's documented entry point while sharing the pinned toolchain with TFTP.
set -euo pipefail
exec "$(dirname "$0")/../../scripts/ci/install-go.sh" "$@"
