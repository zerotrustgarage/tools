[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$Archive,
    [string]$Screenshots = './out/copy-native-ui'
)
$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $false
$root = Join-Path ([IO.Path]::GetTempPath()) ('ztg-copy-desktop-' + [guid]::NewGuid().ToString('N'))
$oldAppData, $oldLocalAppData = $env:APPDATA, $env:LOCALAPPDATA
$oldArguments = $env:WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS
$process = $null
try {
    $bin = Join-Path $root 'app'
    New-Item -ItemType Directory -Path $bin -Force | Out-Null
    Expand-Archive -LiteralPath $Archive -DestinationPath $bin
    $env:APPDATA, $env:LOCALAPPDATA = (Join-Path $root 'roaming'), (Join-Path $root 'local')
    foreach ($directory in @($env:APPDATA, $env:LOCALAPPDATA, (Join-Path $root 'source space'), (Join-Path $root 'destination'))) {
        New-Item -ItemType Directory -Path $directory -Force | Out-Null
    }
    [IO.File]::WriteAllText((Join-Path $root 'source space\café-日本語-😀.txt'), 'Desktop copy 日本語-😀', [Text.UTF8Encoding]::new($false))
    $listener = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, 0)
    $listener.Start()
    $port = $listener.LocalEndpoint.Port
    $listener.Stop()
    # Test-only remote debugging, bound to localhost; the shipped app sets no debugging flags.
    $env:WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS = "--remote-debugging-port=$port"
    $process = Start-Process -FilePath (Join-Path $bin 'ZTGCopy.exe') -PassThru -RedirectStandardError (Join-Path $root 'app.stderr') -RedirectStandardOutput (Join-Path $root 'app.stdout')
    $env:NODE_PATH = (npm root --global)
    & node ./copy/app/uitest/native.mjs $port $root $Screenshots
    if ($LASTEXITCODE -ne 0) { throw 'Real Windows desktop interaction failed' }
} catch {
    foreach ($name in @('app.stderr','app.stdout')) {
        $file = Join-Path $root $name
        if (Test-Path -LiteralPath $file) { Get-Content -LiteralPath $file }
    }
    throw
} finally {
    if ($process -and -not $process.HasExited) {
        $process.CloseMainWindow() | Out-Null
        if (-not $process.WaitForExit(5000)) { $process.Kill(); $process.WaitForExit() }
    }
    $env:APPDATA, $env:LOCALAPPDATA = $oldAppData, $oldLocalAppData
    $env:WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS = $oldArguments
    if (Test-Path -LiteralPath $root) { Remove-Item -LiteralPath $root -Recurse -Force }
}
