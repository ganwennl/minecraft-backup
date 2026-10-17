param([switch]$UI)
$ErrorActionPreference = 'Stop'
$OutputEncoding = [Console]::OutputEncoding = [Text.UTF8Encoding]::new($false)
$repo = Split-Path -Parent $PSScriptRoot
Push-Location -LiteralPath $repo
try {
    go test ./...
    if ($LASTEXITCODE -ne 0) { throw 'Go tests failed.' }
    go vet ./...
    if ($LASTEXITCODE -ne 0) { throw 'Go vet failed.' }
    if ($UI) {
        & (Join-Path $repo 'tests/ui/native_ui_test.ps1')
        & (Join-Path $repo 'tests/ui/automation_ui_test.ps1')
    }
} finally { Pop-Location }
