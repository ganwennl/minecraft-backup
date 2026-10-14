param([string] $ExePath = (Join-Path $PSScriptRoot 'Minecraft地图备份.exe'))

$OutputEncoding = [Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

# Every tested window belongs to a process launched here with isolated world paths.
# Fixtures are deliberately retained under ignored build/ for inspection.
if (-not ('MinecraftNativeSmoke' -as [type])) {
    Add-Type -TypeDefinition @'
using System;
using System.Collections.Generic;
using System.Runtime.InteropServices;
using System.Text;
public static class MinecraftNativeSmoke {
    public delegate bool EnumProc(IntPtr h, IntPtr p);
    [StructLayout(LayoutKind.Sequential)] public struct Rect { public int Left, Top, Right, Bottom; }
    public sealed class Window {
        public IntPtr Handle;
        public int Id;
        public string ClassName;
        public string Text;
        public Rect Bounds;
    }
    [DllImport("user32.dll")] static extern bool EnumWindows(EnumProc p, IntPtr l);
    [DllImport("user32.dll")] static extern bool EnumChildWindows(IntPtr h, EnumProc p, IntPtr l);
    [DllImport("user32.dll")] static extern uint GetWindowThreadProcessId(IntPtr h, out uint p);
    [DllImport("user32.dll", CharSet=CharSet.Unicode)] static extern int GetClassName(IntPtr h, StringBuilder b, int n);
    [DllImport("user32.dll", CharSet=CharSet.Unicode)] static extern IntPtr SendMessage(IntPtr h, uint m, IntPtr w, StringBuilder b);
    [DllImport("user32.dll", CharSet=CharSet.Unicode, EntryPoint="SendMessageW")] static extern IntPtr SendText(IntPtr h, uint m, IntPtr w, string t);
    public static bool SetText(IntPtr h, string t) { return SendText(h, 0x000C, IntPtr.Zero, t) != IntPtr.Zero; }
    [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr h, uint m, IntPtr w, IntPtr l);
    [DllImport("user32.dll")] public static extern bool IsWindow(IntPtr h);
    [DllImport("user32.dll")] public static extern bool IsWindowEnabled(IntPtr h);
    [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr h);
    [DllImport("user32.dll")] static extern int GetDlgCtrlID(IntPtr h);
    [DllImport("user32.dll")] public static extern IntPtr GetDlgItem(IntPtr h, int id);
    [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out Rect r);
    [DllImport("user32.dll")] public static extern bool GetClientRect(IntPtr h, out Rect r);
    [DllImport("user32.dll")] public static extern bool MoveWindow(IntPtr h, int x, int y, int w, int n, bool repaint);
    [DllImport("user32.dll")] public static extern bool PrintWindow(IntPtr h, IntPtr dc, uint flags);
    [DllImport("user32.dll")] public static extern bool SetProcessDPIAware();
    [DllImport("user32.dll")] public static extern uint GetDpiForWindow(IntPtr h);
    public static string Text(IntPtr h) {
        var b = new StringBuilder(131072);
        SendMessage(h, 0x000D, new IntPtr(b.Capacity), b);
        return b.ToString();
    }
    static Window Describe(IntPtr h) {
        var b = new StringBuilder(256); GetClassName(h, b, b.Capacity);
        Rect r; GetWindowRect(h, out r);
        return new Window {Handle=h, Id=GetDlgCtrlID(h), ClassName=b.ToString(), Text=Text(h), Bounds=r};
    }
    public static Window[] Windows(int pid) {
        var a = new List<Window>();
        EnumWindows((h,p) => { uint owner; GetWindowThreadProcessId(h,out owner); if(owner==(uint)pid) a.Add(Describe(h)); return true; },IntPtr.Zero);
        return a.ToArray();
    }
    public static Window[] Children(IntPtr parent) {
        var a = new List<Window>();
        EnumChildWindows(parent,(h,p)=>{a.Add(Describe(h));return true;},IntPtr.Zero);
        return a.ToArray();
    }
}
'@
}

[MinecraftNativeSmoke]::SetProcessDPIAware() | Out-Null

function Assert-True([bool] $Condition, [string] $Message) {
    if (-not $Condition) { throw $Message }
}
function Wait-Until([scriptblock] $Condition, [string] $Description, [int] $TimeoutSeconds = 30) {
    $until = [DateTime]::UtcNow.AddSeconds($TimeoutSeconds)
    do {
        if (& $Condition) { return }
        Start-Sleep -Milliseconds 50
    } while ([DateTime]::UtcNow -lt $until)
    throw "等待超时：$Description"
}
function Post-Command([IntPtr] $Window, [int] $Id) {
    Assert-True ([MinecraftNativeSmoke]::PostMessage($Window, 0x0111, [IntPtr] $Id, [IntPtr]::Zero)) "发送控件命令失败：$Id"
}
function Own-Dialogs([int] $ProcessId) {
    @([MinecraftNativeSmoke]::Windows($ProcessId) | Where-Object { $_.ClassName -eq '#32770' -and [MinecraftNativeSmoke]::IsWindowVisible($_.Handle) })
}
function Wait-Dialog([int] $ProcessId, [string] $Description) {
    Wait-Until { @(Own-Dialogs $ProcessId).Count -gt 0 } $Description
    return @(Own-Dialogs $ProcessId)[0]
}
function Get-DialogText([IntPtr] $Window) {
    return (@([MinecraftNativeSmoke]::Children($Window) | ForEach-Object { $_.Text }) -join "`n")
}
function Close-Dialog([IntPtr] $Window, [int] $ButtonId) {
    $button = [MinecraftNativeSmoke]::GetDlgItem($Window, $ButtonId)
    if ($button -eq [IntPtr]::Zero -and $ButtonId -eq 1) {
        $buttons = @([MinecraftNativeSmoke]::Children($Window) | Where-Object ClassName -eq 'Button')
        if ($buttons.Count -eq 1 -and $buttons[0].Text -match '^(&?OK|确定)$') { $button = $buttons[0].Handle }
    }
    if ($button -eq [IntPtr]::Zero) {
        $details = @([MinecraftNativeSmoke]::Children($Window) | ForEach-Object { "$($_.ClassName):$($_.Id):$($_.Text)" }) -join '; '
        throw "对话框未找到按钮 $ButtonId；$details"
    }
    Assert-True ([MinecraftNativeSmoke]::PostMessage($button, 0x00F5, [IntPtr]::Zero, [IntPtr]::Zero)) '点击对话框按钮失败。'
    Wait-Until { -not [MinecraftNativeSmoke]::IsWindow($Window) -or -not [MinecraftNativeSmoke]::IsWindowVisible($Window) } '关闭测试程序的对话框'
}
function Get-TreeHashes([string] $Root) {
    $result = @{}
    foreach ($file in Get-ChildItem -LiteralPath $Root -File -Recurse) {
        $result[$file.FullName.Substring($Root.Length)] = (Get-FileHash -LiteralPath $file.FullName -Algorithm SHA256).Hash
    }
    return $result
}
function Assert-TreeHashes([string] $Root, [hashtable] $Expected) {
    $actual = Get-TreeHashes $Root
    Assert-True ($actual.Count -eq $Expected.Count) "恢复文件数量不一致：$Root"
    foreach ($key in $Expected.Keys) {
        Assert-True ($actual.ContainsKey($key) -and $actual[$key] -eq $Expected[$key]) "恢复字节不一致：$Root$key"
    }
}

$repo = $PSScriptRoot
$exe = [IO.Path]::GetFullPath($ExePath)
Assert-True (Test-Path -LiteralPath $exe -PathType Leaf) '请先执行 build.ps1 生成程序。'
$testedProcessName = [IO.Path]::GetFileNameWithoutExtension($exe)
$running = @(Get-Process -ErrorAction SilentlyContinue | Where-Object { $_.ProcessName -in @('Minecraft地图备份', $testedProcessName) })
Assert-True ($running.Count -eq 0) '已有地图备份程序运行；请先关闭后运行隔离测试。'
$fixture = Join-Path $repo ('build\ui-smoke-' + [Guid]::NewGuid().ToString('N'))
$roaming = Join-Path $fixture 'Roaming'
$local = Join-Path $fixture 'Local'
$profile = Join-Path $fixture 'Profile'
$dest = Join-Path $fixture '中文备份'
$bedrockRoot = Join-Path $roaming 'Minecraft Bedrock\Users\Smoke\games\com.mojang\minecraftWorlds'
$javaRoot = Join-Path $roaming '.minecraft\saves'
$bedrock = Join-Path $bedrockRoot '基岩中文世界'
$java = Join-Path $javaRoot 'Java中文世界'
foreach ($dir in @($local, $profile, $dest, (Join-Path $bedrock 'db'), (Join-Path $java 'region'))) {
    New-Item -ItemType Directory -Path $dir -Force | Out-Null
}
[IO.File]::WriteAllBytes((Join-Path $bedrock 'level.dat'), [byte[]](0, 1, 2, 255, 128))
Set-Content -LiteralPath (Join-Path $bedrock 'levelname.txt') -Value '基岩中文世界' -Encoding UTF8 -NoNewline
[IO.File]::WriteAllBytes((Join-Path $bedrock 'db\CURRENT'), [byte[]](3, 9, 18, 27))
[IO.File]::WriteAllBytes((Join-Path $java 'level.dat'), [byte[]](31, 139, 8, 0, 255))
Set-Content -LiteralPath (Join-Path $java 'levelname.txt') -Value 'Java中文世界' -Encoding UTF8 -NoNewline
# Incompressible fixture keeps backup busy long enough to exercise WM_CLOSE protection.
$regionPath = Join-Path $java 'region\r.0.0.mca'
$regionStream = [IO.File]::Create($regionPath)
$rng = [Security.Cryptography.RandomNumberGenerator]::Create()
try {
    $chunk = [byte[]]::new(1MB)
    for ($i = 0; $i -lt 64; $i++) { $rng.GetBytes($chunk); $regionStream.Write($chunk, 0, $chunk.Length) }
} finally { $regionStream.Dispose(); $rng.Dispose() }
$bedrockHashes = Get-TreeHashes $bedrock
$javaHashes = Get-TreeHashes $java

function Start-IsolatedApp {
    $start = [Diagnostics.ProcessStartInfo]::new()
    $start.FileName = $exe
    $start.WorkingDirectory = $repo
    $start.UseShellExecute = $false
    $start.WindowStyle = [Diagnostics.ProcessWindowStyle]::Hidden
    $start.Environment['APPDATA'] = $roaming
    $start.Environment['LOCALAPPDATA'] = $local
    $start.Environment['USERPROFILE'] = $profile
    $start.Environment['HOME'] = $profile
    return [Diagnostics.Process]::Start($start)
}

$app = $null
$second = $null
try {
    $app = Start-IsolatedApp
    Wait-Until { @([MinecraftNativeSmoke]::Windows($app.Id) | Where-Object ClassName -eq 'MinecraftWorldBackupNativeWindow').Count -eq 1 } '原生主窗口'
    $window = @([MinecraftNativeSmoke]::Windows($app.Id) | Where-Object ClassName -eq 'MinecraftWorldBackupNativeWindow')[0].Handle
    $backupButton = [MinecraftNativeSmoke]::GetDlgItem($window, 103)
    Wait-Until { [MinecraftNativeSmoke]::IsWindowEnabled($backupButton) } '初始扫描完成'
    $children = @([MinecraftNativeSmoke]::Children($window))
    $readonlyEdits = @($children | Where-Object { $_.ClassName -eq 'Edit' -and $_.Id -eq 0 } | Sort-Object { $_.Bounds.Top })
    Assert-True ($readonlyEdits.Count -eq 2) '未找到原生世界列表及状态编辑框。'
    $worldsEdit = $readonlyEdits[0].Handle
    $statusEdit = $readonlyEdits[1].Handle
    $worldsText = [MinecraftNativeSmoke]::Text($worldsEdit)
    Assert-True ($worldsText.Contains('基岩中文世界') -and $worldsText.Contains('Java中文世界')) '原生列表没有显示两个中文世界。'
    Assert-True (-not (@($children | Where-Object { $_.ClassName -match 'Chrome|WebView|Internet Explorer' }).Count)) '界面存在浏览器嵌入控件。'
    $listeners = @(Get-NetTCPConnection -State Listen -OwningProcess $app.Id -ErrorAction SilentlyContinue)
    Assert-True ($listeners.Count -eq 0) '程序意外开启 TCP 监听端口。'
    $browserChildren = @(Get-CimInstance Win32_Process | Where-Object { $_.ParentProcessId -eq $app.Id -and $_.Name -match 'chrome|msedge|firefox|browser|webview' })
    Assert-True ($browserChildren.Count -eq 0) '程序意外启动浏览器进程。'
    Write-Host 'PASS native-controls / 中文扫描 / no-browser / no-listener'

    $second = Start-IsolatedApp
    Assert-True ($second.WaitForExit(5000)) '第二次启动未及时退出。'
    Assert-True ($second.ExitCode -eq 0) '第二次启动异常退出。'
    Assert-True (@([MinecraftNativeSmoke]::Windows($app.Id) | Where-Object ClassName -eq 'MinecraftWorldBackupNativeWindow').Count -eq 1) '双开后主窗口数量异常。'
    Write-Host 'PASS single-instance'

    $originalRect = [MinecraftNativeSmoke+Rect]::new()
    [MinecraftNativeSmoke]::GetWindowRect($window, [ref] $originalRect) | Out-Null
    $resizeWidth = [int](($originalRect.Right - $originalRect.Left) * 1.15)
    $resizeHeight = [int](($originalRect.Bottom - $originalRect.Top) * 1.05)
    [MinecraftNativeSmoke]::MoveWindow($window, $originalRect.Left, $originalRect.Top, $resizeWidth, $resizeHeight, $true) | Out-Null
    Start-Sleep -Milliseconds 150
    $clientRect = [MinecraftNativeSmoke+Rect]::new()
    [MinecraftNativeSmoke]::GetClientRect($window, [ref] $clientRect) | Out-Null
    $resized = @([MinecraftNativeSmoke]::Children($window))
    foreach ($control in $resized) {
        Assert-True ($control.Bounds.Right -gt $control.Bounds.Left -and $control.Bounds.Bottom -gt $control.Bounds.Top) '调整窗口后出现无尺寸控件。'
    }
    $resizedStatus = @($resized | Where-Object Handle -eq $statusEdit)[0]
    Assert-True ($resizedStatus.Bounds.Right - $resizedStatus.Bounds.Left -gt $readonlyEdits[1].Bounds.Right - $readonlyEdits[1].Bounds.Left) '调整窗口后状态框未扩展。'
    Write-Host 'PASS resize'
    Add-Type -AssemblyName System.Drawing
    $captureRect = [MinecraftNativeSmoke+Rect]::new()
    [MinecraftNativeSmoke]::GetWindowRect($window, [ref] $captureRect) | Out-Null
    $geometry = [ordered]@{
        Dpi = [MinecraftNativeSmoke]::GetDpiForWindow($window)
        Window = $captureRect
        Client = $clientRect
        Controls = @($resized | ForEach-Object { [ordered]@{ Id = $_.Id; Class = $_.ClassName; Bounds = $_.Bounds } })
    }
    $geometry | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath (Join-Path $fixture 'native-geometry.json') -Encoding UTF8
    $bitmap = [Drawing.Bitmap]::new($captureRect.Right - $captureRect.Left, $captureRect.Bottom - $captureRect.Top)
    $graphics = [Drawing.Graphics]::FromImage($bitmap)
    $dc = $graphics.GetHdc()
    try { Assert-True ([MinecraftNativeSmoke]::PrintWindow($window, $dc, 0)) '捕获测试程序原生窗口失败。' }
    finally { $graphics.ReleaseHdc($dc); $graphics.Dispose() }
    try { $bitmap.Save((Join-Path $fixture 'native-window.png'), [Drawing.Imaging.ImageFormat]::Png) }
    finally { $bitmap.Dispose() }

    foreach ($browseId in @(102, 105)) {
        Post-Command $window $browseId
        $dialog = Wait-Dialog $app.Id "打开原生选择器 $browseId"
        Close-Dialog $dialog.Handle 2
        Assert-True ([MinecraftNativeSmoke]::IsWindowEnabled($backupButton)) '取消原生选择器后界面未恢复。'
    }
    Write-Host 'PASS native-folder-dialog / native-file-dialog cancel'

    Assert-True ([MinecraftNativeSmoke]::SetText([MinecraftNativeSmoke]::GetDlgItem($window, 101), $dest)) '设置备份目录失败。'
    Assert-True ([MinecraftNativeSmoke]::Text([MinecraftNativeSmoke]::GetDlgItem($window, 101)) -eq $dest) ('备份目录设值未生效：' + [MinecraftNativeSmoke]::Text([MinecraftNativeSmoke]::GetDlgItem($window, 101)))
    Post-Command $window 103
    Wait-Until { -not [MinecraftNativeSmoke]::IsWindowEnabled($backupButton) } '备份进入忙碌状态'
    [MinecraftNativeSmoke]::PostMessage($window, 0x0010, [IntPtr]::Zero, [IntPtr]::Zero) | Out-Null
    $busyDialog = Wait-Dialog $app.Id '忙碌时关闭保护'
    Assert-True ((Get-DialogText $busyDialog.Handle).Contains('正在处理存档')) '忙碌关闭时未显示保护提示。'
    Close-Dialog $busyDialog.Handle 1
    Assert-True ([MinecraftNativeSmoke]::IsWindow($window)) '忙碌时主窗口被关闭。'
    Wait-Until { [MinecraftNativeSmoke]::IsWindowEnabled($backupButton) } '备份完成' 120
    Assert-True ([MinecraftNativeSmoke]::Text($statusEdit).Contains('备份完成：2 个世界')) ('备份未成功：' + [MinecraftNativeSmoke]::Text($statusEdit))
    $archives = @(Get-ChildItem -LiteralPath $dest -Filter '*.zip' -File)
    Assert-True ($archives.Count -eq 1 -and $archives[0].Length -gt 0) '未生成唯一的非空备份。'
    Write-Host 'PASS backup 2 worlds / busy-close protection'

    # Change originals after backup: restore must preserve these changes and create copies.
    $sentinel = [byte[]](77, 67, 45, 78, 79, 45, 79, 86, 69, 82, 87, 82, 73, 84, 69)
    [IO.File]::WriteAllBytes((Join-Path $bedrock 'level.dat'), $sentinel)
    [IO.File]::WriteAllBytes((Join-Path $java 'level.dat'), $sentinel)
    $originalBedrockAfterChange = Get-TreeHashes $bedrock
    $originalJavaAfterChange = Get-TreeHashes $java
    Assert-True ([MinecraftNativeSmoke]::SetText([MinecraftNativeSmoke]::GetDlgItem($window, 104), $archives[0].FullName)) '设置恢复文件失败。'
    Post-Command $window 106
    $confirm = Wait-Dialog $app.Id '恢复确认'
    Assert-True ((Get-DialogText $confirm.Handle).Contains('确定开始恢复')) '未找到预期恢复确认。'
    Close-Dialog $confirm.Handle 6
    Wait-Until { [MinecraftNativeSmoke]::IsWindowEnabled($backupButton) -and [MinecraftNativeSmoke]::Text($statusEdit).Contains('恢复完成') } '恢复完成' 120
    Assert-True ([MinecraftNativeSmoke]::Text($statusEdit).Contains('恢复完成：2 个世界')) ('恢复未成功：' + [MinecraftNativeSmoke]::Text($statusEdit))
    Assert-TreeHashes $bedrock $originalBedrockAfterChange
    Assert-TreeHashes $java $originalJavaAfterChange
    $restoredBedrock = @(Get-ChildItem -LiteralPath $bedrockRoot -Directory | Where-Object Name -like '基岩中文世界_restored_*')
    $restoredJava = @(Get-ChildItem -LiteralPath $javaRoot -Directory | Where-Object Name -like 'Java中文世界_restored_*')
    Assert-True ($restoredBedrock.Count -eq 1 -and $restoredJava.Count -eq 1) '恢复没有为同名世界创建独立目录。'
    Assert-TreeHashes $restoredBedrock[0].FullName $bedrockHashes
    Assert-TreeHashes $restoredJava[0].FullName $javaHashes
    Assert-True ([MinecraftNativeSmoke]::Text($worldsEdit).Contains('_restored_')) '恢复后世界列表没有刷新。'
    Post-Command $window 107
    Wait-Until { [MinecraftNativeSmoke]::IsWindowEnabled($backupButton) -and [MinecraftNativeSmoke]::Text($statusEdit).Contains('扫描完成') } '手动刷新完成'
    Write-Host 'PASS restore SHA256 / 同名不覆盖 / refresh'

    Post-Command $window 109
    Assert-True ($app.WaitForExit(5000)) '退出按钮未关闭程序。'
    Assert-True ($app.ExitCode -eq 0) '程序退出码异常。'
    Write-Host "NATIVE_UI_SMOKE_OK fixtures=$fixture"
} finally {
    # Cleanup is limited to exact process objects created by this script.
    foreach ($owned in @($second, $app)) {
        if ($null -ne $owned) {
            if (-not $owned.HasExited) { $owned.Kill(); $owned.WaitForExit() }
            $owned.Dispose()
        }
    }
}
