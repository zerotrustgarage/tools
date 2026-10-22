# RC0 only. Run on a disposable Windows machine with administrative rights.
# Creates and removes two synthetic local users, two local SMB shares and only
# its own temporary deviceless connections. Passwords never enter PowerShell.
[CmdletBinding()]
param([Parameter(Mandatory)][string]$Output)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
if (-not $IsWindows) { throw 'Real Windows is required.' }
$identity = [Security.Principal.WindowsIdentity]::GetCurrent()
$principal = [Security.Principal.WindowsPrincipal]::new($identity)
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw 'The disposable-account/share experiment requires an elevated disposable Windows runner.'
}
if (-not (Test-Path -LiteralPath $Output -PathType Container)) { throw 'Capture output folder must already exist.' }
if (Test-Path -LiteralPath (Join-Path $Output 'network.json')) { throw 'Preserve earlier network evidence.' }
Add-Type -Path (Join-Path $PSScriptRoot 'NetworkSpike.cs')
$root = Join-Path ([IO.Path]::GetTempPath()) ('ztg-copy-net0-' + [Guid]::NewGuid().ToString('N'))
[IO.Directory]::CreateDirectory($root) | Out-Null
$facts = @{ state = 'unknown'; dialect = $null; encrypted = $null; signed = $null }
$inspect = [Action[string]] {
    param($share)
    try {
        $connections = @(Get-SmbConnection -ErrorAction Stop | Where-Object ShareName -eq $share)
        if ($connections.Count -ne 1) { return }
        $connection = $connections[0]
        foreach ($entry in @(@('Dialect', 'dialect'), @('Encrypted', 'encrypted'), @('Signed', 'signed'))) {
            $property = $connection.PSObject.Properties[$entry[0]]
            if ($null -ne $property) { $facts[$entry[1]] = $property.Value }
        }
        $facts.state = 'observed'
    } catch {
        # Unknown is retained when CIM/PowerShell access is unavailable.
        $facts.state = 'unknown'
    }
}
try {
    $engine = Join-Path ([Environment]::SystemDirectory) 'Robocopy.exe'
    $native = [Ztg.CopySpike.NetworkSpike]::Run($root, $engine, [IO.Path]::GetFullPath($Output), $inspect)
    $native['smb_facts'] = $facts
    $json = $native | ConvertTo-Json -Depth 8
    $json | Set-Content -LiteralPath (Join-Path $Output 'network.json') -Encoding utf8
    Write-Output ('RC0_NETWORK ' + ($native | ConvertTo-Json -Depth 8 -Compress))
    if (-not $native['passed']) { throw 'Native network/logon experiment failed; inspect network.json.' }
} finally {
    Remove-Item -LiteralPath $root -Recurse -Force -ErrorAction Stop
}
