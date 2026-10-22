<# Tests the actual release payload. No scan is started. Requires an elevated Windows
   test runner (windows-2025). The signed release uses -RequireSignatures; PR CI uses
   unsigned packages. UpgradeMsi is optional and used only by PR CI. #>
param(
  [Parameter(Mandatory)] [string] $Msi,
  [Parameter(Mandatory)] [string] $Portable,
  [Parameter(Mandatory)] [string] $Version,
  [string] $UpgradeMsi = '',
  [switch] $RequireSignatures
)
$ErrorActionPreference = 'Stop'
$Msi = (Resolve-Path $Msi).Path
$Portable = (Resolve-Path $Portable).Path
if ($UpgradeMsi) { $UpgradeMsi = (Resolve-Path $UpgradeMsi).Path }
$installDir = Join-Path $env:ProgramFiles 'ZTG Scanner'
$shortcut = Join-Path $env:ProgramData 'Microsoft\Windows\Start Menu\Programs\ZTG Scanner.lnk'
$files = @('ZTGScanner.exe', 'ztg-scanner.exe', 'README.txt', 'THIRD-PARTY-NOTICES.txt', 'scanner.cdx.json')
$unpack = Join-Path $env:RUNNER_TEMP 'scanner-portable'

function Run-Msi([string] $file, [string] $operation, [int] $expected = 0) {
  $log = Join-Path $env:RUNNER_TEMP "scanner-msi-$([guid]::NewGuid()).log"
  $p = Start-Process msiexec.exe -ArgumentList @($operation, "`"$file`"", '/qn', '/norestart', '/l*v', "`"$log`"") -Wait -PassThru
  if ($p.ExitCode -ne $expected) { Get-Content $log -Tail 60; throw "msiexec $operation exited $($p.ExitCode), expected $expected" }
}
function Assert-Signed([string] $file) {
  if (-not $RequireSignatures) { return }
  $sig = Get-AuthenticodeSignature $file
  if ($sig.Status -ne 'Valid' -or $sig.SignerCertificate.Subject -notmatch '(^|,\s*)CN=Proudlock Technology LLC(,|$)' -or -not $sig.TimeStamperCertificate) {
    throw "Invalid publisher, signature or timestamp on $file : $($sig.Status)"
  }
}
function Assert-Payload([string] $dir, [string] $version) {
  foreach ($file in $files) {
    if (-not (Test-Path (Join-Path $dir $file))) { throw "Missing $file in $dir" }
    if ($RequireSignatures) {
      if ($file.EndsWith('.exe')) { Assert-Signed (Join-Path $dir $file) }
    }
  }
  $cli = Join-Path $dir 'ztg-scanner.exe'
  $reported = & $cli version
  if ($LASTEXITCODE -ne 0 -or $reported -notlike "ztg-scanner $version+*") { throw "CLI version: $reported" }
  $p = Start-Process (Join-Path $dir 'ZTGScanner.exe') -PassThru
  try {
    $ready = $false
    for ($i = 0; $i -lt 60; $i++) {
      Start-Sleep -Milliseconds 500
      $p.Refresh()
      if ($p.HasExited) { throw "Desktop app exited: $($p.ExitCode)" }
      # Only the frontend sets this versioned title after calling the Go binding.
      if ($p.MainWindowTitle -like "ZTG Scanner $version+*") { $ready = $true; break }
    }
    if (-not $ready) { throw 'Desktop frontend did not become ready' }
  } finally {
    if (-not $p.HasExited) { Stop-Process -Id $p.Id -Force; $p.WaitForExit() }
  }
}

# Provision the test runner if necessary, never from inside the MSI.
$runtime = 'HKLM:\SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}'
if (-not (Get-ItemProperty $runtime -ErrorAction SilentlyContinue).pv) {
  $bootstrapper = Join-Path $env:RUNNER_TEMP 'MicrosoftEdgeWebview2Setup.exe'
  Invoke-WebRequest 'https://go.microsoft.com/fwlink/p/?LinkId=2124703' -OutFile $bootstrapper
  $sig = Get-AuthenticodeSignature $bootstrapper
  if ($sig.Status -ne 'Valid' -or $sig.SignerCertificate.Subject -notlike '*O=Microsoft Corporation*') { throw 'Invalid Microsoft WebView2 bootstrapper signature' }
  $p = Start-Process $bootstrapper -ArgumentList @('/silent', '/install') -Wait -PassThru
  if ($p.ExitCode -ne 0) { throw "WebView2 setup exited $($p.ExitCode)" }
}

Assert-Signed $Msi
Expand-Archive -Path $Portable -DestinationPath $unpack -Force
Assert-Payload $unpack $Version
if (Test-Path $installDir) { throw 'Scanner is already installed on this test runner' }
$installed = $false
$currentMsi = $Msi
try {
  Run-Msi $Msi '/i'
  $installed = $true
  if (-not (Test-Path $shortcut)) { throw 'All-users Start menu shortcut missing' }
  Assert-Payload $installDir $Version
  foreach ($file in $files) {
    if ((Get-FileHash (Join-Path $unpack $file)).Hash -ne (Get-FileHash (Join-Path $installDir $file)).Hash) {
      throw "MSI and portable payload differ: $file"
    }
  }
  # Reinstall/repair must keep the installation working.
  Run-Msi $Msi '/fa'
  Assert-Payload $installDir $Version
  if ($UpgradeMsi) {
    Run-Msi $UpgradeMsi '/i'
    $currentMsi = $UpgradeMsi
    Assert-Payload $installDir $Version
    Run-Msi $Msi '/i' 1603 # downgrade is refused
  }
} finally {
  if ($installed) { Run-Msi $currentMsi '/x' }
  Remove-Item $unpack -Recurse -Force
}
foreach ($file in $files) {
  if (Test-Path (Join-Path $installDir $file)) { throw "Uninstall left $file behind" }
}
if (Test-Path $shortcut) { throw 'Uninstall left the shortcut behind' }
if (Test-Path 'HKLM:\Software\ZTG\Scanner') { throw 'Uninstall left Scanner registry entries behind' }
Write-Host 'Scanner portable, install, launch, repair, upgrade/downgrade and uninstall checks passed.'
