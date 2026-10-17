#requires -Version 7.0
param([string] $OutputDirectory = (Join-Path (Split-Path -Parent $PSScriptRoot) 'dist'))

$OutputEncoding = [Console]::OutputEncoding = [Text.UTF8Encoding]::new($false)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$sourceRoot = Split-Path -Parent $PSScriptRoot
Set-Location -LiteralPath $sourceRoot
$utf8 = [Text.UTF8Encoding]::new($false)
$version = (Get-Content -LiteralPath (Join-Path $sourceRoot 'VERSION') -Encoding UTF8 -Raw).Trim()
if ($version -notmatch '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$') { throw 'VERSION 格式无效。' }
$go1264 = Join-Path $env:USERPROFILE 'go\bin\go1.26.4.exe'
$go = if (Test-Path -LiteralPath $go1264) { $go1264 } else { 'go' }
$goOS = (& $go env GOOS).Trim()
$goArch = (& $go env GOARCH).Trim()
if ($LASTEXITCODE -ne 0 -or $goOS -ne 'windows' -or $goArch -ne 'amd64') {
    throw '公开便携包目前只支持 Windows amd64，请清除不符的 GOOS/GOARCH 设置。'
}
$headBefore = (& git rev-parse HEAD).Trim()
if ($LASTEXITCODE -ne 0) { throw '无法读取源码 HEAD。' }

# Hash the actual build inputs, tests, packaging tools and public documentation.
# This identifies an authorized working-tree build even before its final commit.
function Get-SourceRecords {
    $names = @(Get-ChildItem -LiteralPath $sourceRoot -File | Where-Object {
        $_.Extension -in @('.ps1', '.md') -or $_.Name -in @('go.mod', 'go.sum', 'VERSION', 'LICENSE', '.gitignore')
    } | ForEach-Object Name)
    foreach ($folder in @('cmd', 'assets', 'resources', 'scripts', 'tests', 'packaging', 'docs', 'third_party')) {
        $names += @(Get-ChildItem -LiteralPath (Join-Path $sourceRoot $folder) -File -Recurse | ForEach-Object {
            [IO.Path]::GetRelativePath($sourceRoot, $_.FullName).Replace('\', '/')
        })
    }
    return @($names | Sort-Object -Unique | ForEach-Object {
        $path = Join-Path $sourceRoot $_
        $item = Get-Item -LiteralPath $path
        if ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw "源码不是普通文件：$_" }
        [ordered]@{ path = $_; bytes = $item.Length; sha256 = (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash.ToLowerInvariant() }
    })
}
function Get-RecordsDigest($Records) {
    $raw = $utf8.GetBytes(($Records | ConvertTo-Json -Depth 5 -Compress))
    return [Convert]::ToHexString([Security.Cryptography.SHA256]::HashData($raw)).ToLowerInvariant()
}
$sourceRecords = Get-SourceRecords
$sourceDigest = Get-RecordsDigest $sourceRecords
& $go test ./...
if ($LASTEXITCODE -ne 0) { throw 'Go 测试失败，未打包。' }
& $go vet ./...
if ($LASTEXITCODE -ne 0) { throw 'Go vet 失败，未打包。' }

$output = [IO.Path]::GetFullPath($OutputDirectory)
New-Item -ItemType Directory -Path $output -Force | Out-Null
$baseName = "MinecraftWorldBackup-$version-windows-amd64"
$exeAsset = Join-Path $output ($baseName + '.exe')
$zipAsset = Join-Path $output ($baseName + '.zip')
$checksums = Join-Path $output 'SHA256SUMS.txt'
$infoAsset = Join-Path $output 'RELEASE-INFO.json'
foreach ($destination in @($exeAsset, $zipAsset, $checksums, $infoAsset)) {
    if (Test-Path -LiteralPath $destination) { throw "发行文件已存在，换一个空输出目录：$destination" }
}
$staging = Join-Path $output ('package-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $staging | Out-Null
$portableExe = Join-Path $staging 'Minecraft地图备份.exe'
& (Join-Path $sourceRoot 'build.ps1') -OutputPath $portableExe
Copy-Item -LiteralPath $portableExe -Destination $exeAsset
$documents = @('README.md', 'docs/usage.md', 'docs/usage.en.md', 'docs/development.md', 'CHANGELOG.md', 'LICENSE', 'THIRD_PARTY_NOTICES.md', 'third_party/Go-LICENSE.txt', 'VERSION', 'packaging/windows/install-shortcuts.ps1')
foreach ($name in $documents) {
    $destinationName = if ($name -eq 'packaging/windows/install-shortcuts.ps1') { 'install-shortcuts.ps1' } else { $name }
    $destination = Join-Path $staging $destinationName
    New-Item -ItemType Directory -Path (Split-Path -Parent $destination) -Force | Out-Null
    Copy-Item -LiteralPath (Join-Path $sourceRoot $name) -Destination $destination
}
$documents = @($documents | ForEach-Object { if ($_ -eq 'packaging/windows/install-shortcuts.ps1') { 'install-shortcuts.ps1' } else { $_ } })
$exeHash = (Get-FileHash -LiteralPath $exeAsset -Algorithm SHA256).Hash.ToLowerInvariant()
$info = [ordered]@{
    version = $version
    target = 'windows/amd64'
    built_at_utc = [DateTime]::UtcNow.ToString('o')
    go_version = (& $go version).Trim()
    source_head = $headBefore
    source_worktree_modified = (@(& git status --porcelain=v1).Count -gt 0)
    source_sha256 = $sourceDigest
    source_files = $sourceRecords
    executable_sha256 = $exeHash
    code_signed = $false
    checks = @('go test ./...', 'go vet ./...', 'PE version resource readback', 'ZIP file hash readback')
}
[IO.File]::WriteAllText((Join-Path $staging 'RELEASE-INFO.json'), ($info | ConvertTo-Json -Depth 8) + "`n", $utf8)
Add-Type -AssemblyName System.IO.Compression
[IO.Compression.ZipFile]::CreateFromDirectory($staging, $zipAsset, [IO.Compression.CompressionLevel]::Optimal, $false)
$zip = [IO.Compression.ZipFile]::OpenRead($zipAsset)
try {
    $expected = @('Minecraft地图备份.exe', 'RELEASE-INFO.json') + $documents
    if (@($zip.Entries | Where-Object { $_.Name }).Count -ne $expected.Count) { throw 'ZIP 文件数不符。' }
    foreach ($name in $expected) {
        $entry = $zip.GetEntry($name)
        if ($null -eq $entry) { throw "ZIP 缺少文件：$name" }
        $stream = $entry.Open()
        try { $actual = [Convert]::ToHexString([Security.Cryptography.SHA256]::HashData($stream)).ToLowerInvariant() }
        finally { $stream.Dispose() }
        $wanted = (Get-FileHash -LiteralPath (Join-Path $staging $name) -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($actual -ne $wanted) { throw "ZIP 文件校验失败：$name" }
    }
} finally { $zip.Dispose() }
if ((& git rev-parse HEAD).Trim() -ne $headBefore -or (Get-RecordsDigest (Get-SourceRecords)) -ne $sourceDigest) {
    throw '源码在构建过程中变化，未完成发布校验；请使用新空目录重建。'
}
Copy-Item -LiteralPath (Join-Path $staging 'RELEASE-INFO.json') -Destination $infoAsset
$assets = @($exeAsset, $zipAsset, $infoAsset)
$lines = @($assets | ForEach-Object { (Get-FileHash -LiteralPath $_ -Algorithm SHA256).Hash.ToLowerInvariant() + '  ' + [IO.Path]::GetFileName($_) })
[IO.File]::WriteAllText($checksums, ($lines -join "`n") + "`n", $utf8)
Write-Host "RELEASE_PACKAGE_OK version=$version directory=$output"
Write-Host 'UI checks must still be run against this release EXE on an interactive Windows desktop.'
$assets + $checksums | ForEach-Object { Get-Item -LiteralPath $_ | Select-Object Name, Length }
