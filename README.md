# PhyLang Registry 0.6.2：Windows 10 一键部署


## 0.6.2-R4 Windows 修复

R3 修复 Windows 本地注册表索引中的相对包地址解析。部署前预检会现场创建并下载一个真实测试包，确保 `C:\...\index.json` 与 `packages/*.phypkg` 的组合可以正常工作。

## 最短步骤

1. 完整解压本部署包到 `D:\PhyLangRegistry062`。
2. 双击 `Deploy-To-GitHub-Windows10.cmd`。
3. 根据浏览器提示登录 GitHub 用户 `MuRongMoQing`。
4. 保持窗口打开，直到显示 `[PASS] PhyLang registry deployment completed.`。

默认目标：

```text
https://github.com/MuRongMoQing/phylang-registry
https://murongmoqing.github.io/phylang-registry/
```

## PowerShell 方式

```powershell
cd D:\PhyLangRegistry062
Set-ExecutionPolicy -Scope Process Bypass
.\Deploy-To-GitHub-Windows10.ps1
```

远程仓库已经存在时，脚本会停止。确认更新现有仓库时：

```powershell
.\Deploy-To-GitHub-Windows10.ps1 -UpdateExisting
```

## 部署前只做本地检查

```powershell
.\Test-Deployment-Package.ps1 -Root $PWD -RunRuntimeSelfTest
.\Build-Registry-Windows.ps1
```

## 成功判据

脚本只有在以下项目都成功后才输出 PASS：

- 本地部署包布局、编码和 Windows 运行时自测；
- 本地静态注册表构建；
- GitHub `validate.yml` 的 Ubuntu 和 Windows 作业；
- Pages 工作流的显式 post-configuration 部署；
- 线上 `health.json` 返回 `ok: true`。


## R4 Windows UTF-8 与回滚修复

Windows PowerShell 5.1 构建脚本现在使用严格 UTF-8 读取生成的 JSON，不再依赖系统 ANSI 代码页。构建失败时默认自动清理不完整的 `build` 目录。手动清理和可选远程删除见 `Remove-Failed-Deployment.ps1`。


## 0.6.2-R6 Windows Git 与工作流监控保护

R6 保留 R5 的 Git 换行隔离，并修复新仓库工作流尚未出现时直接读取缺失 `headSha` 属性的问题。已推送后发生监控错误时保留与远程一致的配置；可运行 `Resume-GitHub-Deployment-Windows10.ps1` 继续验证。详见 `HOTFIX-R6-README.zh-CN.md`。
## 0.6.2-R7 跨平台锁文件与验证门禁修复

R7 修复真实 `windows-latest` 作业中包源码 CRLF/LF 差异导致 `phylang.lock` 的时间戳和 SHA-256 被重写的问题。包目录哈希和 `.phypkg` 现在使用跨平台规范化内容；`.gitattributes` 明确固定 `*.phy` 与 `*.lock` 为 LF。Pages 不再在 push 后直接部署，必须先通过 Linux 与 Windows 验证。现有 R5/R6 仓库使用 `Repair-Existing-GitHub-Deployment-Windows10.ps1` 做普通快进修复。详见 `HOTFIX-R7-README.zh-CN.md`。


## 0.6.2-R8 已提交临时备份清理修复

R8 识别 R5 推送后清理留下的两个已知 `.phylang-deployment-backup` 删除项，不再把它们误判为用户修改；修复提交会从仓库历史当前版本中移除这些临时文件，并通过 `.gitignore` 阻止再次提交。详见 `HOTFIX-R8-README.zh-CN.md`。


## R9 transport recovery

See `HOTFIX-R9-README.zh-CN.md`, `Diagnose-GitHub-HTTPS-TLS-Windows10.ps1`, and `Resume-R9-Repair-Push-Windows10.ps1`.
