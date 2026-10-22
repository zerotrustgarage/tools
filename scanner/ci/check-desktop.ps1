<# Exercise the real WebView2 window through Windows UI Automation and screen pixels.
   Run with in-box Windows PowerShell for the .NET Framework UIAutomation assemblies. #>
param(
  [Parameter(Mandatory)] [int] $AppProcessId,
  [Parameter(Mandatory)] [string] $Version,
  [Parameter(Mandatory)] [string] $Shots
)
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName UIAutomationClient, UIAutomationTypes, System.Windows.Forms, System.Drawing
Add-Type @'
using System;
using System.Runtime.InteropServices;
public static class ScannerDesktop {
  [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr hWnd);
  [DllImport("user32.dll")] public static extern bool ShowWindow(IntPtr hWnd, int command);
}
'@
$p = Get-Process -Id $AppProcessId
$handle = $p.MainWindowHandle
[ScannerDesktop]::ShowWindow($handle, 3) | Out-Null
[ScannerDesktop]::SetForegroundWindow($handle) | Out-Null
Start-Sleep -Milliseconds 500
$window = [System.Windows.Automation.AutomationElement]::FromHandle($handle)
$scope = [System.Windows.Automation.TreeScope]::Descendants

function Find-Control([string] $name, [System.Windows.Automation.ControlType] $type) {
  $condition = [System.Windows.Automation.AndCondition]::new(
    [System.Windows.Automation.AndCondition]::new(
      [System.Windows.Automation.PropertyCondition]::new([System.Windows.Automation.AutomationElement]::NameProperty, $name),
      [System.Windows.Automation.PropertyCondition]::new([System.Windows.Automation.AutomationElement]::ControlTypeProperty, $type)
    ),
    [System.Windows.Automation.PropertyCondition]::new([System.Windows.Automation.AutomationElement]::FrameworkIdProperty, 'Chrome')
  )
  # Chromium creates its accessibility tree lazily on the first UIA request.
  $found = $null
  for ($attempt = 0; $attempt -lt 100; $attempt++) {
    $found = $window.FindFirst($scope, $condition)
    if ($found -and -not $found.Current.IsOffscreen) { break }
    Start-Sleep -Milliseconds 100
  }
  if (-not $found) {
    $window.FindAll($scope, [System.Windows.Automation.Condition]::TrueCondition) | ForEach-Object {
      Write-Host "$($_.Current.ControlType.ProgrammaticName): $($_.Current.Name) [$($_.Current.FrameworkId)] offscreen=$($_.Current.IsOffscreen) bounds=$($_.Current.BoundingRectangle)"
    }
    throw "Visible desktop control not found: $name"
  }
  return $found
}

$theme = Find-Control 'Theme' ([System.Windows.Automation.ControlType]::ComboBox)
$settings = Find-Control 'Settings' ([System.Windows.Automation.ControlType]::Button)
$updates = Find-Control 'Check for updates' ([System.Windows.Automation.ControlType]::Button)
$minimize = Find-Control 'Minimize' ([System.Windows.Automation.ControlType]::Button)
foreach ($control in @($theme, $settings, $updates)) {
  if ($control.Current.IsOffscreen) { throw 'An app-bar control is offscreen' }
  if ([Math]::Abs($control.Current.BoundingRectangle.Top - $minimize.Current.BoundingRectangle.Top) -gt 20) {
    throw 'Application controls are not merged into the title bar'
  }
}
New-Item -ItemType Directory -Force -Path $Shots | Out-Null
foreach ($choice in @(
  @{ Name = 'dark'; Keys = '{HOME}{DOWN}{DOWN}{ENTER}'; R = 2; G = 12; B = 27 },
  @{ Name = 'light'; Keys = '{HOME}{DOWN}{ENTER}'; R = 245; G = 247; B = 249 }
)) {
  $theme.SetFocus()
  $theme.GetCurrentPattern([System.Windows.Automation.ExpandCollapsePattern]::Pattern).Expand()
  [System.Windows.Forms.SendKeys]::SendWait($choice['Keys'])
  $matched = $false
  for ($i = 0; $i -lt 50; $i++) {
    Start-Sleep -Milliseconds 100
    $rect = $window.Current.BoundingRectangle
    $bitmap = [System.Drawing.Bitmap]::new([int]$rect.Width, [int]$rect.Height)
    $graphics = [System.Drawing.Graphics]::FromImage($bitmap)
    try {
      $graphics.CopyFromScreen([int]$rect.Left, [int]$rect.Top, 0, 0, $bitmap.Size)
      $pixel = $bitmap.GetPixel([int]($bitmap.Width / 2), $bitmap.Height - 60)
      if ($pixel.R -eq $choice.R -and $pixel.G -eq $choice.G -and $pixel.B -eq $choice.B) {
        $bitmap.Save((Join-Path $Shots "scanner-$Version-$($choice.Name).png"), [System.Drawing.Imaging.ImageFormat]::Png)
        $matched = $true
        break
      }
    } finally { $graphics.Dispose(); $bitmap.Dispose() }
  }
  if (-not $matched) { throw "Desktop did not visibly switch to $($choice.Name): observed $pixel" }
}
$theme.SetFocus()
$theme.GetCurrentPattern([System.Windows.Automation.ExpandCollapsePattern]::Pattern).Expand()
[System.Windows.Forms.SendKeys]::SendWait('{HOME}{ENTER}')
Write-Host "Desktop ${Version}: unified controls and actual dark/light pixels verified."
