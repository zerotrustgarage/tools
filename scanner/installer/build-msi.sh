#!/usr/bin/env bash
# Linux packaging with Ubuntu's wixl and msitools. Signing happens after packaging.
#   build-msi.sh <payload-dir> <version> <out.msi>
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
payload="$(realpath "${1:?payload directory required}")"
version="${2:?version required}"
out="$(realpath -m "${3:?output MSI required}")"
# Windows Installer ProductVersion: major/minor <= 255, build <= 65535.
[[ "$version" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] || { echo "version must be numeric X.Y.Z" >&2; exit 2; }
IFS=. read -r major minor build <<<"$version"
(( major <= 255 && minor <= 255 && build <= 65535 )) || { echo "version exceeds MSI limits" >&2; exit 2; }
for file in ZTGScanner.exe ztg-scanner.exe THIRD-PARTY-NOTICES.txt scanner.cdx.json README.txt; do
  test -s "$payload/$file" || { echo "missing payload: $file" >&2; exit 2; }
done
mkdir -p "$(dirname "$out")"
wixl --arch x64 -D "Version=$version" -D "Payload=$payload" -D "IconFile=$here/icon.ico" -o "$out" "$here/ztg-scanner.wxs"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
msiinfo export "$out" LaunchCondition > "$work/LaunchCondition.idt"
condition='Installed OR (WEBVIEW2_VERSION AND WEBVIEW2_VERSION <> "0.0.0.0") OR (WEBVIEW2_USER_VERSION AND WEBVIEW2_USER_VERSION <> "0.0.0.0")'
message='ZTG Scanner needs the Microsoft Edge WebView2 Runtime. Install it from https://developer.microsoft.com/microsoft-edge/webview2/ and run setup again.'
printf '%s\t%s\r\n' "$condition" "$message" >> "$work/LaunchCondition.idt"
msibuild "$out" -i "$work/LaunchCondition.idt"
msiinfo export "$out" LaunchCondition | grep -q WEBVIEW2_VERSION
