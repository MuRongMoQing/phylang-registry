# PhyLang 0.6.2-R4 Windows UTF-8 JSON 与失败回滚修复

R4 修复 Windows PowerShell 5.1 对无 BOM UTF-8 JSON 使用系统 ANSI 代码页读取的问题。R3 生成的 `index.json` 本身是有效 UTF-8，但 `Get-Content -Raw` 在中文 Windows 上会按 GBK/ANSI 解码，造成乱码，并可能吞掉 JSON 引号，最终让 `ConvertFrom-Json` 报错。

## 修复内容

- `scripts/github/build-registry.ps1` 使用严格 UTF-8 解码读取 TOML、`index.json` 和 `health.json`。
- 所有部署配置文件读取显式使用 UTF-8。
- `Build-Registry-Windows.ps1` 在失败时自动删除不完整的 `build` 目录；使用 `-KeepFailedArtifacts` 可保留现场。
- 新增 `Remove-Failed-Deployment.ps1/.cmd`，默认安全清理构建残留并恢复部署脚本备份。
- 远程仓库删除和本地 `.git` 删除都需要显式双重确认参数，不会自动执行。
- Windows CI 增加清理脚本回归测试。

## 当前 R3 失败的安全清理

R3 的失败发生在本地 JSON 验证阶段，尚未进入 Git 初始化和 GitHub 推送。进入 R3 目录执行：

```powershell
Remove-Item -LiteralPath .\build -Recurse -Force -ErrorAction SilentlyContinue
```

或在 R4 中执行：

```powershell
.\Remove-Failed-Deployment.ps1 -Root $PWD -BuildArtifactsOnly
```
