# Minecraft World Backup

[中文](README.md) · [Changelog](CHANGELOG.md) · [Download for Windows](https://github.com/ganwennl/minecraft-backup/releases/latest)

Today I reinstalled our PC and told my daughter her Minecraft worlds were gone. She was almost in tears, so I quickly told her: Dad has a backup, and we can restore them now. She started playing at four and is almost seven now. She has built 11 Bedrock worlds, and we could get them all back.

I still remember losing my own saves and an online game account as a child. So when she started playing, I wrote this scheduled backup tool and paired it with my NAS and Google Drive for two copies. It has worked reliably for us over those years. I am sharing it in the hope that it can help you keep the worlds you would hate to lose, too. My NAS and Google Drive setup uses separately configured storage/synchronization; the tool itself writes to a chosen folder and has no built-in cloud API.

It backs up and restores **Bedrock and Java Edition worlds on Windows** through a native desktop window. Everyday use needs only the EXE: no Go installation, Java runtime, browser, or extra service is required. The application interface is currently in Chinese; this guide explains its controls in English.

## Download and setup

1. Download `MinecraftWorldBackup-2.0.0-windows-amd64.zip` from [Releases](https://github.com/ganwennl/minecraft-backup/releases) and extract it into a permanent folder, such as `D:\Apps\MinecraftWorldBackup`. The versioned standalone `.exe` is also available.
2. Double-click `Minecraft地图备份.exe` inside the ZIP. It is portable, needs no installer, and runs as your normal Windows user.
3. Optionally run `install-shortcuts.ps1` in that folder for desktop and Start menu shortcuts. If PowerShell blocks a downloaded script, inspect it first, then unblock that file with `Unblock-File .\install-shortcuts.ps1`. Shortcuts are optional.
4. The release targets 64-bit x64 Windows PCs and was actually checked on Windows 11 x64. No macOS, Linux, or ARM64 application is published.

The EXE is unsigned, so Windows may show an unknown publisher warning. Download from this repository and compare its SHA-256 with the release's `SHA256SUMS.txt`:

```powershell
Get-FileHash -LiteralPath .\MinecraftWorldBackup-2.0.0-windows-amd64.exe -Algorithm SHA256
```

Icons are built into the EXE; a separate `assets` folder is unnecessary. The ZIP also includes both guides, the changelog, license notices, build/version information, and the optional shortcut script.

## Make a manual backup

1. Save your progress and fully exit Minecraft, then open the tool.
2. Check the world list. Use **重新扫描** (rescan) after creating a new world.
3. Use **选择文件夹…** (choose folder) for the destination, then **开始备份** (start backup). A manual archive contains **all detected worlds**; individual selection is not currently available.
4. Wait for completion, then use **打开备份文件夹** (open backup folder). Files are named like `Minecraft-Worlds-20261014-153000.zip`; every manual backup is separate.

The default destination is `%USERPROFILE%\Documents\MinecraftWorldBackups`. Keep it outside your world folders. Large worlds need time and disk space; do not shut down or force-quit during an operation.

A backup is a normal ZIP containing world folders and `__minecraft_backup_manifest.json`. It excludes the game application, launcher, account, complete mod installation, and unrelated files; files inside a world directory are included. Keep an extra copy on another drive or in your own cloud storage. The tool writes only to your chosen local or mounted destination.

## Automatic backups

- Enable **自动备份** (automatic backup). Choose **每次游戏退出后** (after each session), or **定时备份** (scheduled) with an interval of 1–8760 hours, such as 12 or 24. Settings save automatically.
- If the game is running when due, it waits for game exit and a quiet period. The last successful check is remembered across restarts.
- Each world keeps **one latest ZIP** in the `自动备份` subfolder. Unchanged contents are not rewritten; new worlds are added. Manual backups and automatic archives for missing worlds are not deleted.
- A new ZIP is written and content-checked before replacement. Failure, a restarted game, or changing contents leaves the previous archive intact.
- The tool must stay running. With automatic backup enabled, closing or minimizing hides it in the tray, whose menu can reopen or exit. For historical versions, also make manual backups or copy automatic ZIPs elsewhere.
- Optionally enable **开机启动** (start at sign-in) for background launch when your Windows user logs in. Keep the EXE in place; disable this setting before moving, then enable it from the new location.

## Restore worlds

1. Fully exit Minecraft and first make a separate backup of the current worlds.
2. Under **恢复文件** (restore file), select a tool ZIP, ordinary world ZIP, or Bedrock `.mcworld`, then **开始恢复** (start restore).
3. When finished, reopen Minecraft and check your world list.

Restoration creates new folders. **Existing folders are preserved**, and conflicting names get an `_restored_timestamp` suffix. The original archive is unchanged. If a multi-world restore fails partway through, previously restored worlds may remain; check the message and folders before retrying.

This tool does not convert Bedrock/Java editions or game versions, or install mods. Use a compatible game version and the mods a world needs. A normal ZIP should contain one world, or several world folders at its top level, rather than arbitrary whole-computer or launcher archives. Prefer tool-generated archives when unsure.

Bedrock restore chooses the detected directory with the most worlds. With multiple editions or accounts, check the displayed paths and intended game. If none is detected, it tries the newer Bedrock Shared location. Java restores into `%APPDATA%\.minecraft\saves`. A custom restore destination picker is not currently available.

## Detected locations

| Edition | Automatic scan |
| --- | --- |
| Newer Bedrock / GDK | `%APPDATA%\Minecraft Bedrock\…\minecraftWorlds` |
| Older Store Bedrock | `%LOCALAPPDATA%\Packages\Microsoft.MinecraftUWP_*\LocalState\games\com.mojang\minecraftWorlds` |
| Preview / Beta | Corresponding `Minecraft Bedrock Preview` and `Microsoft.MinecraftWindowsBeta_*` locations |
| Java | `%APPDATA%\.minecraft\saves` |

Custom Java launchers, separate modpacks, servers, and phone/console saves are not scanned. Keep independent copies of these folders; do not move your only save just to make it detectable.

## Troubleshooting and maintenance

- **No worlds:** run the game as the current Windows user, create a world, then rescan. Custom Java paths are outside the scan range.
- **Game still running:** fully exit and retry. Unknown game state also blocks operations to protect files that may be written; do not randomly terminate unrelated Java programs.
- **No automatic backup:** check that it is enabled, the tool is still in the tray, and the destination is writable with space. Read the automatic status; scheduled checks need not occur at an exact clock time.
- **Restored world missing:** check Bedrock versus Preview, the Windows user, and displayed paths. Conflicting directories receive a suffix.
- **New computer:** copy the ZIP, install and run the appropriate edition, then restore. Old settings are unnecessary.
- **Update:** fully exit from the tray before replacing the EXE. Backups remain separate; retain its location when sign-in startup is enabled.
- **Remove:** disable sign-in startup, exit, and delete the application and shortcuts. Settings may be removed from `%LOCALAPPDATA%\MinecraftWorldBackup\settings.json`; keep your archives.

## Build from source

Use Windows, the Go version required by `go.mod` or newer, and GNU `windres.exe` on PATH, for example from MinGW-w64 / MSYS2. Normal builds use the checked-in ICOs and do not require Node.js.

```powershell
.\build.ps1
go test ./...
go vet ./...
.\native_ui_test.ps1
.\automation_ui_test.ps1
```

UI tests require an interactive Windows desktop and launch isolated windows and simulated game processes. Exit the real game and tool first. Saves stay in the ignored `build` folder. Checks cover byte-identical restoration, duplicate preservation, single-instance behavior, the tray, and automatic flows; they do not validate every real Minecraft world.

With a real game running, automatic flows can use an isolated test host; this does not replace checking the production EXE:

```powershell
go test -c -o build\MinecraftNativeIntegration.test.exe .
.\automation_ui_test.ps1 -TestHost
```

In PowerShell 7, create versioned release files, a portable ZIP, and SHA-256 checksums:

```powershell
.\package-release.ps1 -OutputDirectory .\dist
.\native_ui_test.ps1 -ExePath .\dist\MinecraftWorldBackup-2.0.0-windows-amd64.exe
.\automation_ui_test.ps1 -ExePath .\dist\MinecraftWorldBackup-2.0.0-windows-amd64.exe
```

`VERSION` controls the version. Editable icons live in `assets/*.svg`; only regeneration needs Node.js and `sharp`: run `node scripts/build-icons.cjs`.

## Feedback and licensing

Report game version, detected paths, and errors in [Issues](https://github.com/ganwennl/minecraft-backup/issues). Remove private details such as your Windows username; do not upload complete worlds or settings.

My code is released under the [MIT License](LICENSE). Third-party code and artwork retain their respective terms; see [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md). This is an independent personal tool with no affiliation to Mojang or Microsoft.
