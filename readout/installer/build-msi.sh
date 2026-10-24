#!/bin/sh
# Builds the ZTG Readout MSI on Linux with wixl, then adds the WebView2 launch condition, which
# wixl cannot express, to the package's LaunchCondition table with msibuild.
# Ubuntu packages: wixl, msitools.
#   readout/installer/build-msi.sh <ztg-readout.exe> <THIRD-PARTY-NOTICES.txt> <version> <out.msi>
set -eu
here=$(cd "$(dirname "$0")" && pwd)
exe=$(realpath "$1"); notices=$(realpath "$2"); version=$3; out=$(realpath -m "$4")
case "$version" in *[!0-9.]*|'') echo "version must be numeric, like 0.1.0" >&2; exit 2;; esac
# Windows Installer compares only major.minor.build, at most 255.255.65535; anything else
# would build a package that upgrades wrongly.
if ! printf '%s\n' "$version" | awk -F. 'NF == 3 && $1 <= 255 && $2 <= 255 && $3 <= 65535 && $1 != "" && $2 != "" && $3 != "" { ok = 1 } END { exit !ok }'; then
  echo "version must be X.Y.Z with X and Y at most 255 and Z at most 65535" >&2
  exit 2
fi
wixl --arch x64 \
  -D "Version=$version" -D "AppExe=$exe" -D "Notices=$notices" \
  -D "IconFile=$here/../src-tauri/icons/icon.ico" \
  -o "$out" "$here/ztg-readout.wxs"

# Keep the row wixl wrote (its downgrade guard) and append the WebView2 requirement. The
# LaunchConditions action already runs after AppSearch, which reads the runtime's version.
work=$(mktemp -d)
trap 'rm -rf "${work:?}"' EXIT
(cd "$work" && msiinfo export "$out" LaunchCondition > LaunchCondition.idt)
condition='Installed OR (WEBVIEW2_VERSION AND WEBVIEW2_VERSION <> "0.0.0.0") OR (WEBVIEW2_USER_VERSION AND WEBVIEW2_USER_VERSION <> "0.0.0.0")'
message='ZTG Readout needs the Microsoft Edge WebView2 Runtime. Install it from https://developer.microsoft.com/microsoft-edge/webview2/ and run this setup again.'
printf '%s\t%s\r\n' "$condition" "$message" >> "$work/LaunchCondition.idt"
msibuild "$out" -i "$work/LaunchCondition.idt"
msiinfo export "$out" LaunchCondition | grep -q WEBVIEW2_VERSION || { echo "WebView2 launch condition missing" >&2; exit 1; }
