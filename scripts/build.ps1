param([string]$OutputPath)

$OutputEncoding = [Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$scriptDir = Split-Path -Parent $PSScriptRoot
$packageDir = Join-Path $scriptDir 'cmd/minecraft-backup'
Set-Location $scriptDir

$go1264 = Join-Path $env:USERPROFILE "go\bin\go1.26.4.exe"
$go = if (Test-Path -LiteralPath $go1264) { $go1264 } else { "go" }
$exeName = "Minecraft$([char]0x5730)$([char]0x56FE)$([char]0x5907)$([char]0x4EFD).exe"
if (-not $OutputPath) { $OutputPath = Join-Path $scriptDir ('dist/' + $exeName) }
$OutputPath = [System.IO.Path]::GetFullPath($OutputPath)
$version = (Get-Content -LiteralPath (Join-Path $scriptDir 'VERSION') -Encoding UTF8 -Raw).Trim()
if ($version -notmatch '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$') {
    throw 'VERSION 必须为三段数字，例如 2.0.0。'
}
$versionParts = @($version.Split('.') | ForEach-Object { [int]$_ })
if (@($versionParts | Where-Object { $_ -gt 65535 }).Count) { throw 'VERSION 每段必须不大于 65535。' }
$nativeVersion = ($versionParts -join ',') + ',0'
New-Item -ItemType Directory -Path (Split-Path -Parent $OutputPath) -Force | Out-Null
$candidate = Join-Path (Split-Path -Parent $OutputPath) ('minecraft-build-' + [guid]::NewGuid().ToString('N') + '.exe')
$resource = $null
$resourceSource = $null

try {
    $windres = (Get-Command windres.exe -CommandType Application -ErrorAction Stop).Source
    $architecture = (& $go env GOARCH).Trim()
    if ($LASTEXITCODE -ne 0) { throw '读取 Go 架构失败。' }
    $target = switch ($architecture) {
        'amd64' { 'pe-x86-64' }
        '386' { 'pe-i386' }
        default { throw "当前原生资源构建不支持架构：$architecture" }
    }
    $resource = Join-Path $packageDir ('minecraft_resources_' + [guid]::NewGuid().ToString('N') + '_windows_' + $architecture + '.syso')
    $resourceSource = Join-Path $packageDir ('minecraft_resources_' + [guid]::NewGuid().ToString('N') + '.rc')
    $versionResource = @"
#include "resources/windows/app.rc"
1 VERSIONINFO
FILEVERSION $nativeVersion
PRODUCTVERSION $nativeVersion
FILEFLAGSMASK 0x3fL
FILEFLAGS 0x0L
FILEOS 0x40004L
FILETYPE 0x1L
FILESUBTYPE 0x0L
BEGIN
    BLOCK "StringFileInfo"
    BEGIN
        BLOCK "040904b0"
        BEGIN
            VALUE "CompanyName", "ganwennl\0"
            VALUE "FileDescription", "Minecraft World Backup\0"
            VALUE "FileVersion", "$version\0"
            VALUE "InternalName", "MinecraftWorldBackup\0"
            VALUE "LegalCopyright", "Copyright (c) 2026 ganwennl.\0"
            VALUE "OriginalFilename", "MinecraftWorldBackup.exe\0"
            VALUE "ProductName", "Minecraft World Backup\0"
            VALUE "ProductVersion", "$version\0"
        END
    END
    BLOCK "VarFileInfo"
    BEGIN
        VALUE "Translation", 0x0409, 1200
    END
END
"@
    [IO.File]::WriteAllText($resourceSource, $versionResource, [Text.UTF8Encoding]::new($false))
    & $windres -I $scriptDir --input=$resourceSource --output=$resource --output-format=coff --target=$target
    if ($LASTEXITCODE -ne 0) { throw "原生资源编译失败，退出码 $LASTEXITCODE" }
    & $go build -trimpath -ldflags="-s -w -H windowsgui" -o $candidate ./cmd/minecraft-backup
    if ($LASTEXITCODE -ne 0) { throw "Go 编译失败，退出码 $LASTEXITCODE" }
    $buildInfo = @(& $go version -m $candidate)
    if ($LASTEXITCODE -ne 0) { throw '生成的 EXE 构建信息校验失败。' }
    $metadata = [Diagnostics.FileVersionInfo]::GetVersionInfo($candidate)
    if ($metadata.ProductVersion -ne $version -or $metadata.FileVersion -ne $version) {
        throw 'EXE 版本资源校验失败。'
    }
    Move-Item -LiteralPath $candidate -Destination $OutputPath -Force
    Write-Host "已生成原生桌面程序：$OutputPath"
} finally {
    if (Test-Path -LiteralPath $candidate) { Remove-Item -LiteralPath $candidate -Force }
    if ($resource -and (Test-Path -LiteralPath $resource)) { Remove-Item -LiteralPath $resource -Force }
    if ($resourceSource -and (Test-Path -LiteralPath $resourceSource)) { Remove-Item -LiteralPath $resourceSource -Force }
}
