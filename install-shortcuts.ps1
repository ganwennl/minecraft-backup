param(
    [string] $ExePath = (Join-Path $PSScriptRoot 'Minecraft地图备份.exe')
)

$OutputEncoding = [Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$resolvedExe = [IO.Path]::GetFullPath($ExePath)
if (-not (Test-Path -LiteralPath $resolvedExe -PathType Leaf)) {
    throw "程序文件不存在：$resolvedExe"
}
$workDir = Split-Path -Parent $resolvedExe
$desktop = [Environment]::GetFolderPath('DesktopDirectory')
$programs = [Environment]::GetFolderPath('Programs')
if ([string]::IsNullOrWhiteSpace($desktop) -or [string]::IsNullOrWhiteSpace($programs)) {
    throw '无法定位当前用户的桌面或开始菜单目录。'
}
$shortcutPaths = @(
    (Join-Path $desktop 'Minecraft地图备份.lnk'),
    (Join-Path (Join-Path $programs 'Minecraft 地图备份') 'Minecraft地图备份.lnk')
)
$shell = New-Object -ComObject WScript.Shell

# Validate both destinations before creating or updating either shortcut.
foreach ($shortcutPath in $shortcutPaths) {
    if (Test-Path -LiteralPath $shortcutPath) {
        $item = Get-Item -LiteralPath $shortcutPath
        if ($item.PSIsContainer -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint)) {
            throw "目标路径不是普通快捷方式，未覆盖：$shortcutPath"
        }
        $existing = $shell.CreateShortcut($shortcutPath)
        if ([string]::IsNullOrWhiteSpace($existing.TargetPath) -or
            -not [StringComparer]::OrdinalIgnoreCase.Equals([IO.Path]::GetFullPath($existing.TargetPath), $resolvedExe)) {
            throw "同名快捷方式属于其他程序，未覆盖：$shortcutPath"
        }
    }
}

if (-not ('MinecraftShortcutNotify' -as [type])) {
    Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
public static class MinecraftShortcutNotify {
    [DllImport("shell32.dll", CharSet = CharSet.Unicode)]
    public static extern void SHChangeNotify(uint eventId, uint flags, string item, IntPtr other);
}
'@
}

foreach ($shortcutPath in $shortcutPaths) {
    $parent = Split-Path -Parent $shortcutPath
    if (-not (Test-Path -LiteralPath $parent -PathType Container)) {
        New-Item -ItemType Directory -Path $parent -Force | Out-Null
    }
    $shortcut = $shell.CreateShortcut($shortcutPath)
    $shortcut.TargetPath = $resolvedExe
    $shortcut.Arguments = ''
    $shortcut.WorkingDirectory = $workDir
    $shortcut.Description = '备份和恢复 Minecraft 基岩版与 Java 版世界，支持自动备份。'
    $shortcut.IconLocation = $resolvedExe + ',0'
    $shortcut.Save()
    # Refresh only this shortcut; do not reset Explorer or the shared icon cache.
    [MinecraftShortcutNotify]::SHChangeNotify(0x2000, 0x0005, $shortcutPath, [IntPtr]::Zero)
}

foreach ($shortcutPath in $shortcutPaths) {
    $saved = $shell.CreateShortcut($shortcutPath)
    if (-not [StringComparer]::OrdinalIgnoreCase.Equals($saved.TargetPath, $resolvedExe) -or
        -not [StringComparer]::OrdinalIgnoreCase.Equals($saved.IconLocation, ($resolvedExe + ',0')) -or
        -not [StringComparer]::OrdinalIgnoreCase.Equals($saved.WorkingDirectory, $workDir) -or
        $saved.Arguments -ne '') {
        throw "快捷方式读回验证失败：$shortcutPath"
    }
    [pscustomobject]@{
        Shortcut = $shortcutPath
        TargetPath = $saved.TargetPath
        IconLocation = $saved.IconLocation
        WorkingDirectory = $saved.WorkingDirectory
        Arguments = $saved.Arguments
    }
}
