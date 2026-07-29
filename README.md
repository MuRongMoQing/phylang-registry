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


## 0.6.2-R5 Windows Git 初始化保护

R5 隔离系统和全局 Git 换行设置，并在远程仓库创建前失败时自动删除本次新建的 `.git`。详见 `HOTFIX-R5-README.zh-CN.md`。
