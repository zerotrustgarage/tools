#!/usr/bin/env bash
# Cross-compiles the ZTG TFTP desktop app and CLI for Windows from any
# platform, with the application manifest, version information and icon
# embedded (winres/). Output is unsigned; signing is the release workflow's job.
#
#   app/build-windows.sh 0.3.0 [amd64|arm64] [out-dir]
set -euo pipefail

version="${1:?usage: build-windows.sh <version> [arch] [out-dir]}"
arch="${2:-amd64}"
here="$(cd "$(dirname "$0")" && pwd)"
out="$(mkdir -p "${3:-$here/dist}" && cd "${3:-$here/dist}" && pwd)"

# Four-part Windows version: 0.3.0 -> 0.3.0.0 (pre-release suffixes dropped).
numeric="${version%%-*}"
IFS=. read -r a b c _ <<<"$numeric"
winver="${a:-0}.${b:-0}.${c:-0}.0"

commit="$(git -C "$here" rev-parse --short=12 HEAD 2>/dev/null || echo unknown)"
ldflags="-s -w -X main.version=${version}+${commit}"

cd "$here"
# go-winres runs at build time only; it is not a module dependency.
go run github.com/tc-hib/go-winres@v0.3.3 make \
  --in winres/winres.json --out rsrc --arch "$arch" \
  --product-version "$winver" --file-version "$winver"
trap 'rm -f "$here"/rsrc_windows_*.syso' EXIT

GOOS=windows GOARCH="$arch" CGO_ENABLED=0 go build -trimpath -tags desktop,production \
  -ldflags "$ldflags -H windowsgui" -o "$out/ZTGTFTP.exe" .

# The CLI gets the same version resource and icon from a copy beside its main package.
cp rsrc_windows_"$arch".syso ../cmd/ztg-tftp/
trap 'rm -f "$here"/rsrc_windows_*.syso "$here"/../cmd/ztg-tftp/rsrc_windows_*.syso' EXIT
(cd .. && GOOS=windows GOARCH="$arch" CGO_ENABLED=0 go build -trimpath \
  -ldflags "$ldflags" -o "$out/ztg-tftp.exe" ./cmd/ztg-tftp)

(cd "$out" && sha256sum ZTGTFTP.exe ztg-tftp.exe >SHA256SUMS.txt && cat SHA256SUMS.txt)
