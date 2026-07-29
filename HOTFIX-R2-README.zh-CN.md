# PhyLang 0.6.2-R2 Windows 本地路径修复

## 已确认的 R1 缺陷

R1 的注册表读取函数先调用 `url.Parse`，再判断是否为本地文件。Windows 绝对路径例如：

```text
C:\Users\Name\AppData\Local\Temp\index.json
```

会被 Go URL 解析器解释为 scheme `c`，因此运行时返回：

```text
不支持的仓库协议 c
```

该问题同时影响运行时内部自测和用户通过 `package registry add` 添加 Windows 本地索引。

## R2 修复

1. 在 URL 解析之前识别 Windows 驱动器绝对路径、UNC 路径、当前平台绝对路径和相对路径。
2. 正确处理 `file:///C:/...` 以及 `file://server/share/...`。
3. 增加 Go 回归测试，覆盖 Windows 盘符、斜杠盘符、UNC、相对路径、HTTP 和 HTTPS。
4. Windows 预检显式执行：
   - `package registry add`，参数为真实的 Windows 临时绝对路径；
   - `package registry check`；
   - 完整 `self-test`。
5. 预检不再通过 `PHYLANG_REGISTRY_URL` 注入 Windows 本地路径，避免环境状态掩盖运行时内部测试。

运行时语言版本仍为 `0.6.2`；`R2` 是部署包修订号。
