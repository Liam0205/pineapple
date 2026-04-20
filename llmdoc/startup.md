# 启动阅读顺序

每次 Pineapple 任务开始前，按顺序阅读以下文档：

1. `llmdoc/must/conventions.md` — 跨代码库约定：JSON 边界、注册模式、命名规范、版本同步、codegen 新鲜度、测试规范。
   - 深入阅读场景：涉及发布/版本、生成文件、算子命名、全仓贡献模式的任务。

2. `llmdoc/overview/project-overview.md` — 项目定位、系统边界，以及为何 Pineapple 拆分为 Python 声明和 Go 执行两层。
   - 深入阅读场景：涉及入口点、包分发、公共 API 或变更归属判断的任务。

3. `llmdoc/architecture/dag-engine.md` — 核心执行模型：引擎编译、DAG 推导、调度器、DataFrame 语义、算子类型规则、行依赖。
   - 深入阅读场景：涉及执行顺序、数据冒险、屏障算子、运行时 bug、算子语义、性能/并发的任务。

4. `llmdoc/architecture/apple-compiler.md` — Python DSL 声明、编译流水线、校验规则、控制流降级、资源声明。
   - 深入阅读场景：涉及 Flow API、JSON 生成、校验错误、控制流、DSL/运行时不匹配的任务。

5. `llmdoc/reference/operator-contract.md` — 算子开发参考：接口、Schema 注册、类型/输出约束。
   - 深入阅读场景：涉及新增或修改算子、Schema、元数据契约、codegen 定义的任务。
