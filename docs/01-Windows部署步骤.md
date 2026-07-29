# GitHub 静态注册表 Windows 10 部署完整步骤

本部署包已绑定：

```text
MuRongMoQing/phylang-registry
https://murongmoqing.github.io/phylang-registry/
```

## 一键部署

完整解压后双击：

```text
Deploy-To-GitHub-Windows10.cmd
```

或执行：

```powershell
cd <解压后的部署包目录>
Set-ExecutionPolicy -Scope Process Bypass
.\Deploy-To-GitHub-Windows10.ps1
```

脚本会检查或安装 Git 与 GitHub CLI，确认登录用户是 `MuRongMoQing`，执行本地 Windows 自测和注册表构建，初始化 Git，创建远程仓库，配置 Actions 和 Pages，推送 main，等待 Ubuntu/Windows 验证与 Pages 部署，并验证线上健康端点。

远程仓库已经存在时不会覆盖。确认更新时：

```powershell
.\Deploy-To-GitHub-Windows10.ps1 -UpdateExisting
```

## 验证

```powershell
gh run list --repo MuRongMoQing/phylang-registry --limit 10
Invoke-RestMethod https://murongmoqing.github.io/phylang-registry/health.json
Invoke-RestMethod https://murongmoqing.github.io/phylang-registry/index.json
```
