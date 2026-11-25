# Pheromone-mini

单节点、单进程内的数据驱动任务调度与 Trigger 预研，核心设计使用 C11。

当前设计基线：**V0.5**。本模块负责业务步骤③④⑤：App Registry 注册、Immediate / ByName Trigger 匹配、Task Builder 组装 Inline / Shared-Memory 输入、Task Router 提交本地 TaskQueue。

## 当前版本入口

- [V0.5 完整软件设计方案（唯一现行主文档）](docs/V0.5-完整软件设计方案.md)
- [V0.5 五大部分、21 章与附录导航](docs/README.md)
- [V0.5 分功能需求列表](docs/v0.5/06-分功能需求列表.md)
- [V0.5 单一 app.json 示例](docs/v0.5/examples/app.json)

## 历史版本

- [V0.4 历史设计基线](docs/archive/v0.4/V0.4-完整软件设计方案.md)
- [V0.3 历史设计基线](docs/archive/v0.3/V0.3-完整软件设计方案.md)
- [历史版本归档说明](docs/archive/README.md)

**V0.5 边界：** 不实现 DIRECT Dependency、跨进程分发、Data Bucket 底层存储/传输或 Task Coroutine Pool。队列满只记录 PROCESS_DISPATCH_REQUIRED 并返回未接收。T09/T10 是 Inline / Shared-Memory 数据载体接入和生命周期契约测试，不是底层传输研发任务。

当前仓库交付为设计文档与接口示例，非已实现或已验收的软件。

## 仓库管理约定

- `main` 是唯一长期维护分支。
- `docs/*`、`feature/*`、`fix/*` 等均为短期工作分支；PR 合并后应删除。
- 版本内容以文档归档和正式 Git Tag 固定，不为每个版本长期保留开发分支。
