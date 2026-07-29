# PhyLang GitHub Registry Deployment 0.6.2-R8

R8 修复 R7 现有仓库修复脚本对 R5 已知清理残留的误判。

R5 在首次提交前把 `.phylang-deployment-backup` 建在仓库目录内，且当时 `.gitignore` 没有排除它，因此两个临时备份文件被提交。推送后的清理删除了这些文件，工作区于是出现：

```text
 D .phylang-deployment-backup/CODEOWNERS
 D .phylang-deployment-backup/registry-hosting.json
```

这两项是已知部署残留，不是用户修改。R8 只接受这两个路径处于删除状态；任何其他未知改动仍会停止修复。

R8 同时：

- 将 `.phylang-deployment-backup`、R8 修复备份目录和 `deployment-report.json` 加入 `.gitignore`；
- 在修复提交中删除远程仓库里误提交的临时备份文件；
- 保留 R7 的跨平台锁文件、可重复 `.phypkg` 和验证后部署 Pages 修复；
- 修正“本地修复提交已创建但推送失败”时的回滚，使 HEAD 返回修复前提交；
- 不删除远程仓库，不强制推送。

对现有 R5 仓库执行：

```powershell
.\Repair-Existing-GitHub-Deployment-Windows10.ps1 `
  -TargetRoot "P:\PhyLang-GitHub-Registry-Deployment-0.6.2-R5" `
  -Repository "MuRongMoQing/phylang-registry"
```
