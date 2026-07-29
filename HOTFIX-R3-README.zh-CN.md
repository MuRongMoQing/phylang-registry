# PhyLang 0.6.2-R3 Windows 注册表相对包路径修复

## 已确认的 R2 缺陷

R2 已经在读取注册表索引时正确识别了 Windows 本地路径，例如：

```text
C:\Users\Name\AppData\Local\Temp\registry\index.json
```

但是，索引中的包地址通常是相对地址：

```text
packages/demo.phypkg
```

R2 的 `loadRegistry` 仍然直接对索引路径调用 `url.Parse`。Go 会把 `C:` 解释为 URL scheme `c`，于是相对包地址被错误解析为：

```text
c:///packages/demo.phypkg
```

Windows 随后把它当成本地 `C:\packages\demo.phypkg`，导致：

```text
open c:///packages/demo.phypkg: The system cannot find the path specified.
```

这就是 `go test ./...` 中 `TestPackageLifecycleAndRegistry` 失败的直接原因。

## R3 修复

1. 新增 `resolveRegistryReference`，统一解析索引中的包地址。
2. 在任何 `url.Parse` 之前区分：
   - Windows 盘符路径；
   - Windows UNC 路径；
   - POSIX 本地路径；
   - `file://`；
   - HTTP/HTTPS；
   - 相对包地址。
3. 新增 `joinLocalRegistryReference`，正确把：

```text
C:\...\registry\index.json
+ packages/demo.phypkg
```

解析为：

```text
C:\...\registry\packages\demo.phypkg
```

4. 增加跨平台 Go 回归测试，覆盖：
   - Windows 反斜杠盘符路径；
   - Windows 正斜杠盘符路径；
   - UNC 路径；
   - `file:///C:/...`；
   - HTTP 地址。
5. Windows 预检不再只检查空注册表，而是现场创建一个真实 `.phypkg`、写入使用 `packages/*.phypkg` 相对地址的索引，然后执行：
   - `package registry add`；
   - `package registry check`；
   - `package fetch`；
   - `package info`。
6. 重新编译 Windows x64 和 Windows ARM64 运行时。

运行时语言版本仍为 `0.6.2`；`R3` 是部署包修订号。
