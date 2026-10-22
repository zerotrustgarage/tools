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
$installDir = Join-Path $env:ProgramFiles 'ZTG TFTP'
$shortcut = Join-Path $env:ProgramData 'Microsoft\Windows\Start Menu\Programs\ZTG TFTP.lnk'
$files = @('ZTGTFTP.exe', 'ztg-tftp.exe', 'README.txt', 'THIRD-PARTY-NOTICES.txt', 'tftp.cdx.json')
$unpack = Join-Path $env:RUNNER_TEMP 'tftp-portable'

function Run-Msi([string] $file, [string] $operation, [int] $expected = 0) {
  $log = Join-Path $env:RUNNER_TEMP "tftp-msi-$([guid]::NewGuid()).log"
  $p = Start-Process msiexec.exe -ArgumentList @($operation, "`"$file`"", '/qn', '/norestart', '/l*v', "`"$log`"") -Wait -PassThru
  if ($p.ExitCode -ne $expected) { Select-String -Path $log -Pattern 'Return value 3|Error 17|CustomAction|returned actual error' -Context 8,8 | Out-String | Write-Host; Get-Content $log -Tail 60; throw "msiexec $operation exited $($p.ExitCode), expected $expected" }
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
  $cli = Join-Path $dir 'ztg-tftp.exe'
  $reported = & $cli version
  if ($LASTEXITCODE -ne 0 -or $reported -notlike "ztg-tftp $version+*") { throw "CLI version: $reported" }
  $p = Start-Process (Join-Path $dir 'ZTGTFTP.exe') -PassThru
  try {
    $ready = $false
    for ($i = 0; $i -lt 60; $i++) {
      Start-Sleep -Milliseconds 500
      $p.Refresh()
      if ($p.HasExited) { throw "Desktop app exited: $($p.ExitCode)" }
      # Only the frontend sets this versioned title after calling the Go binding.
      if ($p.MainWindowTitle -like "ZTG TFTP $version+*") { $ready = $true; break }
    }
    if (-not $ready) { throw 'Desktop frontend did not become ready' }
  } finally {
    if (-not $p.HasExited) { Stop-Process -Id $p.Id -Force; $p.WaitForExit() }
  }
}

function Assert-Firewall {
  $rules = @(Get-NetFirewallRule -DisplayName 'ZTG TFTP *{2082e148-404e-4a7c-8477-f7472143b644}' -ErrorAction SilentlyContinue)
  if ($rules.Count -ne 2) { throw "Expected two TFTP program rules, found $($rules.Count)" }
  foreach ($rule in $rules) {
    if ([int]$rule.Profile -ne 3 -or $rule.Direction -ne 'Inbound' -or $rule.Action -ne 'Allow') { throw 'Firewall scope is incorrect' }
    if (($rule | Get-NetFirewallPortFilter).Protocol -ne 'UDP') { throw 'Firewall protocol is incorrect' }
    $program = ($rule | Get-NetFirewallApplicationFilter).Program
    if (-not $program.StartsWith($installDir + '\')) { throw "Unexpected firewall program $program" }
  }
}
function Test-Service {
  $base = Join-Path $env:ProgramData ('ZTGTFTP-CI-' + [guid]::NewGuid())
  New-Item -ItemType Directory -Path "$base\images" -Force | Out-Null
  & icacls.exe $base /grant '*S-1-5-19:(OI)(CI)F' | Out-Null
  if ($LASTEXITCODE -ne 0) { throw 'Cannot grant LocalService access to test data' }
  Set-Content "$base\images\boot.txt" 'test data'
  $config = Join-Path $base 'config.json'
  @{root="$base\images";audit_dir="$base\audit";bind='127.0.0.1';port=49699;duration='1h';indefinite=$true;event_log=$true} | ConvertTo-Json | Set-Content $config
  $cli = Join-Path $installDir 'ztg-tftp.exe'
  $created = $false
  try {
    & $cli service install --config $config
    if ($LASTEXITCODE -ne 0) { throw 'Service installation failed' }
    $created = $true
    Start-Service ZTGTFTP
    $service = Get-Service ZTGTFTP
    $service.WaitForStatus('Running', [TimeSpan]::FromSeconds(15))
    & $cli get --port 49699 --timeout 1 --for 15s 127.0.0.1 boot.txt "$base\download.txt"
    if ($LASTEXITCODE -ne 0) { throw 'Windows service did not serve a file' }
    & sc.exe control ZTGTFTP 6 | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'SCM configuration reload failed' }
  } finally {
    if ($created) {
      Stop-Service ZTGTFTP -ErrorAction SilentlyContinue
      (Get-Service ZTGTFTP).WaitForStatus('Stopped', [TimeSpan]::FromSeconds(15))
      & $cli service remove
      if ($LASTEXITCODE -ne 0) { throw 'Service removal failed' }
    }
    Remove-Item $base -Recurse -Force
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
# Exercise the exact elevated helper directly so netsh diagnostics remain visible.
Start-Service MpsSvc
& (Join-Path $unpack 'ztg-tftp.exe') firewall install
if ($LASTEXITCODE -ne 0) { throw 'Portable firewall helper preflight failed' }
& (Join-Path $unpack 'ztg-tftp.exe') firewall remove
if ($LASTEXITCODE -ne 0) { throw 'Portable firewall cleanup failed' }
if (Test-Path $installDir) { throw 'TFTP is already installed on this test runner' }
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
  Assert-Firewall
  Test-Service
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
if (@(Get-NetFirewallRule -DisplayName 'ZTG TFTP *{2082e148-404e-4a7c-8477-f7472143b644}' -ErrorAction SilentlyContinue).Count -ne 0) { throw 'Uninstall left firewall rules' }
if (Test-Path 'HKLM:\Software\ZTG\TFTP') { throw 'Uninstall left TFTP registry entries behind' }
Write-Host 'TFTP portable, install, launch, repair, upgrade/downgrade and uninstall checks passed.'
