param([string]$OutputPath)

$OutputEncoding = [Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $scriptDir

$go1264 = Join-Path $env:USERPROFILE "go\bin\go1.26.4.exe"
$go = if (Test-Path -LiteralPath $go1264) { $go1264 } else { "go" }
$exeName = "Minecraft$([char]0x5730)$([char]0x56FE)$([char]0x5907)$([char]0x4EFD).exe"
if (-not $OutputPath) { $OutputPath = Join-Path $scriptDir $exeName }
$OutputPath = [System.IO.Path]::GetFullPath($OutputPath)
$candidate = Join-Path (Split-Path -Parent $OutputPath) ('minecraft-build-' + [guid]::NewGuid().ToString('N') + '.exe')
$resource = $null

try {
    $windres = (Get-Command windres.exe -CommandType Application -ErrorAction Stop).Source
    $architecture = (& $go env GOARCH).Trim()
    if ($LASTEXITCODE -ne 0) { throw '读取 Go 架构失败。' }
    $target = switch ($architecture) {
        'amd64' { 'pe-x86-64' }
        '386' { 'pe-i386' }
        default { throw "当前原生资源构建不支持架构：$architecture" }
    }
    $resource = Join-Path $scriptDir ('minecraft_resources_' + [guid]::NewGuid().ToString('N') + '_windows_' + $architecture + '.syso')
    & $windres --input=app.rc --output=$resource --output-format=coff --target=$target
    if ($LASTEXITCODE -ne 0) { throw "原生资源编译失败，退出码 $LASTEXITCODE" }
    & $go build -trimpath -ldflags="-s -w -H windowsgui" -o $candidate .
    if ($LASTEXITCODE -ne 0) { throw "Go 编译失败，退出码 $LASTEXITCODE" }
    $buildInfo = @(& $go version -m $candidate)
    if ($LASTEXITCODE -ne 0) { throw '生成的 EXE 构建信息校验失败。' }
    Move-Item -LiteralPath $candidate -Destination $OutputPath -Force
    Write-Host "已生成原生桌面程序：$OutputPath"
} finally {
    if (Test-Path -LiteralPath $candidate) { Remove-Item -LiteralPath $candidate -Force }
    if ($resource -and (Test-Path -LiteralPath $resource)) { Remove-Item -LiteralPath $resource -Force }
}
