# 三 App E2E 实测记录

- 验收日期：2026-11-27（Asia/Shanghai）
- 分支：`dev`
- 验收提交：`4322bd1f92e33484d42b4fee7cc5ce6bedfa31e2`
- Go：`go version go1.26.2 linux/amd64`
- 环境：Ubuntu 24.04.4 LTS，Linux `6.8.0-117-generic`，x86_64，cgroup v2 挂载于 `/sys/fs/cgroup`。
- 验收在指定提交的独立 worktree 的仓库根目录进行。验收前后源码与配置一致。

| 命令 | 退出码 |
| --- | ---: |
| `go version` | 0 |
| `go build ./...` | 0 |
| `go vet ./...` | 0 |
| `go test -count=1 ./...` | 0 |
| `go run ./examples` | 0 |
| `go run -race ./examples` | 0 |

## Mock Pool 链路

三个 App 分别通过 `TriggerInit` 注册成功。Mock Pool 接收任务，示例逐项出队并核对 AppName、FuncName、SOName、DataAddr 和 SessionID，再从 DataAddr 读取输入并模拟执行。每个任务通过 `sendObject` 模拟 `send_object` 把结果交给 Data SDK，SDK 保存完整批次后调用 `EventTriggerNotify`。

| App | 任务数 | 最终批次 |
| --- | ---: | --- |
| ingest | 4 | `[30 50 70]` |
| score | 3 | `[65 155 285]` |
| report | 3 | `[96 206 356]` |

## 真实 Linux 资源探测

示例使用 Trigger 内置的 Linux 探测器。规则命中后，Router 检查资源，通过后调用 `Pool.TryEnqueue`。10 个任务全部成功入队并被消费。补充的 `strace` 运行确认示例进程打开了 `/proc/self/cgroup`、`/proc/self/mountinfo`、`/proc/stat`、`/proc/meminfo`，以及所在 cgroup 和父级的 `cpu.max`、`cpu.stat`、`memory.max` 和 `memory.current`。

两次 E2E 均未出现 `RESOURCE_PROBE_FAILED` 或 `RESOURCE_INSUFFICIENT`，无需切换环境重试。`-race` 未报告数据竞争。

## E2E 实际输出

普通运行与 `-race` 运行输出一致，各有 10 条任务日志、3 条跨 App 读取日志，最后一行均为 `E2E passed`。

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
