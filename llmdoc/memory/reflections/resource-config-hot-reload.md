# 资源配置热加载复盘

## 任务

为 `pkg/server` 的配置热加载补齐资源重载能力——当统一 JSON 配置文件变更时，同时原子替换 Engine 和 ResourceManager。

## 做得好的

- **与 Engine 热加载完全对称**：`resources` 改为 `atomic.Pointer[resource.Manager]`，`reloadConfig` 统一处理 engine + resources，认知负担低。
- **失败回滚自然正确**：新 Manager 的 Start 或 ValidateResourceDeps 失败时，enginePtr 和 resources 都未被替换，旧配置继续服务。
- **in-flight 请求安全**：handleExecute 在请求开始时捕获 Manager 指针为局部变量，旧 Manager Stop 后 atomic.Value 数据仍可读。

## 教训

- **`resetRegistry` 导出问题**：server_test.go 在 `pkg/server` 包中，需要调用 `resource.ResetRegistry()`。原有的 `resetRegistry` 是未导出的，需要改名为大写。影响了 `resource_test.go` 中的所有调用点。跨包测试 helper 应该从一开始就导出。
- **不需要所有权区分**：最初方案设计了 `callerOwnsResources` 标记来区分自建 Manager 和 caller 传入的 Manager。用户指出第三方也应享受热加载，最终去掉了这个区分，简化了设计。

## 文档更新

- `design_doc/11_resource_manager.md` 新增"配置热加载"小节（设计要点 7）。
- `README.md` 补充配置热加载覆盖引擎和资源。
- `llmdoc/architecture/dag-engine.md` 资源与服务器集成部分新增热加载描述。
