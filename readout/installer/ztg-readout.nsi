; ZTG Readout Windows installer.
;
; Built with makensis (NSIS 3.08 or later), on Linux in CI. Required defines:
;   VERSION      e.g. 0.1.0
;   APP_EXE      path to the signed ztg-readout.exe
;   NOTICES      path to THIRD-PARTY-NOTICES.txt
;   OUTFILE      installer path to write
; Optional:
;   SIGN_SCRIPT  a command taking one file path that signs the file in place and exits 0 only
;                when the signature verified. When set, makensis signs the uninstaller it
;                generates (!uninstfinalize) and the finished installer (!finalize), so every
;                binary this installer puts on disk carries the publisher's signature.
;
; Per-machine install (RMM and Intune friendly). Silent install: /S. Silent uninstall:
;   "%ProgramFiles%\ZTG Readout\uninstall.exe" /S
; Exit codes: 0 success, 2 cancelled, 3 not 64-bit Windows, 4 WebView2 Runtime missing.

Unicode true
ManifestDPIAware true
SetCompressor /SOLID lzma
RequestExecutionLevel admin

!macro ZTG_REQUIRE NAME
  !ifndef ${NAME}
    !error "${NAME} must be defined (makensis -D${NAME}=...)"
  !endif
!macroend
!insertmacro ZTG_REQUIRE VERSION
!insertmacro ZTG_REQUIRE APP_EXE
!insertmacro ZTG_REQUIRE NOTICES
!insertmacro ZTG_REQUIRE OUTFILE

!define PRODUCT "ZTG Readout"
!define PUBLISHER "Proudlock Technology LLC"
!define WEBSITE "https://ztg.sh"
!define EXE "ztg-readout.exe"
!define PROGID "ZTGReadout.Document"
!define UNINSTALL_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\ZTGReadout"
; Microsoft Edge WebView2 Runtime client id (evergreen).
!define WEBVIEW2_GUID "{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}"

!ifdef SIGN_SCRIPT
  !uninstfinalize '"${SIGN_SCRIPT}" "%1"' = 0
  !finalize '"${SIGN_SCRIPT}" "%1"' = 0
!endif

!include "MUI2.nsh"
!include "x64.nsh"
!include "LogicLib.nsh"
!include "FileFunc.nsh"

Name "${PRODUCT}"
OutFile "${OUTFILE}"
InstallDir "$PROGRAMFILES64\${PRODUCT}"
InstallDirRegKey HKLM "${UNINSTALL_KEY}" "InstallLocation"
BrandingText "${PRODUCT} ${VERSION}"

VIProductVersion "${VERSION}.0"
VIAddVersionKey "ProductName" "${PRODUCT}"
VIAddVersionKey "CompanyName" "${PUBLISHER}"
VIAddVersionKey "LegalCopyright" "Copyright ${PUBLISHER}"
VIAddVersionKey "FileDescription" "${PRODUCT} Setup"
VIAddVersionKey "FileVersion" "${VERSION}"
VIAddVersionKey "ProductVersion" "${VERSION}"

!define MUI_ICON "../src-tauri/icons/icon.ico"
!define MUI_UNICON "../src-tauri/icons/icon.ico"
!define MUI_ABORTWARNING
!define MUI_FINISHPAGE_RUN "$INSTDIR\${EXE}"
!define MUI_FINISHPAGE_RUN_TEXT "Start ${PRODUCT}"

!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"

Function .onInit
  ${IfNot} ${RunningX64}
    MessageBox MB_ICONSTOP "${PRODUCT} requires 64-bit Windows 10 or later." /SD IDOK
    SetErrorLevel 3
    Quit
  ${EndIf}
  SetRegView 64
  SetShellVarContext all
  ; The app runs on the Microsoft Edge WebView2 Runtime, which Windows 11 and current Windows 10
  ; include. It is checked, not bundled, so this installer carries no third-party binary.
  ReadRegStr $0 HKLM "SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\${WEBVIEW2_GUID}" "pv"
  ${If} $0 == ""
  ${OrIf} $0 == "0.0.0.0"
    ReadRegStr $0 HKCU "Software\Microsoft\EdgeUpdate\Clients\${WEBVIEW2_GUID}" "pv"
  ${EndIf}
  ${If} $0 == ""
  ${OrIf} $0 == "0.0.0.0"
    MessageBox MB_ICONSTOP "${PRODUCT} needs the Microsoft Edge WebView2 Runtime.$\r$\n$\r$\nInstall it from https://developer.microsoft.com/microsoft-edge/webview2/ and run this setup again." /SD IDOK
    SetErrorLevel 4
    Quit
  ${EndIf}
FunctionEnd

Function un.onInit
  SetRegView 64
  SetShellVarContext all
FunctionEnd

Section "Install"
  SetOutPath "$INSTDIR"
  File "/oname=${EXE}" "${APP_EXE}"
  File "/oname=THIRD-PARTY-NOTICES.txt" "${NOTICES}"
  WriteUninstaller "$INSTDIR\uninstall.exe"

  CreateShortcut "$SMPROGRAMS\${PRODUCT}.lnk" "$INSTDIR\${EXE}"

  ; Open with: a ProgID offered for Markdown files without taking over the user's default
  ; (Windows keeps that choice with the user).
  WriteRegStr HKLM "Software\Classes\${PROGID}" "" "Markdown document"
  WriteRegStr HKLM "Software\Classes\${PROGID}\DefaultIcon" "" '"$INSTDIR\${EXE}",0'
  WriteRegStr HKLM "Software\Classes\${PROGID}\shell\open\command" "" '"$INSTDIR\${EXE}" "%1"'
  !macro ZTG_OPEN_WITH EXT
    WriteRegStr HKLM "Software\Classes\${EXT}\OpenWithProgids" "${PROGID}" ""
    WriteRegStr HKLM "Software\Classes\Applications\${EXE}\SupportedTypes" "${EXT}" ""
  !macroend
  !insertmacro ZTG_OPEN_WITH ".md"
  !insertmacro ZTG_OPEN_WITH ".markdown"
  !insertmacro ZTG_OPEN_WITH ".mdown"
  !insertmacro ZTG_OPEN_WITH ".mkd"
  WriteRegStr HKLM "Software\Classes\Applications\${EXE}" "FriendlyAppName" "${PRODUCT}"
  WriteRegStr HKLM "Software\Classes\Applications\${EXE}\shell\open\command" "" '"$INSTDIR\${EXE}" "%1"'

  ; Folder context menu: open a repository or docs folder.
  WriteRegStr HKLM "Software\Classes\Directory\shell\ZTGReadout" "" "Open in ${PRODUCT}"
  WriteRegStr HKLM "Software\Classes\Directory\shell\ZTGReadout" "Icon" '"$INSTDIR\${EXE}",0'
  WriteRegStr HKLM "Software\Classes\Directory\shell\ZTGReadout\command" "" '"$INSTDIR\${EXE}" "%1"'
  WriteRegStr HKLM "Software\Classes\Directory\Background\shell\ZTGReadout" "" "Open in ${PRODUCT}"
  WriteRegStr HKLM "Software\Classes\Directory\Background\shell\ZTGReadout" "Icon" '"$INSTDIR\${EXE}",0'
  WriteRegStr HKLM "Software\Classes\Directory\Background\shell\ZTGReadout\command" "" '"$INSTDIR\${EXE}" "%V"'

  ; Programs and Features entry.
  WriteRegStr HKLM "${UNINSTALL_KEY}" "DisplayName" "${PRODUCT}"
  WriteRegStr HKLM "${UNINSTALL_KEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKLM "${UNINSTALL_KEY}" "Publisher" "${PUBLISHER}"
  WriteRegStr HKLM "${UNINSTALL_KEY}" "URLInfoAbout" "${WEBSITE}"
  WriteRegStr HKLM "${UNINSTALL_KEY}" "DisplayIcon" '"$INSTDIR\${EXE}",0'
  WriteRegStr HKLM "${UNINSTALL_KEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKLM "${UNINSTALL_KEY}" "UninstallString" '"$INSTDIR\uninstall.exe"'
  WriteRegStr HKLM "${UNINSTALL_KEY}" "QuietUninstallString" '"$INSTDIR\uninstall.exe" /S'
  WriteRegDWORD HKLM "${UNINSTALL_KEY}" "NoModify" 1
  WriteRegDWORD HKLM "${UNINSTALL_KEY}" "NoRepair" 1
  ${GetSize} "$INSTDIR" "/S=0K" $0 $1 $2
  WriteRegDWORD HKLM "${UNINSTALL_KEY}" "EstimatedSize" $0

  System::Call 'shell32::SHChangeNotify(i 0x08000000, i 0, p 0, p 0)'
SectionEnd

Section "Uninstall"
  Delete "$INSTDIR\${EXE}"
  Delete "$INSTDIR\THIRD-PARTY-NOTICES.txt"
  Delete "$INSTDIR\uninstall.exe"
  RMDir "$INSTDIR"
  Delete "$SMPROGRAMS\${PRODUCT}.lnk"

  DeleteRegKey HKLM "Software\Classes\${PROGID}"
  !macro ZTG_REMOVE_OPEN_WITH EXT
    DeleteRegValue HKLM "Software\Classes\${EXT}\OpenWithProgids" "${PROGID}"
  !macroend
  !insertmacro ZTG_REMOVE_OPEN_WITH ".md"
  !insertmacro ZTG_REMOVE_OPEN_WITH ".markdown"
  !insertmacro ZTG_REMOVE_OPEN_WITH ".mdown"
  !insertmacro ZTG_REMOVE_OPEN_WITH ".mkd"
  DeleteRegKey HKLM "Software\Classes\Applications\${EXE}"
  DeleteRegKey HKLM "Software\Classes\Directory\shell\ZTGReadout"
  DeleteRegKey HKLM "Software\Classes\Directory\Background\shell\ZTGReadout"
  DeleteRegKey HKLM "${UNINSTALL_KEY}"

  System::Call 'shell32::SHChangeNotify(i 0x08000000, i 0, p 0, p 0)'
SectionEnd
