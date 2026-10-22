#!/bin/sh
# Exercises a Windows installer under Wine on Linux: refusal without the WebView2 Runtime,
# silent install, files, Start menu entry, registry (Programs and Features, Open with, folder
# context menu), reinstall over the top, and a clean silent uninstall. It does not test the app
# UI: WebView2 does not run under Wine, so that needs a real Windows machine.
#
# Needs: wine (64 and 32-bit), xvfb-run. On Ubuntu:
#   sudo dpkg --add-architecture i386 && sudo apt-get update
#   sudo apt-get install --no-install-recommends wine wine64 wine32:i386 xvfb xauth
#   scripts/ci/test-installer-wine.sh <Setup.exe>
set -eu
setup=$(realpath "$1")
export WINEPREFIX="$(mktemp -d)/prefix" WINEDEBUG=-all
app="$WINEPREFIX/drive_c/Program Files/ZTG Readout"
links="$WINEPREFIX/drive_c/ProgramData/Microsoft/Windows/Start Menu/Programs"
webview='HKLM\SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}'
fail() { echo "FAIL: $*"; exit 1; }
run() { set +e; xvfb-run -a wine "$@" >/dev/null 2>&1; code=$?; set -e; wineserver -w; return 0; }
key() { wine reg query "$1" /reg:64 >/dev/null 2>&1; }

xvfb-run -a wineboot -i >/dev/null 2>&1; wineserver -w

run "$setup" /S
[ "$code" = 4 ] || fail "install without WebView2 should exit 4, got $code"
[ ! -e "$app" ] || fail "nothing should be installed without WebView2"
echo "ok: refuses without the WebView2 Runtime (exit 4)"

# Stand in for the runtime: the installer only reads its registered version.
wine reg add "$webview" /v pv /t REG_SZ /d 130.0.0.0 /f /reg:64 >/dev/null 2>&1
run "$setup" /S
[ "$code" = 0 ] || fail "silent install exited $code"
for f in ztg-readout.exe uninstall.exe THIRD-PARTY-NOTICES.txt; do [ -f "$app/$f" ] || fail "missing $f"; done
[ -f "$links/ZTG Readout.lnk" ] || fail "missing Start menu shortcut"
for k in 'HKLM\Software\Microsoft\Windows\CurrentVersion\Uninstall\ZTGReadout' \
         'HKLM\Software\Classes\ZTGReadout.Document\shell\open\command' \
         'HKLM\Software\Classes\Directory\shell\ZTGReadout\command' \
         'HKLM\Software\Classes\Directory\Background\shell\ZTGReadout\command'; do
  key "$k" || fail "missing registry key $k"
done
wine reg query 'HKLM\Software\Classes\.md\OpenWithProgids' /reg:64 2>/dev/null | grep -q ZTGReadout.Document || fail ".md Open with entry missing"
echo "ok: silent install (files, shortcut, uninstall entry, Open with, folder menu)"

run "$setup" /S
[ "$code" = 0 ] || fail "reinstall exited $code"
echo "ok: reinstall over an existing install"

run "$app/uninstall.exe" /S
i=0; while [ -e "$app" ] && [ $i -lt 30 ]; do sleep 1; i=$((i+1)); done; wineserver -w
[ ! -e "$app" ] || fail "install folder left behind"
[ ! -e "$links/ZTG Readout.lnk" ] || fail "shortcut left behind"
for k in 'HKLM\Software\Microsoft\Windows\CurrentVersion\Uninstall\ZTGReadout' 'HKLM\Software\Classes\ZTGReadout.Document' \
         'HKLM\Software\Classes\Directory\shell\ZTGReadout' 'HKLM\Software\Classes\Applications\ztg-readout.exe'; do
  ! key "$k" || fail "registry key left behind: $k"
done
! wine reg query 'HKLM\Software\Classes\.md\OpenWithProgids' /reg:64 2>/dev/null | grep -q ZTGReadout || fail ".md Open with entry left behind"
echo "ok: silent uninstall removes files, shortcut and registry entries"
rm -rf "$(dirname "$WINEPREFIX")"
