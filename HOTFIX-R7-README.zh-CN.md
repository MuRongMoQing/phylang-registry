# PhyLang GitHub Registry Deployment 0.6.2-R7

R7 修复真实 GitHub `windows-latest` 验证中发现的跨平台锁文件不确定性。

## 已确认的原始故障

Windows 作业完成了运行时自测和注册表构建，但 `git diff --exit-code -- ':(glob)**/phylang.lock'` 失败。三个锁文件的 `generated` 和 `sha256` 字段发生变化。原因是包目录哈希直接读取工作区原始字节，而 `.phy` 没有固定 LF；Windows 检出为 CRLF，Linux 检出为 LF，因此同一源码得到不同哈希。

## R7 修复

- `.gitattributes` 明确设置 `*.phy` 和 `*.lock` 为 LF。
- 包目录哈希对有效 UTF-8 文本统一将 CRLF 规范为 LF。
- `.phypkg` 使用固定 ZIP 时间、排序路径、LF 文本内容和 Store 方法，得到跨平台稳定归档。
- 新增 LF/CRLF 哈希、锁文件和归档完全一致的 Go 回归测试。
- Pages 工作流不再直接响应 push；只有验证成功后才由脚本触发。
- Pages 工作流自身再次查询 `validate.yml`，拒绝部署未通过验证的提交。
- R7 恢复脚本会修复现有 R5/R6 Git 仓库、运行本地回归、提交推送、等待真实 Linux/Windows 验证，然后部署 Pages。
- 工作流失败时恢复脚本输出失败 Job、步骤、URL 和日志尾部，不再只显示 `gh run watch` 返回 1。

## 修复现有仓库

在 R7 解压目录运行：

```powershell
.\Repair-Existing-GitHub-Deployment-Windows10.ps1 `
  -TargetRoot "P:\PhyLang-GitHub-Registry-Deployment-0.6.2-R5" `
  -Repository "MuRongMoQing/phylang-registry"
```

不要删除当前远程仓库；该脚本对现有 `main` 做普通快进提交，不使用强制推送。
