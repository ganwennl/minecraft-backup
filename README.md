# 我的世界地图备份 / Minecraft World Backup

一个备份和恢复我的世界地图的 Windows 小工具，可以查找基岩版和 Java 版存档。支持备份世界，以及恢复本工具 ZIP、普通世界 ZIP 和 `.mcworld` 文件，同名世界会另存。

开发需要 Windows、Go 和 `windres.exe`，Go 版本见 `go.mod`。源码在根目录，构建和检查：

```powershell
.\build.ps1
go test ./...
go vet ./...
```

运行时选择自己的存档和备份目录，备份或恢复前关闭游戏。Minecraft 名称和第三方素材的权利归原权利人。

## English

A small Windows tool for backing up and restoring Minecraft worlds, with discovery for Bedrock and Java Edition saves. It backs up worlds and restores ZIP files made by this tool, ordinary world ZIP files and `.mcworld` files. Worlds with matching names are saved separately.

Development requires Windows, Go and `windres.exe`; see `go.mod` for the Go version. Source is in the repository root. Use the commands above to build and check it.

Choose your own save and backup directories, and close the game before backing up or restoring. The Minecraft name and third-party assets belong to their respective owners.
