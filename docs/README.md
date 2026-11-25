# V0.5 设计文档导航

**唯一现行主文档：[V0.5 完整软件设计方案](V0.5-完整软件设计方案.md)**

配套需求基线：[V0.5 分功能需求列表](v0.5/06-分功能需求列表.md)（按模块定义功能目标、输入输出、规则、责任方和验收标准；不计入五大部分、21 章）。

V0.5 采用单一 app.json 注册、Immediate / ByName 两种 Trigger、Inline / Shared-Memory 两种事件数据载体。DIRECT Dependency 不在本期实现。Task Coroutine Pool 和 Data Bucket SDK 的底层实现分别由下游、上游团队负责。

## 五大部分、21 章

| 部分 | 章节 | 文档 |
|---|---|---|
| 总体设计 | 1–3 | [01-总体设计](v0.5/01-总体设计.md) |
| 核心模块详细设计 | 4–10 | [02-核心模块详细设计](v0.5/02-核心模块详细设计.md) |
| 接口与协作设计 | 11–14 | [03-接口与协作设计](v0.5/03-接口与协作设计.md) |
| 工程实现设计 | 15–18 | [04-工程实现设计](v0.5/04-工程实现设计.md) |
| 需求追踪与测试验收 | 19–21 | [05-需求追踪与测试验收](v0.5/05-需求追踪与测试验收.md) |

## 配套需求、附录与示例

- [06-分功能需求列表](v0.5/06-分功能需求列表.md)：按模块定义功能目标、输入输出、规则、责任方和验收标准。
- [附录 A：Pheromone 源码映射与裁剪说明](v0.5/appendix/A-Pheromone源码映射.md)
- [附录 B：单一 app.json 配置规范与示例](v0.5/appendix/B-JSON配置协议示例.md)
- [附录 C：C 接口与 Task ABI](v0.5/appendix/C-C接口与Task-ABI.md)
- [附录 D：数据结构、所有权与错误码](v0.5/appendix/D-数据结构与错误码.md)
- [唯一配置示例：app.json](v0.5/examples/app.json)
- [C ABI 和 SO 示例目录](v0.5/examples/)

## 研发责任边界

- 本模块：App Registry、Event Trigger、Task Builder、Task Router。
- 上游：Data Bucket SDK、Object 存储、1KB 阈值策略、Inline/Shared-Memory 载体选择。
- 下游：Task Coroutine Pool、TaskQueue、协程唤醒、SO 加载与 handler 执行。
- 队列满：只打印进程间分发需求日志，不执行真实分发。

## 历史版本

- [V0.4 主文档与配套文件](archive/v0.4/README.md)
- [V0.3 主文档与配套文件](archive/v0.3/README.md)
- [归档规则](archive/README.md)
