#!/usr/bin/env bash
# Deterministic archive layout and metadata; the input EXEs may already be signed.
#   portable.sh <payload-dir> <out.zip>
set -euo pipefail
payload="$(realpath "${1:?payload directory required}")"
out="$(realpath -m "${2:?output ZIP required}")"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
files=(ZTGTFTP.exe ztg-tftp.exe THIRD-PARTY-NOTICES.txt tftp.cdx.json README.txt)
for file in "${files[@]}"; do
  test -s "$payload/$file" || { echo "missing payload: $file" >&2; exit 2; }
  cp "$payload/$file" "$work/$file"
  chmod 644 "$work/$file"
  TZ=UTC touch -t 198001010000 "$work/$file"
done
mkdir -p "$(dirname "$out")"
# Build a new archive so stale entries from a previous run can never ship.
(cd "$work" && TZ=UTC zip -X -q package.zip "${files[@]}")
mv "$work/package.zip" "$out"
