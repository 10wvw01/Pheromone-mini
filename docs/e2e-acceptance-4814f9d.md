# 单进程、单 Pool 三 App E2E 验收

- 验收日期为 2026-11-27（Asia/Shanghai）。
- 验收分支为 `dev`，实际 SHA 为 `4814f9d794b7d28f167b91b07039787c277ae8ec`。
- 实际 Go 版本为 `go version go1.26.2 linux/amd64`。
- 运行环境为 Ubuntu 24.04.4 LTS，Linux `6.8.0-117-generic`，x86_64，cgroup v2 挂载于 `/sys/fs/cgroup`。
- 在指定提交的独立 worktree 根目录执行。源码和配置保持不变，未注入固定资源快照，未使用源码 overlay。

| 命令 | 退出码 |
| --- | ---: |
| `go version` | 0 |
| `go build ./...` | 0 |
| `go vet ./...` | 0 |
| `go test -count=1 ./...` | 0 |
| `go test -race -count=1 ./...` | 0 |
| `go run ./examples` | 0 |
| `go run -race ./examples` | 0 |

## 默认链路与资源探测

`examples` 不调用 `mockpool.New`、`engine.New` 或 `wiring.Install`。上游只调用 `TriggerInit` 和 `EventTriggerNotify`。三个 App 共用进程级 Trigger，默认下游适配器通过 `mockpool.TryEnqueue` 写入根目录 `mockpool` 包的唯一队列。

SDK 先保存完整批次再通知 Trigger。规则命中后，Router 通过内置 Linux 探测完成准入并调用 `TryEnqueue`。模拟下游通过 `mockpool.TryDequeue` 从同一队列取任务，从 `DataAddr` 读入整批数据并执行，再用 `sendObject` 将结果交回 SDK，由 SDK 保存并发出下一次通知。

补充 `strace` 确认示例进程打开了 `/proc/self/cgroup`、`/proc/self/mountinfo`、`/proc/stat`、`/proc/meminfo`，以及所在 cgroup 和父级的 CPU、内存限额与用量文件。两次 E2E 均未出现 `RESOURCE_PROBE_FAILED` 或 `RESOURCE_INSUFFICIENT`。全量 race 测试和 race 示例均未报告数据竞争。

**真实下游 Pool 联调未执行。**

## 实测结果

两次 E2E 输出一致，各有 3 个 App 初始化、10 个任务执行、3 条跨 App 读取。最后一行均为 `E2E passed`。

| App | 最终批次 |
| --- | --- |
| ingest | `[30 50 70]` |
| score | `[65 155 285]` |
| report | `[96 206 356]` |

```text
init examples/app.json
init examples/app-score.json
init examples/app-report.json
ingest/normalize session=session-001 batch=[1 2 3]
ingest/validate session=session-001 batch=[2 4 6]
ingest/enrich session=session-001 batch=[3 5 7]
ingest/publish session=session-001 batch=[30 50 70]
ingest/result/ready = [30 50 70]
score/load session=session-001 batch=[2 3 4]
  SDK read ingest/result/ready -> score/load
score/weight session=session-001 batch=[60 150 280]
score/publish session=session-001 batch=[65 155 285]
score/result/ready = [65 155 285]
report/join session=session-001 batch=[1 1 1]
  SDK read ingest/result/ready -> report/join
  SDK read score/result/ready -> report/join
report/summarize session=session-001 batch=[96 206 356]
report/publish session=session-001 batch=[96 206 356]
report/result/ready = [96 206 356]
E2E passed
```
