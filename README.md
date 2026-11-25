# Pheromone-mini

单节点、单进程内的数据驱动任务调度预研，核心模块使用 C11。

当前设计基线：**V0.4**。负责业务步骤 3、4、5：数据就绪事件 → DIRECT / Immediate / ByName → TaskDescriptor → 本地协程池投递。协程池内部、共享内存池内部均由其他团队负责；队列满只打印进程间分发日志并返回未接收，不实际分发。

- [V0.4 完整软件设计方案（主文档）](docs/V0.4-完整软件设计方案.md)
- [五大部分、21 章与附录导航](docs/README.md)
- [可解析的 JSON 示例](docs/examples/)
- [V0.3 历史快照](https://github.com/10wvw01/Pheromone-mini/tree/06f21152de6be9cad18c1dc792a2fe3dc4b15069/docs)

当前交付为设计文档，非已实现或已验收的软件。
