[CmdletBinding()]
param([Parameter(Mandatory)][string]$CLI)
$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [Text.UTF8Encoding]::new($false)
$OutputEncoding = [Console]::OutputEncoding
Import-Module BitsTransfer -ErrorAction Stop
$CLI = (Resolve-Path -LiteralPath $CLI).Path
$root = Join-Path ([IO.Path]::GetTempPath()) ('ztg-bits-designer-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $root | Out-Null
try {
    $catalog = (& $CLI bits flags --json | Out-String) | ConvertFrom-Json
    if ($LASTEXITCODE -ne 0) { throw 'BITS catalog failed' }
    $cmdlet = Get-Command BitsTransfer\Start-BitsTransfer
    foreach ($entry in $catalog) {
        if (-not $cmdlet.Parameters.ContainsKey($entry.name)) { throw "Catalog parameter missing from installed cmdlet: $($entry.name)" }
    }
    $job = @{
        schema = 1; engine = 'bits'
        transfers = @(@{ source = 'https://example.invalid/payload.bin'; destination = (Join-Path $root 'payload.bin') })
        options = @(@{ name = 'TransferType'; values = @('Download') }, @{ name = 'Priority'; values = @('Normal') })
    }
    $jobFile = Join-Path $root 'job.json'
    function Get-Design($options) {
        $job.options = $options
        [IO.File]::WriteAllText($jobFile, ($job | ConvertTo-Json -Depth 8), [Text.UTF8Encoding]::new($false))
        $text = & $CLI bits command --json $jobFile | Out-String
        if ($LASTEXITCODE -ne 0) { throw "BITS command generation failed: $text" }
        $view = $text | ConvertFrom-Json
        $tokens, $parseErrors = $null, $null
        $ast = [Management.Automation.Language.Parser]::ParseInput($view.command, [ref]$tokens, [ref]$parseErrors)
        if ($parseErrors.Count -ne 0) { throw 'Generated command is not valid PowerShell' }
        $forbidden = $ast.FindAll({
            param($node)
            $node -is [Management.Automation.Language.ExpandableStringExpressionAst] -or
            $node -is [Management.Automation.Language.SubExpressionAst] -or
            $node -is [Management.Automation.Language.ScriptBlockExpressionAst]
        }, $true)
        if ($forbidden.Count -ne 0) { throw 'Generated command contains executable expansion' }
        $commands = $ast.FindAll({ param($node) $node -is [Management.Automation.Language.CommandAst] }, $true)
        foreach ($command in $commands) {
            if ($command.GetCommandName() -notin @('BitsTransfer\Start-BitsTransfer', 'Get-Credential')) { throw 'Unexpected generated command' }
        }
        return $view
    }
    $baseOptions = $job.options
    $marker = Join-Path $root 'injection-marker'
    $description = "O'Brien ‘curly’ " + '$(New-Item ' + $marker + '); $env:TEMP & 日本語-😀'
    $view = Get-Design ($baseOptions + @(@{ name = 'Description'; values = @($description) }, @{ name = 'RetryInterval'; values = @('60') }, @{ name = 'RetryTimeout'; values = @('3600') }, @{ name = 'TransferPolicy'; values = @('Standard') }))
    $literalTokens, $literalErrors = $null, $null
    $descriptionAst = [Management.Automation.Language.Parser]::ParseInput($view.command, [ref]$literalTokens, [ref]$literalErrors)
    $descriptionValue = $descriptionAst.FindAll({ param($node) $node -is [Management.Automation.Language.StringConstantExpressionAst] -and $node.Value -eq $description }, $true)
    if ($descriptionValue.Count -ne 1) { throw 'PowerShell literal changed straight or typographic quotes' }
    # WhatIf binds the generated arguments to the real inbox cmdlet without starting a transfer.
    & ([ScriptBlock]::Create($view.command + ' -WhatIf'))
    if (Test-Path -LiteralPath $marker) { throw 'Literal command text executed' }
    if (Test-Path -LiteralPath (Join-Path $root 'payload.bin')) { throw 'WhatIf wrote a destination file' }
    $view = Get-Design ($baseOptions + @(@{ name = 'Asynchronous' }, @{ name = 'Suspended' }, @{ name = 'ProxyUsage'; values = @('Override') }, @{ name = 'ProxyList'; values = @('http://proxy.example.invalid:8080') }, @{ name = 'ProxyBypass'; values = @('*.example.invalid', '<local>') }, @{ name = 'Authentication'; values = @('Negotiate') }, @{ name = 'ProxyAuthentication'; values = @('Negotiate') }, @{ name = 'MaxDownloadTime'; values = @('3600') }))
    & ([ScriptBlock]::Create($view.command + ' -WhatIf'))
    $prompt = Get-Design ($baseOptions + @(@{ name = 'Credential' }, @{ name = 'ProxyCredential' }))
    if ($prompt.command -notmatch '\-Credential \(Get-Credential\)' -or $prompt.command -notmatch '\-ProxyCredential \(Get-Credential\)') { throw 'Credentials were not expressed as runtime prompts' }
    $script = Join-Path $root 'Unicode.ps1'
    & $CLI bits export --output $script $jobFile
    if ($LASTEXITCODE -ne 0) { throw 'BITS script export failed' }
    $bytes = [IO.File]::ReadAllBytes($script)
    if ($bytes[0] -ne 0xef -or $bytes[1] -ne 0xbb -or $bytes[2] -ne 0xbf) { throw 'Windows PowerShell script lacks UTF-8 BOM' }
    $ErrorActionPreference = 'Continue'
    & $CLI bits export --output $script $jobFile 2>$null
    $exportCode = $LASTEXITCODE
    $ErrorActionPreference = 'Stop'
    if ($exportCode -eq 0) { throw 'Export overwrote an existing script' }
    Write-Output 'COPY_BITS_POWERSHELL {"passed":true,"installed_catalog":true,"native_binding_whatif":true,"literal_injection_safe":true,"credential_prompts":true,"script_bom":true,"no_transfer":true}'
} finally {
    Remove-Item -LiteralPath $root -Recurse -Force
}
exit 0
