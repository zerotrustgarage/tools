<#
  Windows Installer lifecycle test for the ZTG Readout MSI, on a real Windows machine (CI:
  GitHub-hosted windows-2025). It needs no secret and signs nothing.

  Two modes:
    PR CI      unsigned TEST packages built in the same job. -PriorMsi is a synthetic older build
               (no released Readout MSI exists yet, so there is nothing real to upgrade from).
    release    the signed MSI from the release workflow, with -RequireSignatures. -ExpectedExe is
               the signed portable exe from the same download set; the installed exe must be
               byte-identical to it.

  Steps: [install prior], damage and repair (/fa), upgrade to the current MSI, launch with a
  file, refuse a downgrade, uninstall, and check nothing is left behind.

    pwsh scripts/release/test-readout-msi.ps1 -Msi <current.msi> -Version <X.Y.Z> -ExpectedExe <exe> `
         -Sample <file.md> [-PriorMsi <older.msi> -PriorVersion <X.Y.Z>] [-RequireSignatures] [-LogDir <dir>]
#>
param(
  [Parameter(Mandatory)] [string] $Msi,
  [Parameter(Mandatory)] [string] $Version,
  [Parameter(Mandatory)] [string] $ExpectedExe,
  [Parameter(Mandatory)] [string] $Sample,
  [string] $PriorMsi = '',
  [string] $PriorVersion = '',
  [string] $LogDir = '',
  # Package version the installed exe's file version must start with (the MSI Version can differ in CI fixtures).
  [string] $ExeVersion = '',
  [switch] $RequireSignatures
)
$ErrorActionPreference = 'Stop'
# Full Windows paths: msiexec rejects a relative path written with forward slashes (1619).
$Msi = (Resolve-Path -LiteralPath $Msi).ProviderPath
$ExpectedExe = (Resolve-Path -LiteralPath $ExpectedExe).ProviderPath
$Sample = (Resolve-Path -LiteralPath $Sample).ProviderPath
if ($PriorMsi) {
  if (-not $PriorVersion) { throw 'PriorVersion is required with PriorMsi' }
  $PriorMsi = (Resolve-Path -LiteralPath $PriorMsi).ProviderPath
}
if (-not $ExeVersion) { $ExeVersion = $Version }
if (-not $LogDir) { $LogDir = Join-Path $env:RUNNER_TEMP 'readout-msi-logs' }
New-Item -ItemType Directory -Force -Path $LogDir | Out-Null

$label = if ($RequireSignatures) { 'SIGNED RELEASE PACKAGE' } else { 'UNSIGNED TEST PACKAGE (not for distribution)' }
Write-Host "Testing: $label"
$installDir = Join-Path $env:ProgramFiles 'ZTG Readout'
$exe = Join-Path $installDir 'ztg-readout.exe'
$notices = Join-Path $installDir 'THIRD-PARTY-NOTICES.txt'
$shortcut = Join-Path $env:ProgramData 'Microsoft\Windows\Start Menu\Programs\ZTG Readout.lnk'
$webview = 'HKLM:\SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}'
$keys = @(
  'HKLM:\Software\Classes\ZTGReadout.Document\shell\open\command',
  'HKLM:\Software\Classes\Directory\shell\ZTGReadout\command',
  'HKLM:\Software\Classes\Directory\Background\shell\ZTGReadout\command',
  'HKLM:\Software\Classes\Applications\ztg-readout.exe',
  'HKLM:\Software\ZTG\Readout'
)
$step = 0

function Ok([string] $message) { Write-Host "ok: $message" }

function Run-Msi([string] $file, [string] $operation, [int] $expected = 0) {
  $script:step++
  $log = Join-Path $LogDir ('{0:D2}-msiexec{1}.log' -f $script:step, ($operation -replace '[^A-Za-z]', ''))
  $p = Start-Process msiexec.exe -ArgumentList @($operation, "`"$file`"", '/qn', '/norestart', '/l*v', "`"$log`"") -Wait -PassThru
  if ($p.ExitCode -ne $expected) {
    Get-Content $log -Tail 60
    throw "msiexec $operation $(Split-Path $file -Leaf) exited $($p.ExitCode), expected $expected (log: $log)"
  }
}

function Hash([string] $path) { (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash }

function Assert-Signed([string] $file) {
  if (-not $RequireSignatures) { return }
  $sig = Get-AuthenticodeSignature $file
  if ($sig.Status -ne 'Valid' -or $sig.SignerCertificate.Subject -notmatch '(^|,\s*)CN=Proudlock Technology LLC(,|$)' -or -not $sig.TimeStamperCertificate) {
    throw "Invalid publisher, signature or timestamp on $file : $($sig.Status)"
  }
}

function Registered-Products {
  $roots = 'HKLM:\Software\Microsoft\Windows\CurrentVersion\Uninstall', 'HKLM:\Software\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall'
  foreach ($root in $roots) {
    if (Test-Path $root) {
      Get-ChildItem $root | ForEach-Object { Get-ItemProperty $_.PSPath } |
        Where-Object { $_.DisplayName -eq 'ZTG Readout' } | ForEach-Object { [pscustomobject]@{ Version = $_.DisplayVersion; Key = $_.PSChildName } }
    }
  }
}

function Assert-Installed([string] $how, [string] $version) {
  foreach ($f in $exe, $notices, $shortcut) { if (-not (Test-Path $f)) { throw "${how}: $f missing" } }
  foreach ($k in $keys) { if (-not (Test-Path $k)) { throw "${how}: registry key missing: $k" } }
  $openWith = Get-ItemProperty 'HKLM:\Software\Classes\.md\OpenWithProgids' -ErrorAction SilentlyContinue
  if (-not $openWith -or -not ($openWith.PSObject.Properties.Name -contains 'ZTGReadout.Document')) { throw "${how}: .md Open with entry missing" }
  $command = (Get-ItemProperty 'HKLM:\Software\Classes\ZTGReadout.Document\shell\open\command').'(default)'
  if ($command -ne "`"$exe`" `"%1`"") { throw "${how}: open command is $command" }
  $products = @(Registered-Products)
  if ($products.Count -ne 1 -or $products[0].Version -ne $version) { throw "${how}: expected exactly one ZTG Readout $version registration, found: $($products | ConvertTo-Json -Compress)" }
  Ok "$how files, shortcut, registry entries and a single $version registration"
}

function Assert-Launches([string] $how) {
  $leaf = Split-Path $Sample -Leaf
  $p = Start-Process -FilePath $exe -ArgumentList @("`"$Sample`"") -PassThru
  $title = ''
  for ($i = 0; $i -lt 60 -and $title -notlike "*$leaf - ZTG Readout*"; $i++) {
    Start-Sleep -Milliseconds 500
    if ($p.HasExited) { throw "${how}: the app exited with code $($p.ExitCode)" }
    $p.Refresh()
    $title = $p.MainWindowTitle
  }
  try {
    if ($title -notlike "*$leaf - ZTG Readout*") { throw "${how}: window title is '$title'" }
  } finally {
    if (-not $p.HasExited) { Stop-Process -Id $p.Id -Force; $p.WaitForExit() }
  }
  Ok "$how launched and opened the sample; window title '$title'"
}

# Provision the test runner's WebView2 Runtime if necessary, never from inside the MSI.
if (-not (Get-ItemProperty $webview -ErrorAction SilentlyContinue).pv) {
  $bootstrapper = Join-Path $env:RUNNER_TEMP 'MicrosoftEdgeWebview2Setup.exe'
  Invoke-WebRequest 'https://go.microsoft.com/fwlink/p/?LinkId=2124703' -OutFile $bootstrapper
  $sig = Get-AuthenticodeSignature $bootstrapper
  if ($sig.Status -ne 'Valid' -or $sig.SignerCertificate.Subject -notlike '*O=Microsoft Corporation*') { throw 'Invalid Microsoft WebView2 bootstrapper signature' }
  $p = Start-Process $bootstrapper -ArgumentList @('/silent', '/install') -Wait -PassThru
  if ($p.ExitCode -ne 0) { throw "WebView2 setup exited $($p.ExitCode)" }
}

if (Test-Path $installDir) { throw 'ZTG Readout is already installed on this test runner' }
Assert-Signed $Msi
$expectedHash = Hash $ExpectedExe
$installedMsi = $null
try {
  if ($PriorMsi) {
    Run-Msi $PriorMsi '/i'
    $installedMsi = $PriorMsi
    Assert-Installed 'prior install' $PriorVersion
    $priorHash = Hash $exe
    if ($priorHash -eq $expectedHash) { throw 'the prior fixture must differ from the current build or the upgrade proves nothing' }
    # Repair: damage the installation, then reinstall every file (/fa).
    Remove-Item -LiteralPath $exe, $notices -Force
    Run-Msi $PriorMsi '/fa'
    Assert-Installed 'repair' $PriorVersion
    if ((Hash $exe) -ne $priorHash) { throw 'repair did not restore the original executable' }
    Run-Msi $Msi '/i'
    $installedMsi = $Msi
    Assert-Installed 'upgrade' $Version
    if ((Hash $exe) -eq $priorHash) { throw 'upgrade kept the previous executable' }
  } else {
    Run-Msi $Msi '/i'
    $installedMsi = $Msi
    Assert-Installed 'install' $Version
    Remove-Item -LiteralPath $exe, $notices -Force
    Run-Msi $Msi '/fa'
    Assert-Installed 'repair' $Version
  }
  if ((Hash $exe) -ne $expectedHash) { throw 'the installed executable is not the expected build' }
  Assert-Signed $exe
  Ok "installed executable is the expected build (SHA-256 $expectedHash)"
  # Tauri stamps the exe's file version from package.json. If that stopped, real upgrades would
  # keep the old file (see docs/release-readiness-readout.md).
  $fileVersion = (Get-Item -LiteralPath $exe).VersionInfo.FileVersion
  if (-not $fileVersion -or ($fileVersion -ne $ExeVersion -and $fileVersion -ne "$ExeVersion.0")) { throw "installed exe FileVersion is '$fileVersion', expected '$ExeVersion' or '$ExeVersion.0'" }
  Ok "installed executable FileVersion $fileVersion"
  Assert-Launches 'current'
  if ($PriorMsi) {
    Run-Msi $PriorMsi '/i' 1603 # downgrade refused by MajorUpgrade
    Assert-Installed 'refused downgrade' $Version
  }
  # Repair after upgrade, then a clean removal.
  Remove-Item -LiteralPath $exe -Force
  Run-Msi $Msi '/fa'
  if ((Hash $exe) -ne $expectedHash) { throw 'repair after upgrade did not restore the executable' }
  Run-Msi $Msi '/x'
  $installedMsi = $null
} finally {
  if ($installedMsi) { Start-Process msiexec.exe -ArgumentList @('/x', "`"$installedMsi`"", '/qn', '/norestart') -Wait | Out-Null }
}
for ($i = 0; $i -lt 30 -and (Test-Path $exe); $i++) { Start-Sleep -Seconds 1 }
if (Test-Path $installDir) { $left = Get-ChildItem $installDir -Recurse | ForEach-Object Name; if ($left) { throw "uninstall left files behind: $left" } }
if (Test-Path $shortcut) { throw 'uninstall left the shortcut behind' }
foreach ($k in $keys) { if (Test-Path $k) { throw "uninstall left a registry key behind: $k" } }
$openWith = Get-ItemProperty 'HKLM:\Software\Classes\.md\OpenWithProgids' -ErrorAction SilentlyContinue
if ($openWith -and ($openWith.PSObject.Properties.Name -contains 'ZTGReadout.Document')) { throw 'uninstall left the .md Open with entry behind' }
if (@(Registered-Products).Count -ne 0) { throw 'uninstall left a ZTG Readout registration behind' }
Ok 'uninstall removed files, shortcut, registry entries and the product registration'
Write-Host "Readout MSI lifecycle checks passed for the $label."
