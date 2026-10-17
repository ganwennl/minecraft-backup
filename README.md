# Minecraft 地图备份

给女儿写的 Windows 原生地图备份工具，支持基岩版和 Java 版的手动备份、自动备份与恢复。

[下载成品](https://github.com/ganwennl/minecraft-backup/releases/latest) · [使用说明](docs/usage.md) · [English](docs/usage.en.md) · [更新日志](CHANGELOG.md)

## 使用

下载便携包并解压，运行 `Minecraft地图备份.exe`。无需 Go、Java 或浏览器。备份只写入你指定的目录；恢复遇到同名世界会另存，不覆盖原世界。

## 项目目录

| 目录 | 内容 |
| --- | --- |
| `cmd/minecraft-backup/` | 程序入口、备份逻辑、原生界面及相邻 Go 测试 |
| `assets/` | SVG 图标源、生成的 ICO 与嵌入资源 |
| `resources/windows/` | Windows 清单和版本资源模板 |
| `scripts/` | 构建、发布包和图标生成脚本 |
| `tests/ui/` | 原生窗口与自动备份集成测试 |
| `packaging/windows/` | 便携包中的可选快捷方式脚本 |
| `docs/` | 使用说明和开发文档 |
| `third_party/` | 第三方许可 |
| `build/`、`dist/` | 本地测试文件、构建产物；不提交 Git |

## 开发

```powershell
.\build.ps1
.\scripts\test.ps1
```

默认成品输出到 `dist/`。工具链、UI 测试与打包命令见[开发说明](docs/development.md)。

## English

A native Windows tool for backing up and restoring Minecraft Bedrock and Java worlds. Download a portable build from [Releases](https://github.com/ganwennl/minecraft-backup/releases/latest), or read the [English guide](docs/usage.en.md). Source, assets, scripts, tests and packaging files are grouped by purpose.

## 许可

[MIT](LICENSE) · [第三方声明](THIRD_PARTY_NOTICES.md)。独立个人项目，与 Mojang 或 Microsoft 没有从属关系。
