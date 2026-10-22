# RC0 only: synthetic local copies, no supplied endpoints or credentials.
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$Spike,
    [Parameter(Mandatory)][string]$Output
)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
if (-not $IsWindows) { throw 'Real Windows is required.' }
function Read-OptionalProperty($Object, [string]$Name) {
    $property = $Object.PSObject.Properties[$Name]
    if ($null -ne $property) { return $property.Value }
    return $null
}
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
        display_version = (Read-OptionalProperty $windows 'DisplayVersion')
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
$manifestPath = Join-Path $Output 'manifest.json'
if (Test-Path -LiteralPath $manifestPath) { Get-Content -LiteralPath $manifestPath }
foreach ($name in @('help-default', 'help-unicode', 'serial-copy', 'mt8-copy')) {
    $path = Join-Path $Output ($name + '.stdout.bin')
    if (Test-Path -LiteralPath $path) {
        $encoded = [Convert]::ToBase64String([IO.File]::ReadAllBytes($path))
        Write-Output ('RC0_FIXTURE ' + (@{ name = $name; base64 = $encoded } | ConvertTo-Json -Compress))
    }
}
$nativeLog = Join-Path $Output 'native-log.native.log'
if (Test-Path -LiteralPath $nativeLog) {
    Write-Output ('RC0_FIXTURE ' + (@{ name = 'native-log'; base64 = [Convert]::ToBase64String([IO.File]::ReadAllBytes($nativeLog)) } | ConvertTo-Json -Compress))
}
if ($spikeExit -ne 0) { throw "RC0 observations need investigation (spike exit $spikeExit)." }
