param(
    [switch] $TestHost,
    [string] $ResumeFixture,
    [string] $ExePath = (Join-Path (Split-Path -Parent (Split-Path -Parent $PSScriptRoot)) 'dist/Minecraft地图备份.exe')
)

$OutputEncoding = [Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$repo = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)

# Reuse only the native API declarations; never execute the older smoke scenario.
if (-not ('MinecraftNativeSmoke' -as [type])) {
    $helperSource = Get-Content -LiteralPath (Join-Path $PSScriptRoot 'native_ui_test.ps1') -Encoding UTF8 -Raw
    $helperMatch = [regex]::Match($helperSource, "(?s)Add-Type -TypeDefinition @'\r?\n(.*?)\r?\n'@")
    if (-not $helperMatch.Success) { throw '无法读取原生 smoke API helper。' }
    Add-Type -TypeDefinition $helperMatch.Groups[1].Value
}
[MinecraftNativeSmoke]::SetProcessDPIAware() | Out-Null
if (-not ('MinecraftAutomationFocus' -as [type])) {
    Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
public static class MinecraftAutomationFocus {
    [DllImport("kernel32.dll")] static extern uint GetCurrentThreadId();
    [DllImport("user32.dll")] static extern uint GetWindowThreadProcessId(IntPtr h, out uint p);
    [DllImport("user32.dll")] static extern bool AttachThreadInput(uint a, uint b, bool attach);
    [DllImport("user32.dll")] static extern bool SetForegroundWindow(IntPtr h);
    [DllImport("user32.dll")] static extern IntPtr SetFocus(IntPtr h);
    [DllImport("user32.dll")] static extern IntPtr GetFocus();
    public static bool Focus(IntPtr window, IntPtr control) {
        SetForegroundWindow(window);
        uint process;
        uint target = GetWindowThreadProcessId(control, out process);
        uint caller = GetCurrentThreadId();
        bool attached = target == caller || AttachThreadInput(caller, target, true);
        if (!attached) return false;
        try { SetFocus(control); return GetFocus() == control; }
        finally { if (target != caller) AttachThreadInput(caller, target, false); }
    }
}
'@
}

function Assert-Auto([bool] $Condition, [string] $Message) {
    if (-not $Condition) { throw $Message }
}
function Wait-Auto([scriptblock] $Condition, [string] $Description, [int] $TimeoutSeconds = 65) {
    $until = [DateTime]::UtcNow.AddSeconds($TimeoutSeconds)
    $nextProgress = [DateTime]::UtcNow.AddSeconds(10)
    do {
        if (& $Condition) { return }
        if ($null -ne $script:app -and $script:app.HasExited) { throw "程序提前退出：$Description" }
        if ([DateTime]::UtcNow -ge $nextProgress) {
            Write-Host "WAIT $Description"
            $nextProgress = [DateTime]::UtcNow.AddSeconds(10)
        }
        Start-Sleep -Milliseconds 200
    } while ([DateTime]::UtcNow -lt $until)
    $status = if ($script:window -ne [IntPtr]::Zero) { Get-AutoStatus } else { '' }
    throw "等待超时：$Description；$status"
}
function Post-AutoCommand([int] $Id) {
    Assert-Auto ([MinecraftNativeSmoke]::PostMessage($script:window, 0x0111, [IntPtr] $Id, [IntPtr]::Zero)) "发送控件命令失败：$Id"
}
function Get-AutoStatus {
    # The status is deliberately found by visible text rather than an internal ID.
    return (@([MinecraftNativeSmoke]::Children($script:window) | Where-Object { $_.ClassName -eq 'Static' -and $_.Text -match '自动|定时备份|游戏.*运行|Minecraft.*运行' } | ForEach-Object Text) -join "`n")
}
function Get-StartupSnapshot {
    $key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Software\Microsoft\Windows\CurrentVersion\Run', $false)
    if ($null -eq $key) { return '<absent>' }
    try {
        if ($key.GetValueNames() -notcontains 'MinecraftWorldBackup') { return '<absent>' }
        return ([string] $key.GetValueKind('MinecraftWorldBackup')) + ':' + [string] $key.GetValue('MinecraftWorldBackup', $null, [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
    } finally { $key.Dispose() }
}
function Start-TestApp([bool] $Background) {
    $start = [Diagnostics.ProcessStartInfo]::new()
    $start.FileName = $script:exe
    $start.WorkingDirectory = $repo
    $start.UseShellExecute = $false
    $start.WindowStyle = [Diagnostics.ProcessWindowStyle]::Hidden
    if ($script:TestHost) {
        $start.Arguments = '-test.run=^TestNativeAutomationHost$ -test.v'
        $start.Environment['MINECRAFT_UI_FIXTURE'] = $script:fixture
        $start.Environment['MINECRAFT_UI_FOREGROUND'] = if ($Background) { '0' } else { '1' }
    } elseif ($Background) { $start.Arguments = '--background' }
    $start.Environment['APPDATA'] = $script:roaming
    $start.Environment['LOCALAPPDATA'] = $script:local
    $start.Environment['USERPROFILE'] = $script:profile
    $start.Environment['HOME'] = $script:profile
    return [Diagnostics.Process]::Start($start)
}
function Start-TestGame {
    if ($script:TestHost) {
        [IO.File]::WriteAllText((Join-Path $script:fixture 'game-running.marker'), 'running', [Text.UTF8Encoding]::new($false))
        return
    }
    $start = [Diagnostics.ProcessStartInfo]::new()
    $start.FileName = $script:fakeGameExe
    $start.Arguments = '-t 127.0.0.1'
    $start.WorkingDirectory = $script:fixture
    $start.UseShellExecute = $false
    $start.CreateNoWindow = $true
    $start.WindowStyle = [Diagnostics.ProcessWindowStyle]::Hidden
    $script:game = [Diagnostics.Process]::Start($start)
    Start-Sleep -Milliseconds 100
    Assert-Auto (-not $script:game.HasExited) '模拟游戏进程未保持运行。'
}
function Stop-TestGame {
    if ($script:TestHost) {
        $marker = Join-Path $script:fixture 'game-running.marker'
        if (Test-Path -LiteralPath $marker) { Remove-Item -LiteralPath $marker -Force }
        return
    }
    if ($null -ne $script:game) {
        if (-not $script:game.HasExited) { $script:game.Kill(); $script:game.WaitForExit() }
        $script:game.Dispose()
        $script:game = $null
    }
}
function Wait-GameObserved {
    Wait-Auto { (Get-AutoStatus) -match 'Minecraft 正在运行，等待游戏退出' } '监测到模拟游戏运行' 40
}
function Wait-AutoComplete {
    Wait-Auto { (Get-AutoStatus) -match '自动备份.*(完成|最新|未变化|已更新|没有变化|保持)' } '游戏退出后自动备份完成'
}
function Get-ArchiveSnapshot {
    $state = @{}
    if (Test-Path -LiteralPath $script:autoDir) {
        foreach ($file in Get-ChildItem -LiteralPath $script:autoDir -Filter '*.zip' -File) {
            $state[$file.Name] = [ordered]@{
                Hash = (Get-FileHash -LiteralPath $file.FullName -Algorithm SHA256).Hash
                ModifiedTicks = $file.LastWriteTimeUtc.Ticks
                Length = $file.Length
            }
        }
    }
    return $state
}
function Assert-Unchanged([hashtable] $Before, [hashtable] $After, [string[]] $Names) {
    foreach ($name in $Names) {
        Assert-Auto ($Before.ContainsKey($name) -and $After.ContainsKey($name)) "缺少自动备份：$name"
        Assert-Auto ($Before[$name].Hash -eq $After[$name].Hash -and $Before[$name].ModifiedTicks -eq $After[$name].ModifiedTicks) "未变化的世界被重写：$name"
    }
}
function Get-WorldArchiveName([string] $Edition, [string] $Path) {
    $sha = [Security.Cryptography.SHA256]::Create()
    try {
        $bytes = [Text.Encoding]::UTF8.GetBytes($Edition.ToLowerInvariant() + [char]0 + [IO.Path]::GetFullPath($Path).ToLowerInvariant())
        $id = [Convert]::ToHexString($sha.ComputeHash($bytes)).ToLowerInvariant()
        return $Edition.ToLowerInvariant() + '-' + $id + '.zip'
    } finally { $sha.Dispose() }
}
function Assert-ArchiveWorldBytes([string] $ArchiveName, [byte[]] $Expected) {
    $zip = [IO.Compression.ZipFile]::OpenRead((Join-Path $script:autoDir $ArchiveName))
    try {
        $entries = @($zip.Entries | Where-Object FullName -match '(^|/)level\.dat$')
        Assert-Auto ($entries.Count -eq 1) "自动备份应只包含一个世界：$ArchiveName"
        $stream = $entries[0].Open()
        $memory = [IO.MemoryStream]::new()
        try { $stream.CopyTo($memory); $actual = $memory.ToArray() }
        finally { $stream.Dispose(); $memory.Dispose() }
        Assert-Auto ([Convert]::ToBase64String($actual) -eq [Convert]::ToBase64String($Expected)) "自动备份内世界字节不一致：$ArchiveName"
    } finally { $zip.Dispose() }
}

$exe = if ($TestHost) { Join-Path $repo 'build\MinecraftNativeIntegration.test.exe' } else { [IO.Path]::GetFullPath($ExePath) }
$testedProcessName = [IO.Path]::GetFileNameWithoutExtension($exe)
Assert-Auto (Test-Path -LiteralPath $exe -PathType Leaf) '请先构建程序。'
$alreadyRunning = @(if ($TestHost) {
    Get-Process -Name 'Minecraft地图备份', 'MinecraftNativeIntegration.test' -ErrorAction SilentlyContinue
} else { Get-Process -ErrorAction SilentlyContinue | Where-Object {
    if ($_.ProcessName -in @('Minecraft地图备份', $testedProcessName, 'Minecraft.Windows')) { return $true }
    if ($_.ProcessName -ne 'Minecraft') { return $false }
    # Current Store Launcher uses Minecraft.exe too; it is not the game.
    try { return $_.Path -notmatch '(?i)[\\/]WindowsApps[\\/]Microsoft\.4297127D64EC6_[^\\/]+[\\/]Minecraft\.exe$' }
    catch { return $true }
} })
Assert-Auto ($alreadyRunning.Count -eq 0) '已有备份程序或 Minecraft 运行，无法隔离模拟游戏测试。'
$fixture = Join-Path $repo ('build\automation-smoke-' + [Guid]::NewGuid().ToString('N'))
if ($ResumeFixture) {
    Assert-Auto ([bool] $TestHost) '-ResumeFixture 仅支持隔离测试宿主。'
    $fixture = [IO.Path]::GetFullPath($ResumeFixture)
    $buildRoot = [IO.Path]::GetFullPath((Join-Path $repo 'build')) + [IO.Path]::DirectorySeparatorChar
    Assert-Auto ($fixture.StartsWith($buildRoot, [StringComparison]::OrdinalIgnoreCase) -and (Test-Path -LiteralPath $fixture -PathType Container)) '续测目录必须是 repo/build 内已有的私有 fixture。'
}
$roaming = Join-Path $fixture 'Roaming'
$local = Join-Path $fixture 'Local'
$profile = Join-Path $fixture 'Profile'
$dest = Join-Path $fixture '备份'
$autoDir = Join-Path $dest '自动备份'
$settingsPath = Join-Path $local 'MinecraftWorldBackup\settings.json'
$bedrock = Join-Path $roaming 'Minecraft Bedrock\Users\Smoke\games\com.mojang\minecraftWorlds\自动基岩世界'
$java = Join-Path $roaming '.minecraft\saves\自动Java世界'
if (-not $ResumeFixture) {
foreach ($dir in @($profile, $dest, (Split-Path -Parent $settingsPath), (Join-Path $bedrock 'db'), (Join-Path $java 'region'))) {
    New-Item -ItemType Directory -Path $dir -Force | Out-Null
}
$bedrockBytes = [byte[]](0, 1, 2, 255, 128)
$javaBytes = [byte[]](31, 139, 8, 0, 255)
[IO.File]::WriteAllBytes((Join-Path $bedrock 'level.dat'), $bedrockBytes)
[IO.File]::WriteAllBytes((Join-Path $bedrock 'db\CURRENT'), [byte[]](9, 8, 7))
Set-Content -LiteralPath (Join-Path $bedrock 'levelname.txt') -Value '自动基岩世界' -Encoding UTF8 -NoNewline
[IO.File]::WriteAllBytes((Join-Path $java 'level.dat'), $javaBytes)
[IO.File]::WriteAllBytes((Join-Path $java 'region\r.0.0.mca'), [byte[]](4, 5, 6, 7))
Set-Content -LiteralPath (Join-Path $java 'levelname.txt') -Value '自动Java世界' -Encoding UTF8 -NoNewline
$settingsJson = @{ backupDir = $dest; automatic = $true } | ConvertTo-Json
[IO.File]::WriteAllText($settingsPath, $settingsJson, [Text.UTF8Encoding]::new($false))
} else {
    Assert-Auto (Test-Path -LiteralPath $settingsPath -PathType Leaf) '续测 fixture 缺少设置文件。'
}
$fakeGameExe = Join-Path $fixture 'Minecraft.Windows.exe'
if (-not $TestHost) { Copy-Item -LiteralPath (Join-Path $env:WINDIR 'System32\ping.exe') -Destination $fakeGameExe }
$bedrockArchive = Get-WorldArchiveName 'Bedrock' $bedrock
$javaArchive = Get-WorldArchiveName 'Java' $java
$startupBefore = Get-StartupSnapshot
$app = $null
$activation = $null
$game = $null
$window = [IntPtr]::Zero
$evidence = [ordered]@{ Fixture = $fixture; TestHost = [bool] $TestHost; ResumedFixture = [bool] $ResumeFixture; ExeSHA256 = (Get-FileHash -LiteralPath $exe -Algorithm SHA256).Hash }

try {
    if (-not $ResumeFixture) {
    Start-TestGame
    $app = Start-TestApp $true
    Wait-Auto { @([MinecraftNativeSmoke]::Windows($app.Id) | Where-Object ClassName -eq 'MinecraftWorldBackupNativeWindow').Count -eq 1 } '后台原生窗口创建' 15
    $window = @([MinecraftNativeSmoke]::Windows($app.Id) | Where-Object ClassName -eq 'MinecraftWorldBackupNativeWindow')[0].Handle
    Assert-Auto (-not [MinecraftNativeSmoke]::IsWindowVisible($window)) '--background 启动抢先显示了窗口。'
    $activation = Start-TestApp $false
    Assert-Auto ($activation.WaitForExit(5000) -and $activation.ExitCode -eq 0) '普通启动没有转交已有后台实例。'
    Wait-Auto { [MinecraftNativeSmoke]::IsWindowVisible($window) } '普通启动激活后台窗口' 10
    Assert-Auto (@([MinecraftNativeSmoke]::Windows($app.Id) | Where-Object ClassName -eq 'MinecraftWorldBackupNativeWindow').Count -eq 1) '存在多个主窗口。'
    Assert-Auto ([MinecraftNativeSmoke]::GetDlgItem($window, 110) -ne [IntPtr]::Zero -and [MinecraftNativeSmoke]::GetDlgItem($window, 111) -ne [IntPtr]::Zero) '自动备份/开机启动原生控件缺失。'
    [MinecraftNativeSmoke]::PostMessage($window, 0x0010, [IntPtr]::Zero, [IntPtr]::Zero) | Out-Null
    Wait-Auto { -not [MinecraftNativeSmoke]::IsWindowVisible($window) } '关闭窗口隐藏到托盘' 10
    Assert-Auto (-not $app.HasExited) '关闭主窗口退出了后台程序。'
    [MinecraftNativeSmoke]::PostMessage($window, 0x8002, [IntPtr]::Zero, [IntPtr] 0x0202) | Out-Null
    Wait-Auto { [MinecraftNativeSmoke]::IsWindowVisible($window) } '托盘消息显示窗口' 10
    Write-Host 'PASS background hidden / single-instance activate / close-to-tray / tray restore'

    Wait-GameObserved
    Assert-Auto ((Get-ArchiveSnapshot).Count -eq 0) '游戏运行时提前生成了自动备份。'
    Write-Host 'PASS active game blocks automatic backup'
    Stop-TestGame
    Wait-Auto { (Get-ArchiveSnapshot).Count -eq 2 } '首次退出游戏后生成 2 个自动备份'
    Wait-AutoComplete
    $initial = Get-ArchiveSnapshot
    Assert-Auto ($initial.ContainsKey($bedrockArchive) -and $initial.ContainsKey($javaArchive)) '自动备份文件未按世界独立保存。'
    Assert-ArchiveWorldBytes $bedrockArchive $bedrockBytes
    Assert-ArchiveWorldBytes $javaArchive $javaBytes
    $evidence.Initial = $initial
    Write-Host 'PASS first game exit / 2 independent ZIPs / archive bytes'

    Start-TestGame
    Wait-GameObserved
    Assert-Unchanged $initial (Get-ArchiveSnapshot) @($bedrockArchive, $javaArchive)
    Stop-TestGame
    Wait-AutoComplete
    $same = Get-ArchiveSnapshot
    Assert-Auto ($same.Count -eq 2) '未变化轮增加了额外 ZIP。'
    Assert-Unchanged $initial $same @($bedrockArchive, $javaArchive)
    $evidence.UnchangedSession = $same
    Write-Host 'PASS unchanged session preserves ZIP hash and mtime'

    Start-TestGame
    Wait-GameObserved
    $javaBytes = [byte[]](66, 77, 88, 99, 0, 255)
    [IO.File]::WriteAllBytes((Join-Path $java 'level.dat'), $javaBytes)
    Assert-Unchanged $same (Get-ArchiveSnapshot) @($bedrockArchive, $javaArchive)
    Stop-TestGame
    Wait-Auto { $now = Get-ArchiveSnapshot; $now.ContainsKey($javaArchive) -and $now[$javaArchive].Hash -ne $same[$javaArchive].Hash } '只替换内容变化的 Java 世界'
    Wait-AutoComplete
    $changed = Get-ArchiveSnapshot
    Assert-Auto ($changed.Count -eq 2) '变化世界没有在原位置替换备份。'
    Assert-Unchanged $same $changed @($bedrockArchive)
    Assert-Auto ($changed[$javaArchive].ModifiedTicks -ne $same[$javaArchive].ModifiedTicks) '变化世界的 ZIP 未替换。'
    Assert-ArchiveWorldBytes $javaArchive $javaBytes
    $evidence.OneChangedWorld = $changed
    Write-Host 'PASS changed world replaces only its own ZIP'

    $third = Join-Path $roaming '.minecraft\saves\新增第三世界'
    New-Item -ItemType Directory -Path $third -Force | Out-Null
    $thirdBytes = [byte[]](12, 34, 56, 78)
    [IO.File]::WriteAllBytes((Join-Path $third 'level.dat'), $thirdBytes)
    Set-Content -LiteralPath (Join-Path $third 'levelname.txt') -Value '新增第三世界' -Encoding UTF8 -NoNewline
    $thirdArchive = Get-WorldArchiveName 'Java' $third
    Wait-Auto { (Get-ArchiveSnapshot).Count -eq 3 } '发现新增第三世界并自动备份'
    Wait-AutoComplete
    $added = Get-ArchiveSnapshot
    Assert-Unchanged $changed $added @($bedrockArchive, $javaArchive)
    Assert-Auto ($added.ContainsKey($thirdArchive)) '新增世界缺少独立自动备份。'
    Assert-ArchiveWorldBytes $thirdArchive $thirdBytes
    $evidence.AddedWorld = $added
    Write-Host 'PASS new world adds one ZIP / existing ZIPs preserved'
    } else {
        $app = Start-TestApp $true
        Wait-Auto { @([MinecraftNativeSmoke]::Windows($app.Id) | Where-Object ClassName -eq 'MinecraftWorldBackupNativeWindow').Count -eq 1 } '续测已有自动备份 fixture' 15
        $window = @([MinecraftNativeSmoke]::Windows($app.Id) | Where-Object ClassName -eq 'MinecraftWorldBackupNativeWindow')[0].Handle
        [MinecraftNativeSmoke]::PostMessage($window, 0x8002, [IntPtr]::Zero, [IntPtr] 0x0202) | Out-Null
        Wait-Auto { [MinecraftNativeSmoke]::IsWindowVisible($window) } '显示续测定时窗口' 10
        $added = Get-ArchiveSnapshot
        Assert-Auto ($added.Count -eq 3) '续测 fixture 必须已有 3 个已验证的世界 ZIP。'
        $third = Join-Path $roaming '.minecraft\saves\新增第三世界'
        $thirdArchive = Get-WorldArchiveName 'Java' $third
        $evidence.ExistingArchives = $added
    }

    $scheduleControl = [MinecraftNativeSmoke]::GetDlgItem($window, 113)
    $hoursControl = [MinecraftNativeSmoke]::GetDlgItem($window, 114)
    Assert-Auto ($scheduleControl -ne [IntPtr]::Zero -and $hoursControl -ne [IntPtr]::Zero) '定时间隔控件缺失。'
    [MinecraftNativeSmoke]::PostMessage($scheduleControl, 0x014e, [IntPtr] 1, [IntPtr]::Zero) | Out-Null
    [MinecraftNativeSmoke]::PostMessage($window, 0x0111, [IntPtr] (113 -bor (1 -shl 16)), $scheduleControl) | Out-Null
    Wait-Auto {
        $saved = Get-Content -LiteralPath $settingsPath -Encoding UTF8 -Raw | ConvertFrom-Json
        $saved.PSObject.Properties.Name -contains 'intervalHours' -and $saved.intervalHours -eq 12
    } '切换定时模式并保存默认 12 小时' 10
    Wait-Auto { (Get-AutoStatus) -match '定时备份：每 12 小时检查一次' } '定时模式刷新完成' 20
    Assert-Auto ([MinecraftAutomationFocus]::Focus($window, $hoursControl)) '无法聚焦时间输入框。'
    Assert-Auto ([MinecraftNativeSmoke]::SetText($hoursControl, '24')) '设置 24 小时失败。'
    Assert-Auto ([MinecraftNativeSmoke]::Text($hoursControl) -eq '24') '时间输入框没有显示 24。'
    Assert-Auto ([MinecraftAutomationFocus]::Focus($window, [MinecraftNativeSmoke]::GetDlgItem($window, 101))) '无法移出时间输入框焦点。'
    Wait-Auto {
        (Get-Content -LiteralPath $settingsPath -Encoding UTF8 -Raw | ConvertFrom-Json).intervalHours -eq 24
    } '24 小时失焦后持久化' 10
    $scheduledSettings = Get-Content -LiteralPath $settingsPath -Encoding UTF8 -Raw | ConvertFrom-Json
    Assert-Auto ($scheduledSettings.automatic -and $null -ne $scheduledSettings.lastAutomaticAt) '定时模式未保留自动启用状态或上次成功时间。'
    Wait-Auto { (Get-AutoStatus) -match '定时备份：每 24 小时检查一次' } '定时模式显示下一次检查时间' 20
    $evidence.ScheduleBeforeRestart = $scheduledSettings
    Post-AutoCommand 109
    Assert-Auto ($app.WaitForExit(5000) -and $app.ExitCode -eq 0) '切换定时后未能退出宿主。'
    $app.Dispose()
    $app = $null
    if ($null -ne $activation) { $activation.Dispose(); $activation = $null }
    # A real content change makes an unwanted immediate restart backup observable.
    [IO.File]::WriteAllBytes((Join-Path $third 'level.dat'), [byte[]](90, 91, 92, 93))
    $app = Start-TestApp $true
    Wait-Auto { @([MinecraftNativeSmoke]::Windows($app.Id) | Where-Object ClassName -eq 'MinecraftWorldBackupNativeWindow').Count -eq 1 } '按持久化定时间隔重新启动宿主' 15
    $window = @([MinecraftNativeSmoke]::Windows($app.Id) | Where-Object ClassName -eq 'MinecraftWorldBackupNativeWindow')[0].Handle
    [MinecraftNativeSmoke]::PostMessage($window, 0x8002, [IntPtr]::Zero, [IntPtr] 0x0202) | Out-Null
    Wait-Auto { [MinecraftNativeSmoke]::IsWindowVisible($window) } '显示重启后的定时窗口' 10
    $hoursControl = [MinecraftNativeSmoke]::GetDlgItem($window, 114)
    Assert-Auto ([MinecraftNativeSmoke]::Text($hoursControl) -eq '24') '重启后未保留 24 小时。'
    Wait-Auto { (Get-AutoStatus) -match '定时备份：每 24 小时检查一次' } '重启沿用上次成功时间并延后自动备份' 25
    $observeUntil = [DateTime]::UtcNow.AddSeconds(18)
    Wait-Auto { [DateTime]::UtcNow -ge $observeUntil } '定时模式跨一次轮询保持延迟' 25
    $scheduledAfter = Get-Content -LiteralPath $settingsPath -Encoding UTF8 -Raw | ConvertFrom-Json
    Assert-Auto ($scheduledAfter.intervalHours -eq 24 -and $scheduledAfter.lastAutomaticAt -eq $scheduledSettings.lastAutomaticAt) '重启绕过了已保存的定时间隔。'
    Assert-Unchanged $added (Get-ArchiveSnapshot) @($bedrockArchive, $javaArchive, $thirdArchive)
    $evidence.ScheduleAfterRestart = $scheduledAfter
    $evidence.ScheduledRestartPreservedArchives = $true
    Write-Host 'PASS schedule 24 hours / persisted restart / no immediate content backup'
    Assert-Auto ((Get-StartupSnapshot) -ceq $startupBefore) '测试过程中开机启动项发生了变化。'
    $evidence.StartupRegistryUnchanged = $true
    $evidence.FinalStatus = Get-AutoStatus
    $evidence | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath (Join-Path $fixture 'evidence.json') -Encoding UTF8
    Write-Host 'PASS startup registry unchanged'

    Add-Type -AssemblyName System.Drawing
    $rect = [MinecraftNativeSmoke+Rect]::new()
    [MinecraftNativeSmoke]::GetWindowRect($window, [ref] $rect) | Out-Null
    $bitmap = [Drawing.Bitmap]::new($rect.Right - $rect.Left, $rect.Bottom - $rect.Top)
    $graphics = [Drawing.Graphics]::FromImage($bitmap)
    $dc = $graphics.GetHdc()
    try { Assert-Auto ([MinecraftNativeSmoke]::PrintWindow($window, $dc, 0)) '捕获自动备份窗口失败。' }
    finally { $graphics.ReleaseHdc($dc); $graphics.Dispose() }
    try { $bitmap.Save((Join-Path $fixture 'automatic-window.png'), [Drawing.Imaging.ImageFormat]::Png) }
    finally { $bitmap.Dispose() }
    Post-AutoCommand 109
    Assert-Auto ($app.WaitForExit(5000) -and $app.ExitCode -eq 0) '退出按钮未真正退出后台程序。'
    Write-Host "AUTOMATION_UI_SMOKE_OK fixtures=$fixture"
} finally {
    Stop-TestGame
    foreach ($owned in @($activation, $app)) {
        if ($null -ne $owned) {
            if (-not $owned.HasExited) { $owned.Kill(); $owned.WaitForExit() }
            $owned.Dispose()
        }
    }
}
