# 第三方声明 / Third-party notices

本项目自有代码适用 [MIT 许可](LICENSE)；这个许可不替代第三方代码、素材、名称或标志的原有权利和许可，也不授予 Minecraft 商标权。

The project's own code uses the [MIT License](LICENSE). It does not replace the rights or terms of third-party code, artwork, names, or logos, and grants no Minecraft trademark rights.

程序使用 Go 标准库及运行时，通过 Windows 系统 API 提供原生界面。Go 的 BSD 许可全文随发行包提供，见 [GO-LICENSE.txt](third_party/Go-LICENSE.txt)。当前 `go.mod` 没有外部 Go 模块依赖。

The application uses the Go standard library and runtime and Windows system APIs. The Go BSD license is included in [GO-LICENSE.txt](third_party/Go-LICENSE.txt). The current `go.mod` contains no external Go module dependencies.

GNU windres / MinGW-w64 是构建工具，不作为组件附在发行包内。Node.js 与 sharp 仅用于可选图标生成，普通构建使用已有 ICO，不分发这些构建工具。仓库中的 SVG/ICO 随源代码保存；MIT 不代表对第三方游戏素材作出额外授权。

GNU windres / MinGW-w64 is a build tool, not a shipped component. Node.js and sharp are used only for optional icon generation; ordinary builds use the checked-in ICOs and do not ship these tools. SVG/ICO assets are retained in the source; MIT does not imply an additional grant for third-party game artwork.

发行包不包含 Minecraft 游戏文件或玩家世界。Minecraft 名称属于相应权利人，本工具与 Mojang 或 Microsoft 没有从属关系。

The release includes no Minecraft game files or player worlds. Minecraft names belong to their respective owners. This tool is not affiliated with Mojang or Microsoft.
