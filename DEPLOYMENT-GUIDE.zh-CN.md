# PhyLang Registry 0.6.2：Windows 10 一键部署

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


## 失败清理

仅清理本地构建残留：

```powershell
.\Remove-Failed-Deployment.ps1 -Root $PWD -BuildArtifactsOnly
```

完整回滚参数见 `docs/06-失败部署清理与回滚.md`。


## 0.6.2-R5 Windows Git 初始化保护

R5 隔离系统和全局 Git 换行设置，并在远程仓库创建前失败时自动删除本次新建的 `.git`。详见 `HOTFIX-R5-README.zh-CN.md`。
