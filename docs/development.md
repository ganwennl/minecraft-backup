# 开发与验证

从项目根目录运行命令。需要 Go 1.26.4 或更新版本、PowerShell，以及 PATH 中的 GNU `windres.exe`。普通构建使用现有 ICO，不需要 Node.js。

```powershell
.\build.ps1
.\scripts\test.ps1
.\scripts\test.ps1 -UI
.\scripts\package-release.ps1 -OutputDirectory .\dist\release
```

`build.ps1` 是稳定的构建入口，实际实现在 `scripts/build.ps1`；默认输出 `dist/Minecraft地图备份.exe`，可用 `-OutputPath` 指定其他文件。程序源码在 `cmd/minecraft-backup`，Go 测试与被测代码放在同一包。

只运行 Go 检查：`go test ./...`、`go vet ./...`。UI 测试需要交互桌面，并会启动隔离窗口；先退出真实游戏。真实游戏运行时可使用隔离测试宿主：

```powershell
go test -c -o build/MinecraftNativeIntegration.test.exe ./cmd/minecraft-backup
.\tests\ui\automation_ui_test.ps1 -TestHost
```

图标源和 ICO 放在 `assets/`；修改图标时运行 `node scripts/build-icons.cjs`，此时才需要 Node.js 与 sharp。Windows 清单在 `resources/windows/`，临时版本资源在构建结束后清理。

发布脚本会校验测试、版本、源码清单和 ZIP 内容，保留 `docs/`、许可及可选快捷方式脚本。只生成本地文件，不上传、不安装。使用详情见[中文说明](usage.md)和[English guide](usage.en.md)。
