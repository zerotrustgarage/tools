[CmdletBinding()]
param([Parameter(Mandatory)][string]$CLI)
$ErrorActionPreference = 'Stop'
$executable = (Resolve-Path -LiteralPath $CLI).Path
$root = Join-Path ([IO.Path]::GetTempPath()) ('ztg-copy-cli-' + [guid]::NewGuid().ToString('N'))
$oldAppData = $env:APPDATA
try {
    $source = Join-Path $root 'source space'
    $destination = Join-Path $root 'destination'
    $nativeDestination = Join-Path $root 'native destination'
    $records = Join-Path $root 'records'
    $env:APPDATA = Join-Path $root 'roaming'
    foreach ($directory in @($source, $destination, $nativeDestination)) {
        New-Item -ItemType Directory -Path $directory -Force | Out-Null
    }
    $filename = 'café-日本語-😀.txt'
    $payload = 'CLI fixture 日本語-😀'
    [IO.File]::WriteAllText((Join-Path $source $filename), $payload, [Text.UTF8Encoding]::new($false))
    $job = @{schema=1; source=$source; destination=$destination; options=@('/E','/MT:8','/R:0','/W:0','/XJ')}
    $jobFile = Join-Path $root 'job.json'
    $job | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath $jobFile -Encoding utf8NoBOM
    & $executable plan --report $records $jobFile
    if ($LASTEXITCODE -ne 0) { throw 'CLI plan failed' }
    if (Test-Path -LiteralPath (Join-Path $destination $filename)) { throw 'Plan copied data' }
    & $executable run --report $records $jobFile
    if ($LASTEXITCODE -ne 0) { throw 'CLI copy or convergence failed' }
    if ([IO.File]::ReadAllText((Join-Path $destination $filename)) -cne $payload) { throw 'CLI copy changed content' }
    $job.destination = $nativeDestination
    $job | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath $jobFile -Encoding utf8NoBOM
    & $executable run --native-exit --report $records $jobFile
    if ($LASTEXITCODE -ne 1) { throw 'Native exit did not preserve the copied-files bit' }
    & $executable template save --from $jobFile local-copy
    if ($LASTEXITCODE -ne 0) { throw 'CLI template save failed' }
    & $executable template run --report $records local-copy
    if ($LASTEXITCODE -ne 0) { throw 'CLI template run failed' }
    $templateDir = Join-Path $env:APPDATA 'ZTG\Copy\templates'
    $templateFile = Join-Path $templateDir 'local-copy.json'
    $sid = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
    $privatePaths = @($templateDir, $templateFile) + @(Get-ChildItem -LiteralPath $records -Recurse -File | ForEach-Object FullName)
    foreach ($name in $privatePaths) {
        $acl = Get-Acl -LiteralPath $name
        if (@($acl.Access).Count -eq 0) { throw 'Private path has no explicit owner access' }
        foreach ($rule in $acl.Access) {
            $ruleSid = $rule.IdentityReference.Translate([Security.Principal.SecurityIdentifier]).Value
            if ($ruleSid -ne $sid) { throw 'Private path grants another identity access' }
        }
    }
    foreach ($name in @($templateDir, $templateFile, $records)) {
        if (-not (Get-Acl -LiteralPath $name).AreAccessRulesProtected) { throw 'Private DACL inherits parent access' }
    }
    $runFiles = @(Get-ChildItem -LiteralPath $records -Recurse -Filter run.json -File)
    if ($runFiles.Count -ne 4) { throw 'Missing run records' }
    foreach ($file in $runFiles) {
        $record = Get-Content -LiteralPath $file.FullName -Raw | ConvertFrom-Json
        if ($record.normalized_exit -ne 0) { throw 'Saved run outcome differs from CLI outcome' }
        if ($record.mode -eq 'run' -and $record.phases[-1].name -ne 'convergence') { throw 'Copy skipped convergence' }
    }
    Write-Host ('COPY_CLI_INTEGRATION ' + (@{passed=$true; runs=$runFiles.Count; owner_only_dacls=$true; native_exit_preserved=$true; preview_unchanged=$true; unicode_content_equal=$true} | ConvertTo-Json -Compress))
} finally {
    $env:APPDATA = $oldAppData
    if (Test-Path -LiteralPath $root) { Remove-Item -LiteralPath $root -Recurse -Force }
}
