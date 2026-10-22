# RC0 only: synthetic local copies, no supplied endpoints or credentials.
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$Spike,
    [Parameter(Mandatory)][string]$Output
)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
if (-not $IsWindows) { throw 'Real Windows is required.' }
$parent = Split-Path -Parent ([IO.Path]::GetFullPath($Output))
[IO.Directory]::CreateDirectory($parent) | Out-Null
if (Test-Path -LiteralPath $Output) { throw 'Output folder already exists; preserve earlier evidence.' }
& $Spike --output $Output
$spikeExit = $LASTEXITCODE
if (Test-Path -LiteralPath $Output) {
    $engine = Join-Path ([Environment]::SystemDirectory) 'Robocopy.exe'
    $signature = Get-AuthenticodeSignature -LiteralPath $engine
    $version = (Get-Item -LiteralPath $engine).VersionInfo
    $windows = Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion'
    [ordered]@{
        schema = 1
        captured_at = [DateTime]::UtcNow.ToString('o')
        os_description = [Runtime.InteropServices.RuntimeInformation]::OSDescription
        display_version = $windows.DisplayVersion
        build = $windows.CurrentBuildNumber
        revision = $windows.UBR
        culture = [Globalization.CultureInfo]::CurrentCulture.Name
        ui_culture = [Globalization.CultureInfo]::CurrentUICulture.Name
        powershell = $PSVersionTable.PSVersion.ToString()
        engine = $engine
        file_version = $version.FileVersion
        product_version = $version.ProductVersion
        sha256 = (Get-FileHash -LiteralPath $engine -Algorithm SHA256).Hash
        signature_status = $signature.Status.ToString()
        signature_type = $signature.SignatureType.ToString()
        spike_exit = $spikeExit
    } | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath (Join-Path $Output 'environment.json') -Encoding utf8
}
if ($spikeExit -ne 0) { throw "RC0 observations need investigation (spike exit $spikeExit)." }
