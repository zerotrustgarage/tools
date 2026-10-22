<#
  Installs ZTG Readout on a real Windows machine (CI: GitHub-hosted windows-2025), checks what
  each installer put on disk and in the registry, launches the app, and uninstalls it again.
  The Wine test (test-installer-wine.sh) covers the installer logic on Linux; this one covers what
  Wine cannot: the app itself starting on WebView2 and loading its UI.

  The native window starts with the static title "ZTG Readout" (tauri.conf.json); only the app's
  own JavaScript changes it to "<file> - ZTG Readout". Seeing that title therefore proves the
  WebView loaded the frontend and the frontend reached the Rust side. Opening a
  sample file by command line proves the file association path (Rust reads the file, the UI
  shows its name).

    pwsh scripts/ci/test-installer-windows.ps1 -Setup <Setup.exe> -Msi <.msi> -Sample <file.md> [-Shots <dir>]
#>
param(
  [Parameter(Mandatory)] [string] $Setup,
  [Parameter(Mandatory)] [string] $Msi,
  [Parameter(Mandatory)] [string] $Sample,
  [string] $Shots = ''
)
$ErrorActionPreference = 'Stop'
# Full Windows paths: msiexec rejects a relative path written with forward slashes (1619).
$Setup, $Msi, $Sample = $Setup, $Msi, $Sample | ForEach-Object { (Resolve-Path -LiteralPath $_).ProviderPath }
$installDir = Join-Path $env:ProgramFiles 'ZTG Readout'
$exe = Join-Path $installDir 'ztg-readout.exe'
$shortcut = Join-Path $env:ProgramData 'Microsoft\Windows\Start Menu\Programs\ZTG Readout.lnk'
$webview = 'HKLM:\SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}'
$keys = @(
  'HKLM:\Software\Classes\ZTGReadout.Document\shell\open\command',
  'HKLM:\Software\Classes\Directory\shell\ZTGReadout\command',
  'HKLM:\Software\Classes\Directory\Background\shell\ZTGReadout\command',
  'HKLM:\Software\Classes\Applications\ztg-readout.exe'
)

function Fail([string] $message) { Write-Host "FAIL: $message"; exit 1 }
function Ok([string] $message) { Write-Host "ok: $message" }

function Run([string] $file, [string[]] $arguments) {
  $p = Start-Process -FilePath $file -ArgumentList $arguments -Wait -PassThru
  return $p.ExitCode
}

function Assert-Installed([string] $how) {
  foreach ($f in 'ztg-readout.exe', 'THIRD-PARTY-NOTICES.txt') {
    if (-not (Test-Path (Join-Path $installDir $f))) { Fail "${how}: $f missing" }
  }
  if (-not (Test-Path $shortcut)) { Fail "${how}: Start menu shortcut missing (expected the All Users Start Menu)" }
  foreach ($k in $keys) { if (-not (Test-Path $k)) { Fail "${how}: registry key missing: $k" } }
  $openWith = Get-ItemProperty 'HKLM:\Software\Classes\.md\OpenWithProgids' -ErrorAction SilentlyContinue
  if (-not $openWith -or -not ($openWith.PSObject.Properties.Name -contains 'ZTGReadout.Document')) { Fail "${how}: .md Open with entry missing" }
  $command = (Get-ItemProperty 'HKLM:\Software\Classes\ZTGReadout.Document\shell\open\command').'(default)'
  if ($command -ne "`"$exe`" `"%1`"") { Fail "${how}: open command is $command" }
  $sig = Get-AuthenticodeSignature $exe
  Ok "$how installed files, shortcut and registry entries (exe signature status: $($sig.Status))"
}

function Assert-Removed([string] $how) {
  for ($i = 0; $i -lt 30 -and (Test-Path $exe); $i++) { Start-Sleep -Seconds 1 }
  if (Test-Path $exe) { Fail "${how}: ztg-readout.exe left behind" }
  if (Test-Path $shortcut) { Fail "${how}: shortcut left behind" }
  foreach ($k in $keys) { if (Test-Path $k) { Fail "${how}: registry key left behind: $k" } }
  $openWith = Get-ItemProperty 'HKLM:\Software\Classes\.md\OpenWithProgids' -ErrorAction SilentlyContinue
  if ($openWith -and ($openWith.PSObject.Properties.Name -contains 'ZTGReadout.Document')) { Fail "${how}: .md Open with entry left behind" }
  Ok "$how uninstall removed files, shortcut and registry entries"
}

function Save-Screen([string] $name) {
  if (-not $Shots) { return }
  try {
    Add-Type -AssemblyName System.Windows.Forms, System.Drawing
    $bounds = [System.Windows.Forms.Screen]::PrimaryScreen.Bounds
    $bitmap = New-Object System.Drawing.Bitmap $bounds.Width, $bounds.Height
    $graphics = [System.Drawing.Graphics]::FromImage($bitmap)
    $graphics.CopyFromScreen($bounds.Location, [System.Drawing.Point]::Empty, $bounds.Size)
    New-Item -ItemType Directory -Force -Path $Shots | Out-Null
    $bitmap.Save((Join-Path $Shots "$name.png"), [System.Drawing.Imaging.ImageFormat]::Png)
    $graphics.Dispose(); $bitmap.Dispose()
  } catch { Write-Host "note: screenshot $name not taken: $($_.Exception.Message)" }
}

function Assert-Launches([string] $how, [string[]] $arguments, [string] $expectTitle) {
  $p = if ($arguments) { Start-Process -FilePath $exe -ArgumentList $arguments -PassThru } else { Start-Process -FilePath $exe -PassThru }
  $title = ''
  for ($i = 0; $i -lt 40 -and $title -notlike "*$expectTitle*"; $i++) {
    Start-Sleep -Milliseconds 500
    if ($p.HasExited) { Fail "${how}: the app exited with code $($p.ExitCode)" }
    $p.Refresh()
    $title = $p.MainWindowTitle
  }
  if ($title -notlike "*$expectTitle*") { Fail "${how}: window title is '$title', expected it to contain '$expectTitle'" }
  Start-Sleep -Seconds 2
  Save-Screen ($how -replace '[^A-Za-z0-9-]', '-')
  Stop-Process -Id $p.Id -Force
  $p.WaitForExit()
  Ok "$how launched; window title '$title'"
}

# ---- WebView2 Runtime: the installers refuse without it -----------------------------------
if (-not (Test-Path $webview) -or -not (Get-ItemProperty $webview -ErrorAction SilentlyContinue).pv) {
  $code = Run $Setup @('/S')
  if ($code -ne 4) { Fail "without WebView2 the NSIS installer should exit 4, got $code" }
  Ok 'NSIS installer refuses without the WebView2 Runtime (exit 4)'
  $bootstrapper = Join-Path $env:RUNNER_TEMP 'MicrosoftEdgeWebview2Setup.exe'
  Invoke-WebRequest 'https://go.microsoft.com/fwlink/p/?LinkId=2124703' -OutFile $bootstrapper
  $sig = Get-AuthenticodeSignature $bootstrapper
  if ($sig.Status -ne 'Valid' -or $sig.SignerCertificate.Subject -notlike '*O=Microsoft Corporation*') { Fail "WebView2 bootstrapper signature: $($sig.Status) $($sig.SignerCertificate.Subject)" }
  if ((Run $bootstrapper @('/silent', '/install')) -ne 0) { Fail 'WebView2 Runtime install failed' }
  Ok 'installed the WebView2 Runtime (Microsoft-signed bootstrapper)'
} else {
  Ok "WebView2 Runtime present ($((Get-ItemProperty $webview).pv))"
}

# ---- NSIS installer ------------------------------------------------------------------------
$code = Run $Setup @('/S')
if ($code -ne 0) { Fail "NSIS silent install exited $code" }
Assert-Installed 'NSIS'
Assert-Launches 'NSIS launch' @() 'Untitled.md - ZTG Readout'
Assert-Launches 'NSIS open file' @("`"$Sample`"") "$(Split-Path $Sample -Leaf) - ZTG Readout"
$code = Run (Join-Path $installDir 'uninstall.exe') @('/S')
if ($code -ne 0) { Fail "NSIS silent uninstall exited $code" }
Assert-Removed 'NSIS'

# ---- MSI -----------------------------------------------------------------------------------
$log = Join-Path $env:RUNNER_TEMP 'readout-msi-install.log'
$code = Run 'msiexec.exe' @('/i', "`"$Msi`"", '/qn', '/norestart', '/l*v', "`"$log`"")
if ($code -ne 0) { Get-Content $log -Tail 40; Fail "MSI install exited $code" }
Assert-Installed 'MSI'
Assert-Launches 'MSI launch' @() 'Untitled.md - ZTG Readout'
$code = Run 'msiexec.exe' @('/x', "`"$Msi`"", '/qn', '/norestart')
if ($code -ne 0) { Fail "MSI uninstall exited $code" }
Assert-Removed 'MSI'

Write-Host 'All Windows installer checks passed.'
