# PhyLang 0.6.2-R9：Git HTTPS/TLS 传输恢复修复

R9 针对 R8 在 `git push origin main` 阶段遇到 `TLS connect error: ... unexpected eof while reading` 后撤销本地修复提交的问题。

## 精确行为

- 在修改文件前以只读 `git ls-remote` 测试三种 HTTPS 传输模式：默认、HTTP/1.1、Windows Schannel + HTTP/1.1。
- 只使用已经实际通过只读测试的模式。
- 对明确的可重试传输错误执行有限次退避重试。
- 若本地修复提交已创建但推送仍失败，保留提交和修复内容，不再回退 HEAD。
- 生成脱敏诊断日志，不记录令牌。
- 提供 `Resume-R9-Repair-Push-Windows10.ps1` 从已保留的本地提交继续推送。
- 不关闭 TLS 证书验证，不设置 `http.sslVerify=false`。

## 当前场景

R8 已在提交前完成全部本地测试，但 HTTPS TLS 会话在 push 时被提前关闭。R8 随后恢复了原 HEAD，因此应直接运行 R9 修复脚本，不要删除远程仓库。
