# Minecraft 地图备份

[English](README.en.md) · [更新日志 / Changelog](CHANGELOG.md) · [下载 Windows 成品](https://github.com/ganwennl/minecraft-backup/releases/latest)

今天重装电脑，我告诉女儿她的 Minecraft 世界没了。她差点掉眼泪，我赶紧告诉她：爸爸有备份，现在就恢复。她从四岁开始玩，到现在快七岁，基岩版已经攒了 11 个世界，都能找回来。

我小时候丢过存档，也丢过网游账号，那种感觉一直记得。所以她刚开始玩，我就给她写了这个定时备份工具，自己配合 NAS 和 Google Drive 留两份，几年用下来一直稳定。现在把它分享出来，希望也能帮你留住那些舍不得丢的世界。NAS 和 Google Drive 是我另外配置的存储/同步方案，工具本身只写入指定目录，没有内置云盘接口。

它支持 Windows 上的**基岩版和 Java 版**，用原生窗口完成备份和恢复。日常使用只需要一个 EXE，不需要 Go、Java 运行环境、浏览器或额外服务。界面目前是中文，英文使用说明见上面的链接。

## 下载与安装

1. 在 [Releases](https://github.com/ganwennl/minecraft-backup/releases) 下载 `MinecraftWorldBackup-2.0.0-windows-amd64.zip`，解压到固定文件夹，例如 `D:\Apps\MinecraftWorldBackup`；也可下载同版本 `.exe` 单独运行。
2. ZIP 内双击 `Minecraft地图备份.exe`。便携运行，无需安装，也不要求管理员权限。
3. 可选：在解压目录运行 `install-shortcuts.ps1` 创建桌面和开始菜单快捷方式。如果 PowerShell 阻止下载的脚本，请先检查内容，再对这个脚本使用 `Unblock-File .\install-shortcuts.ps1`。快捷方式不是使用程序的前提。
4. 成品面向 Windows 64 位 x64 电脑；本次发布实际在 Windows 11 x64 验证，没有发布 macOS、Linux 或 ARM64 成品。

EXE 未做代码签名，Windows 可能提示未知发布者。请从本仓库 Releases 下载，并与同页 `SHA256SUMS.txt` 对照：

```powershell
Get-FileHash -LiteralPath .\MinecraftWorldBackup-2.0.0-windows-amd64.exe -Algorithm SHA256
```

程序图标和按钮资源已编进 EXE，无需另行下载 `assets`。ZIP 还附带双语说明、更新日志、许可声明、版本信息和可选快捷方式脚本。

## 手动备份

1. 保存进度并完全退出 Minecraft，打开备份工具。
2. 检查世界列表；刚创建过世界时点击“重新扫描”。
3. 点击“选择文件夹…”指定备份位置，再点击“开始备份”。一次手动备份包含**所有找到的世界**，目前没有逐个勾选功能。
4. 等显示完成后，点击“打开备份文件夹”。文件类似 `Minecraft-Worlds-20261014-153000.zip`，每次手动备份单独保存。

默认位置是 `%USERPROFILE%\Documents\MinecraftWorldBackups`。不要把备份放在世界目录里面；大世界需要时间和足够空间，处理期间不要关机或强制结束程序。

备份是普通 ZIP，包含世界目录和 `__minecraft_backup_manifest.json`。它不包含游戏程序、启动器、账号、整个模组安装或其它个人文件；世界目录内的文件会随世界保存。建议再复制一份 ZIP 到另一块硬盘或自己的云盘，程序只写入你指定的本地/挂载路径。

## 自动备份

- 勾选“自动备份”，选择“每次游戏退出后”，或“定时备份”并填写 1–8760 小时（例如 12、24）；设置自动保存。
- 到点时游戏仍在运行，会等退出并经过存档安静期后检查；重启程序保留上次成功检查时间。
- 每个世界在保存位置的 `自动备份` 子目录中保留**一份最新 ZIP**。内容没有变化就不重写，新增世界自动加入；手动备份和已经消失世界的自动备份不会被删除。
- 新 ZIP 写完、内容校验通过后才替换旧 ZIP。失败、游戏重启或存档继续变化时保留上次备份。
- 自动备份需要工具仍在运行。启用后关闭或最小化窗口会隐藏到托盘，可从托盘打开或退出。若需要多个历史版本，请同时做手动备份或复制自动 ZIP。
- 可选勾选“开机启动”，在当前 Windows 用户登录后后台运行。保持 EXE 的位置不变；移动前先取消，再从新位置重新勾选。

## 恢复世界

1. 完全退出 Minecraft，先给当前世界另做一次手动备份。
2. 在“恢复文件”选择本工具 ZIP、普通世界 ZIP 或基岩版 `.mcworld`，点击“开始恢复”。
3. 完成后重新打开游戏，查看世界列表。

恢复创建新目录，**同名目录另存为带 `_restored_时间` 的名字，不覆盖现有世界**，不修改原备份。多世界包若中途失败，之前成功恢复的世界仍可能存在；根据提示检查后再重试。

本工具不转换基岩/Java 存档、不转换游戏版本，也不安装模组。请使用兼容游戏版本和世界原本需要的模组。普通 ZIP 应包含一个世界，或顶层多个世界文件夹，不是任意层级的整机/启动器备份；不确定时优先恢复本工具生成的包。

基岩版恢复到自动检测到、包含世界最多的存档目录；多版本或多账号时请核对窗口路径和目标游戏。未找到目录时会尝试新版基岩 Shared 目录。Java 恢复到 `%APPDATA%\.minecraft\saves`。目前没有自定义恢复目录选择器。

## 可以找到哪些存档

| 游戏 | 自动扫描范围 |
| --- | --- |
| 基岩新版 GDK | `%APPDATA%\Minecraft Bedrock\…\minecraftWorlds` |
| 基岩旧商店版 | `%LOCALAPPDATA%\Packages\Microsoft.MinecraftUWP_*\LocalState\games\com.mojang\minecraftWorlds` |
| Preview / Beta | `Minecraft Bedrock Preview` 和 `Microsoft.MinecraftWindowsBeta_*` 对应目录 |
| Java | `%APPDATA%\.minecraft\saves` |

Java 自定义启动器、独立整合包、服务器目录及手机/主机存档不会被自动扫描。请自行保存这些世界的原目录，不要为了识别而移动唯一存档。

## 常见问题与维护

- **没有找到世界：**确认当前 Windows 用户运行过游戏并创建过世界，再重新扫描；自定义 Java 路径不在扫描范围内。
- **提示游戏正在运行：**退出游戏后再试。游戏状态无法确认时也会阻止操作，以免读取正在写入的存档；不要随意结束不相关 Java 程序。
- **自动备份没执行：**确认已启用、工具仍在托盘、保存路径可写且有空间，查看自动状态；定时模式不保证整点执行。
- **恢复后找不到世界：**核对基岩正式版/Preview、Windows 用户和窗口显示的路径；同名目录会有恢复后缀。
- **换电脑：**复制 ZIP，在新电脑安装并运行对应 Minecraft 版本，再用工具恢复；不用复制旧设置。
- **升级：**从托盘完全退出工具后替换 EXE，备份 ZIP 不受影响；启用了开机启动时优先原位置替换。
- **卸载：**先取消“开机启动”，退出工具，删除程序及快捷方式。设置在 `%LOCALAPPDATA%\MinecraftWorldBackup\settings.json`，可另删；保留备份文件。

## 从源码构建

需要 Windows、`go.mod` 要求的 Go 版本或更新版本，以及 PATH 中的 GNU `windres.exe`（如 MinGW-w64 / MSYS2 提供）。普通构建使用已有 ICO，不需要 Node.js。

```powershell
.\build.ps1
go test ./...
go vet ./...
.\native_ui_test.ps1
.\automation_ui_test.ps1
```

UI 测试需要可交互的 Windows 桌面，会启动隔离窗口和模拟游戏进程；先退出真实游戏及工具。测试存档只放在忽略的 `build` 目录，检查备份恢复 SHA-256、同名不覆盖、单实例、托盘及自动流程，不代表验证了每一种真实 Minecraft 世界。

真实游戏运行时，可用隔离自动测试宿主；这不替代发布 EXE 验证：

```powershell
go test -c -o build\MinecraftNativeIntegration.test.exe .
.\automation_ui_test.ps1 -TestHost
```

在 PowerShell 7 中生成带版本资源、便携 ZIP 和 SHA-256 的发布文件：

```powershell
.\package-release.ps1 -OutputDirectory .\dist
.\native_ui_test.ps1 -ExePath .\dist\MinecraftWorldBackup-2.0.0-windows-amd64.exe
.\automation_ui_test.ps1 -ExePath .\dist\MinecraftWorldBackup-2.0.0-windows-amd64.exe
```

版本来自 `VERSION`。图标源在 `assets/*.svg`；重新生成时才需要 Node.js 和 `sharp`，执行 `node scripts/build-icons.cjs`。

## 反馈与许可

欢迎在 [Issues](https://github.com/ganwennl/minecraft-backup/issues) 说明游戏版本、扫描路径和错误。请隐去 Windows 用户名等私人信息，不要上传自己的完整世界或设置文件。

我的代码按 [MIT 许可](LICENSE) 发布；第三方代码和素材保留各自许可，见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。这是独立个人工具，与 Mojang 或 Microsoft 没有从属关系。
