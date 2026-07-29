# PhyLang 0.6.2-R5 Git EOL 初始化修复

R5 修复 Windows Git 在系统或全局 `core.eol=crlf`、`core.autocrlf=true` 环境下首次执行 `git add --all` 时出现：

```text
fatal: LF would be replaced by CRLF in .gitattributes
```

修复内容：

- 仓库级固定 `core.autocrlf=false` 和 `core.eol=lf`；
- 首次暂存使用一次性 `core.safecrlf=false`，由 `.gitattributes` 规范化索引内容；
- `.gitattributes` 与 `.gitignore` 明确使用 LF；
- 若本次运行新建 `.git` 且尚未创建远程仓库，失败时自动移除本次 `.git`；
- 新增 Git for Windows 全局 CRLF 冲突模拟测试；
- GitHub Actions Linux 验证作业执行该回归测试。

R4 在 `git add` 处失败时尚未进入远程仓库创建阶段。清理 R4 残留：

```powershell
.\Remove-Failed-Deployment.ps1 -Root $PWD -RemoveLocalGitRepository -ConfirmLocalPath $PWD.Path
```
