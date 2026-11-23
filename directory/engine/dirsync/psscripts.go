package dirsync

// The fixed PowerShell scripts that drive Connect Sync on the sync server
// (design 6.2a, 6.4; runbook R3, R28). Nothing is ever spliced into them: each
// reads one JSON object from standard input (server, user, domain, password,
// operation), builds a PSCredential from it, clears the plain text, and runs a
// fixed script block on the sync server through PowerShell remoting with
// Kerberos (Invoke-Command -Authentication Kerberos; never CredSSP). The result
// is one compressed JSON object on standard output, or {"error":"message"}.
//
// Cmdlets, all documented by Microsoft:
//   - Get-ADSyncScheduler, Start-ADSyncSyncCycle -PolicyType Delta
//     https://learn.microsoft.com/en-us/entra/identity/hybrid/connect/how-to-connect-sync-feature-scheduler
//   - Get-ADSyncRunProfileResult -NumberRequested
//     https://learn.microsoft.com/en-us/entra/identity/hybrid/connect/reference-connect-adsync
//   - (Get-ADSyncGlobalSettings).Parameters, Microsoft.Synchronize.ServerConfigurationVersion
//     https://learn.microsoft.com/en-us/troubleshoot/entra/entra-id/user-prov-sync/cannot-work-automatic-upgrade
//   - AzureADConnect.exe product version
//     https://learn.microsoft.com/en-us/entra/identity/hybrid/verify-sync-tool-version
//   - Get-ADSyncEntraConnectorCredential (ConnectorIdentityType ServiceAccount or Application)
//     https://learn.microsoft.com/en-us/entra/identity/hybrid/connect/authenticate-application-id
//
// The operation check makes a script refuse input meant for the other one.

const psHead = `$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding -ArgumentList $false
$code = 0
$sec = $null
$cred = $null
try {
  $raw = [Console]::In.ReadToEnd()
  $in = ConvertFrom-Json -InputObject $raw
  Remove-Variable -Name raw
  if ([string]$in.operation -ne `

const psMid = `) { throw 'The script received input for another operation.' }
  $server = [string]$in.server
  $user = [string]$in.user
  if ([string]$in.domain) { $user = [string]$in.domain + '\' + $user }
  $sec = ConvertTo-SecureString -String ([string]$in.password) -AsPlainText -Force
  $in.password = $null
  Remove-Variable -Name in
  $cred = New-Object System.Management.Automation.PSCredential -ArgumentList $user, $sec
  $r = @(Invoke-Command -ComputerName $server -Credential $cred -Authentication Kerberos -ErrorAction Stop -ScriptBlock `

const psTail = `)
  if ($r.Count -eq 0) { throw 'The sync server returned no result.' }
  [Console]::Out.Write([string]$r[-1])
} catch {
  $code = 1
  [Console]::Out.Write((ConvertTo-Json -InputObject @{ error = [string]$_.Exception.Message } -Compress))
} finally {
  $cred = $null
  if ($null -ne $sec) { $sec.Dispose() }
  [Console]::Out.Flush()
}
exit $code
`

// healthBlock runs on the sync server and returns the state as JSON.
// Times are ISO 8601; NextSyncCycleStartTimeInUTC is UTC by its name, and the
// run history times are read as UTC when they carry no zone.
const healthBlock = `{
  $ErrorActionPreference = 'Stop'
  Import-Module ADSync
  function Format-Time($t) {
    if ($null -eq $t) { return '' }
    $d = [datetime]$t
    if ($d.Kind -eq [DateTimeKind]::Unspecified) { $d = [datetime]::SpecifyKind($d, [DateTimeKind]::Utc) }
    return $d.ToString('o', [Globalization.CultureInfo]::InvariantCulture)
  }
  $warn = New-Object System.Collections.Generic.List[string]
  $s = Get-ADSyncScheduler
  $o = [ordered]@{
    syncCycleEnabled = [bool]$s.SyncCycleEnabled
    stagingModeEnabled = [bool]$s.StagingModeEnabled
    schedulerSuspended = [bool]$s.SchedulerSuspended
    nextSyncCycleStartTimeInUTC = (Format-Time $s.NextSyncCycleStartTimeInUTC)
    intervalMinutes = 0
    configVersion = ''
    installedVersion = ''
    lastResult = ''
    lastConnector = ''
    lastProfile = ''
    lastStart = ''
    lastEnd = ''
    connectorIdentityType = ''
    warnings = @()
  }
  if ($null -ne $s.CurrentlyEffectiveSyncCycleInterval) {
    $o.intervalMinutes = [int][Math]::Round(([TimeSpan]$s.CurrentlyEffectiveSyncCycleInterval).TotalMinutes)
  }
  try {
    $p = (Get-ADSyncGlobalSettings).Parameters | Where-Object { $_.Name -eq 'Microsoft.Synchronize.ServerConfigurationVersion' } | Select-Object -First 1
    if ($null -ne $p) { $o.configVersion = [string]$p.Value }
  } catch { $warn.Add('Server configuration version: ' + $_.Exception.Message) }
  try {
    $exe = Join-Path -Path $env:ProgramFiles -ChildPath 'Microsoft Azure AD Connect\AzureADConnect.exe'
    if (Test-Path -LiteralPath $exe) { $o.installedVersion = [string](Get-Item -LiteralPath $exe).VersionInfo.ProductVersion }
  } catch { $warn.Add('Installed version: ' + $_.Exception.Message) }
  try {
    $run = Get-ADSyncRunProfileResult -NumberRequested 1 | Select-Object -First 1
    if ($null -ne $run) {
      $o.lastResult = [string]$run.Result
      $o.lastConnector = [string]$run.ConnectorName
      $o.lastProfile = [string]$run.RunProfileName
      $o.lastStart = (Format-Time $run.StartDate)
      $o.lastEnd = (Format-Time $run.EndDate)
    }
  } catch { $warn.Add('Last run: ' + $_.Exception.Message) }
  try {
    if (Get-Command -Name Get-ADSyncEntraConnectorCredential -ErrorAction SilentlyContinue) {
      $c = Get-ADSyncEntraConnectorCredential | Select-Object -First 1
      if ($null -ne $c) {
        if ($c.PSObject.Properties['ConnectorIdentityType']) { $o.connectorIdentityType = [string]$c.ConnectorIdentityType }
        else { $o.connectorIdentityType = [string]$c }
      }
    }
  } catch { $warn.Add('Authentication method: ' + $_.Exception.Message) }
  $o.warnings = $warn.ToArray()
  ConvertTo-Json -InputObject $o -Compress -Depth 3
}`

// syncBlock starts a delta cycle on the sync server.
const syncBlock = `{
  $ErrorActionPreference = 'Stop'
  Import-Module ADSync
  $res = Start-ADSyncSyncCycle -PolicyType Delta
  $text = ''
  if ($null -ne $res) {
    if ($res.PSObject.Properties['Result']) { $text = [string]$res.Result } else { $text = [string]$res }
  }
  ConvertTo-Json -InputObject @{ result = $text } -Compress
}`

// Operations named in the JSON input.
const (
	opHealth = "health"
	opSync   = "sync"
)

// HealthScript and SyncScript are the fixed scripts the Runner executes.
const (
	HealthScript = psHead + "'" + opHealth + "'" + psMid + healthBlock + psTail
	SyncScript   = psHead + "'" + opSync + "'" + psMid + syncBlock + psTail
)
