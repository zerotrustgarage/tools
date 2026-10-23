[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$PackageDirectory,
    [string]$Screenshots = './out/copy-native-ui'
)
$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $false
$root = Join-Path ([IO.Path]::GetTempPath()) ('ztg-copy-desktop-' + [guid]::NewGuid().ToString('N'))
$oldAppData, $oldLocalAppData = $env:APPDATA, $env:LOCALAPPDATA
$oldArguments, $oldNodePath = $env:WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS, $env:NODE_PATH
$process = $null
function Get-FixtureWebViews {
    @(Get-CimInstance Win32_Process -Filter "Name='msedgewebview2.exe'" |
        Where-Object { $_.CommandLine -and $_.CommandLine.Contains($root) })
}
function Write-FailureScreenshot {
    try {
        Add-Type -AssemblyName System.Windows.Forms
        Add-Type -AssemblyName System.Drawing
        New-Item -ItemType Directory -Path $Screenshots -Force | Out-Null
        $bounds = [Windows.Forms.SystemInformation]::VirtualScreen
        $bitmap = [Drawing.Bitmap]::new($bounds.Width, $bounds.Height)
        $graphics = [Drawing.Graphics]::FromImage($bitmap)
        try {
            $graphics.CopyFromScreen($bounds.Location, [Drawing.Point]::Empty, $bounds.Size)
            $file = Join-Path $Screenshots 'copy-native-failure.png'
            $bitmap.Save([IO.Path]::GetFullPath($file), [Drawing.Imaging.ImageFormat]::Png)
            $encoded = [Convert]::ToBase64String([IO.File]::ReadAllBytes([IO.Path]::GetFullPath($file)))
            for ($offset = 0; $offset -lt $encoded.Length; $offset += 16000) {
                Write-Output ("COPY_GUI_SCREENSHOT copy-native-failure.png $offset " + $encoded.Substring($offset, [Math]::Min(16000, $encoded.Length - $offset)))
            }
        } finally { $graphics.Dispose(); $bitmap.Dispose() }
    } catch { Write-Warning "Failure screenshot unavailable: $_" }
}
try {
    $bin = Join-Path $root 'app'
    New-Item -ItemType Directory -Path $bin -Force | Out-Null
    Copy-Item -Path (Join-Path $PackageDirectory '*') -Destination $bin
    $env:APPDATA, $env:LOCALAPPDATA = (Join-Path $root 'roaming'), (Join-Path $root 'local')
    foreach ($directory in @($env:APPDATA, $env:LOCALAPPDATA, (Join-Path $root 'source space'), (Join-Path $root 'destination'))) {
        New-Item -ItemType Directory -Path $directory -Force | Out-Null
    }
    [IO.File]::WriteAllText((Join-Path $root 'source space\café-日本語-😀.txt'), 'Desktop copy 日本語-😀', [Text.UTF8Encoding]::new($false))
    $listener = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, 0)
    $listener.Start()
    $port = $listener.LocalEndpoint.Port
    $listener.Stop()
    # Only this disposable test process inherits debugging arguments. The shipped app sets none.
    $env:WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS = "--remote-debugging-port=$port --remote-debugging-address=127.0.0.1"
    $process = Start-Process -FilePath (Join-Path $bin 'ZTGCopy.exe') -PassThru -RedirectStandardError (Join-Path $root 'app.stderr') -RedirectStandardOutput (Join-Path $root 'app.stdout')
    Write-Output "Native desktop fixture started: PID=$($process.Id), CDP=$port"
    $env:NODE_PATH = (npm root --global)
    & node ./copy/app/uitest/native.mjs $port $root $Screenshots
    if ($LASTEXITCODE -ne 0) { throw 'Real Windows desktop interaction failed' }
} catch {
    if ($process) {
        $process.Refresh()
        Write-Output "App exited=$($process.HasExited), window=$($process.MainWindowHandle), title=$($process.MainWindowTitle)"
    }
    foreach ($webview in (Get-FixtureWebViews)) {
        Write-Output "Fixture WebView PID=$($webview.ProcessId) args=$($webview.CommandLine)"
    }
    Get-NetTCPConnection -State Listen -ErrorAction SilentlyContinue |
        Where-Object { $_.LocalPort -eq $port } | Format-Table LocalAddress, LocalPort, OwningProcess
    foreach ($name in @('app.stderr','app.stdout')) {
        $file = Join-Path $root $name
        if (Test-Path -LiteralPath $file) { Get-Content -LiteralPath $file }
    }
    Write-FailureScreenshot
    throw
} finally {
    if ($process -and -not $process.HasExited) {
        $process.CloseMainWindow() | Out-Null
        if (-not $process.WaitForExit(5000)) { $process.Kill(); $process.WaitForExit() }
    }
    # Kill only WebViews whose command line contains this unique temporary fixture path.
    foreach ($webview in (Get-FixtureWebViews)) {
        Stop-Process -Id $webview.ProcessId -Force -ErrorAction SilentlyContinue
    }
    $env:APPDATA, $env:LOCALAPPDATA = $oldAppData, $oldLocalAppData
    $env:WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS, $env:NODE_PATH = $oldArguments, $oldNodePath
    for ($attempt = 0; $attempt -lt 5 -and (Test-Path -LiteralPath $root); $attempt++) {
        try { Remove-Item -LiteralPath $root -Recurse -Force }
        catch { if ($attempt -eq 4) { Write-Warning "Fixture cleanup: $_" } else { Start-Sleep -Milliseconds 300 } }
    }
}
