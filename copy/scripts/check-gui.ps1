[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$PackageDirectory,
    [string]$Screenshots = './out/copy-native-ui'
)
$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $false
Add-Type -AssemblyName UIAutomationClient
Add-Type -AssemblyName UIAutomationTypes
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
$root = Join-Path ([IO.Path]::GetTempPath()) ('ztg-copy-desktop-' + [guid]::NewGuid().ToString('N'))
$oldAppData, $oldLocalAppData = $env:APPDATA, $env:LOCALAPPDATA
$process, $window = $null, $null
function Get-FixtureWebViews {
    @(Get-CimInstance Win32_Process -Filter "Name='msedgewebview2.exe'" |
        Where-Object { $_.CommandLine -and $_.CommandLine.Contains($root) })
}
function Save-DesktopScreenshot([string]$name) {
    New-Item -ItemType Directory -Path $Screenshots -Force | Out-Null
    $bounds = [Windows.Forms.SystemInformation]::VirtualScreen
    $bitmap = [Drawing.Bitmap]::new($bounds.Width, $bounds.Height)
    $graphics = [Drawing.Graphics]::FromImage($bitmap)
    try {
        $graphics.CopyFromScreen($bounds.Location, [Drawing.Point]::Empty, $bounds.Size)
        $file = [IO.Path]::GetFullPath((Join-Path $Screenshots $name))
        $bitmap.Save($file, [Drawing.Imaging.ImageFormat]::Png)
        $encoded = [Convert]::ToBase64String([IO.File]::ReadAllBytes($file))
        for ($offset = 0; $offset -lt $encoded.Length; $offset += 16000) {
            Write-Output ("COPY_GUI_SCREENSHOT $name $offset " + $encoded.Substring($offset, [Math]::Min(16000, $encoded.Length - $offset)))
        }
    } finally { $graphics.Dispose(); $bitmap.Dispose() }
}
function Find-Control([string]$name, [string]$kind = '', [int]$timeout = 20) {
    $deadline = [DateTime]::UtcNow.AddSeconds($timeout)
    do {
        $condition = [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::NameProperty, $name)
        foreach ($element in $window.FindAll([Windows.Automation.TreeScope]::Descendants, $condition)) {
            if (-not $kind -or $element.Current.ControlType.ProgrammaticName -eq "ControlType.$kind") {
                return $element
            }
        }
        Start-Sleep -Milliseconds 150
    } while ([DateTime]::UtcNow -lt $deadline)
    throw "Native control not found: '$name' ($kind)"
}
function Click-Button([string]$prefix) {
    $deadline = [DateTime]::UtcNow.AddSeconds(20)
    do {
        $condition = [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::ControlTypeProperty, [Windows.Automation.ControlType]::Button)
        foreach ($element in $window.FindAll([Windows.Automation.TreeScope]::Descendants, $condition)) {
            if ($element.Current.Name.StartsWith($prefix) -and $element.Current.IsEnabled -and -not $element.Current.IsOffscreen) {
                $pattern = $element.GetCurrentPattern([Windows.Automation.InvokePattern]::Pattern)
                $pattern.Invoke()
                return
            }
        }
        Start-Sleep -Milliseconds 150
    } while ([DateTime]::UtcNow -lt $deadline)
    throw "Native button not ready: '$prefix'"
}
function Set-Edit([string]$name, [string]$value) {
    $element = Find-Control $name 'Edit'
    $pattern = $element.GetCurrentPattern([Windows.Automation.ValuePattern]::Pattern)
    $pattern.SetValue($value)
}
function Wait-CompletedRecord([int]$after, [string]$state) {
    $deadline = [DateTime]::UtcNow.AddSeconds(30)
    do {
        $files = @(Get-ChildItem -Path (Join-Path $env:LOCALAPPDATA 'ZTG\Copy\runs') -Filter run.json -Recurse -ErrorAction SilentlyContinue)
        if ($files.Count -gt $after) {
            foreach ($file in ($files | Sort-Object LastWriteTimeUtc -Descending)) {
                try { $record = Get-Content -LiteralPath $file.FullName -Raw | ConvertFrom-Json } catch { continue }
                if ($record.state -eq $state) { return $record }
            }
        }
        Start-Sleep -Milliseconds 150
    } while ([DateTime]::UtcNow -lt $deadline)
    throw "Native operation did not finish with state '$state'"
}
try {
    $bin = Join-Path $root 'app'
    New-Item -ItemType Directory -Path $bin -Force | Out-Null
    Copy-Item -Path (Join-Path $PackageDirectory '*') -Destination $bin
    $env:APPDATA, $env:LOCALAPPDATA = (Join-Path $root 'roaming'), (Join-Path $root 'local')
    $source, $destination = (Join-Path $root 'source space'), (Join-Path $root 'destination')
    foreach ($directory in @($env:APPDATA, $env:LOCALAPPDATA, $source, $destination)) {
        New-Item -ItemType Directory -Path $directory -Force | Out-Null
    }
    $fileName, $payload = 'café-日本語-😀.txt', 'Desktop copy 日本語-😀'
    [IO.File]::WriteAllText((Join-Path $source $fileName), $payload, [Text.UTF8Encoding]::new($false))
    # Exercise the packaged executable through native accessibility. Wails intentionally
    # refuses environment overrides for remote debugging; no alternate/debug build is used.
    $process = Start-Process -FilePath (Join-Path $bin 'ZTGCopy.exe') -PassThru -RedirectStandardError (Join-Path $root 'app.stderr') -RedirectStandardOutput (Join-Path $root 'app.stdout')
    $deadline = [DateTime]::UtcNow.AddSeconds(30)
    do {
        $process.Refresh()
        if ($process.HasExited) { throw 'Desktop application exited before opening' }
        if ($process.MainWindowHandle -ne 0) {
            $window = [Windows.Automation.AutomationElement]::FromHandle($process.MainWindowHandle)
            break
        }
        Start-Sleep -Milliseconds 150
    } while ([DateTime]::UtcNow -lt $deadline)
    if (-not $window) { throw 'Desktop window did not open' }
    Write-Output "Native desktop started: $($process.MainWindowTitle)"
    Find-Control 'SOURCE' 'Edit' | Out-Null
    Set-Edit 'SOURCE' $source
    Set-Edit 'DESTINATION' $destination
    Click-Button 'Preview this job'
    Find-Control 'Preview ready' | Out-Null
    $plan = Wait-CompletedRecord 0 'planned'
    if (Test-Path -LiteralPath (Join-Path $destination $fileName)) { throw 'Preview changed destination' }
    if ($plan.normalized_exit -ne 0) { throw 'Preview was not successful' }
    Click-Button 'Start copy'
    Click-Button 'Start copy'
    Find-Control 'Copy complete' | Out-Null
    $copied = Wait-CompletedRecord 1 'copied'
    if ([IO.File]::ReadAllText((Join-Path $destination $fileName)) -ne $payload) { throw 'Copied Unicode file content differs' }
    $verification = @($copied.phases | Where-Object { $_.name -eq 'convergence' })
    if ($copied.normalized_exit -ne 0 -or $verification.Count -ne 1 -or $verification[0].process.native_exit -ne 0) { throw 'Copy did not pass convergence' }
    Save-DesktopScreenshot 'copy-native-complete.png'
    # Import a throttled job through the real command dialog so Stop is tested
    # while the contained engine is still copying, rather than after a fast copy.
    [IO.File]::WriteAllBytes((Join-Path $source 'slow.bin'), [byte[]]::new(8MB))
    Click-Button 'Back to designer'
    Click-Button 'Explain a command'
    Set-Edit 'Robocopy command' ('robocopy "' + $source + '" "' + $destination + '" /E /R:2 /W:5 /XJ /IORATE:1048576 /NOOFFLOAD /NOCLONE')
    Click-Button 'Import and explain'
    Click-Button 'Preview this job'
    Find-Control 'Preview ready' | Out-Null
    $secondPlan = Wait-CompletedRecord 2 'planned'
    Click-Button 'Start copy'
    Click-Button 'Start copy'
    Find-Control 'Copying' | Out-Null
    Click-Button 'Stop'
    Find-Control 'Operation stopped' | Out-Null
    $stopped = Wait-CompletedRecord 3 'canceled'
    if ($stopped.normalized_exit -ne 3) { throw 'Stopped copy was reported as success' }
    Save-DesktopScreenshot 'copy-native-stopped.png'
    Write-Output 'COPY_NATIVE_GUI {"passed":true,"packaged_executable":true,"native_accessibility":true,"preview_unchanged":true,"unicode_content_equal":true,"convergence":true,"stop_canceled":true}'
} catch {
    if ($window) {
        foreach ($element in $window.FindAll([Windows.Automation.TreeScope]::Descendants, [Windows.Automation.Condition]::TrueCondition)) {
            try { Write-Output ("UIA " + $element.Current.ControlType.ProgrammaticName + " id=" + $element.Current.AutomationId + " name=" + $element.Current.Name) } catch {}
        }
    }
    foreach ($name in @('app.stderr','app.stdout')) {
        $file = Join-Path $root $name
        if (Test-Path -LiteralPath $file) { Get-Content -LiteralPath $file }
    }
    try { Save-DesktopScreenshot 'copy-native-failure.png' } catch { Write-Warning "Failure screenshot unavailable: $_" }
    throw
} finally {
    if ($process -and -not $process.HasExited) {
        $process.CloseMainWindow() | Out-Null
        if (-not $process.WaitForExit(5000)) { $process.Kill(); $process.WaitForExit() }
    }
    foreach ($webview in (Get-FixtureWebViews)) { Stop-Process -Id $webview.ProcessId -Force -ErrorAction SilentlyContinue }
    $env:APPDATA, $env:LOCALAPPDATA = $oldAppData, $oldLocalAppData
    for ($attempt = 0; $attempt -lt 5 -and (Test-Path -LiteralPath $root); $attempt++) {
        try { Remove-Item -LiteralPath $root -Recurse -Force }
        catch { if ($attempt -eq 4) { Write-Warning "Fixture cleanup: $_" } else { Start-Sleep -Milliseconds 300 } }
    }
}
