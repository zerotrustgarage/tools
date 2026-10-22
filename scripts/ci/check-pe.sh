#!/usr/bin/env bash
# Checks that a file is a 64-bit Windows executable with the expected
# subsystem, using only coreutils (the CI pool has no `file` command).
#
# Shared by every tool's Windows build (moved here from scanner/ci when Readout needed it too).
#
#   scripts/ci/check-pe.sh <exe> <gui|console> [amd64|arm64]
set -euo pipefail

f="${1:?usage: check-pe.sh <exe> <gui|console> [amd64|arm64]}"
case "${2:?subsystem: gui or console}" in
  gui) want_sub=2 ;;
  console) want_sub=3 ;;
  *) echo "check-pe: subsystem must be gui or console" >&2; exit 2 ;;
esac
case "${3:-amd64}" in
  amd64) want_machine=34404 ;; # IMAGE_FILE_MACHINE_AMD64 0x8664
  arm64) want_machine=43620 ;; # IMAGE_FILE_MACHINE_ARM64 0xAA64
  *) echo "check-pe: architecture must be amd64 or arm64" >&2; exit 2 ;;
esac

fail() { echo "check-pe: $f: $*" >&2; exit 1; }
u16() { od -An -tu2 -j "$1" -N2 "$f" | tr -d ' '; } # little-endian host assumed (x86-64, arm64)
u32() { od -An -tu4 -j "$1" -N4 "$f" | tr -d ' '; }

[[ -f "$f" ]] || fail "missing"
[[ "$(head -c2 "$f")" == "MZ" ]] || fail "no MZ header"
pe="$(u32 60)"                                                # e_lfanew
[[ "$(od -An -tx1 -j "$pe" -N4 "$f" | tr -d ' ')" == "50450000" ]] || fail "no PE signature"
[[ "$(u16 $((pe + 4)))" == "$want_machine" ]] || fail "machine $(u16 $((pe + 4))), want $want_machine"
[[ "$(u16 $((pe + 24)))" == "523" ]] || fail "not PE32+ (optional header magic $(u16 $((pe + 24))))"
sub="$(u16 $((pe + 24 + 68)))"                                # OptionalHeader.Subsystem
[[ "$sub" == "$want_sub" ]] || fail "subsystem $sub, want $want_sub"
echo "check-pe: $f: PE32+ ${3:-amd64} ${2} ok"
