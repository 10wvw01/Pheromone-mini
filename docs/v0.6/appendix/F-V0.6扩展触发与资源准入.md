# 附录 F：扩展触发与资源准入

本附录定义 V0.6 增量设计。原两个上游 API 和唯一 TryEnqueue 保持；不新增执行超时、Rerun、跨进程调度或资源预留。

## F.1 配置

全部写入 app.json。DIRECT、IMMEDIATE、BY_NAME 保留原语义。

| 类型 | 新字段（相对所在对象） | 约束 |
|---|---|---|
| dependencies[] MANY_TO_ONE | src_functions、tgt_functions | 至少两个不同来源、至少一个目标；全部引用当前 App 的 func_name |
| dependencies[] K_OUT_OF_N | src_functions、tgt_functions、description.k、description.n | 1 ≤ k ≤ n，n 必须等于不同来源的数量；投票由目标函数执行 |
| triggers[] BY_SET | primitive.function、primitive.key_set | 非空且不重复的 Key 列表 |
| triggers[] REDUNDANT | primitive.function、primitive.key_set、primitive.k、primitive.n | 1 ≤ k ≤ n，n 等于 key_set 长度 |
| triggers[] BY_BATCH_SIZE | primitive.function、primitive.count、primitive.time_window（可选，毫秒） | 正整数 count；达到条数立即触发，有窗口时到期提交非空余批 |
| triggers[] BY_TIME | primitive.function、primitive.time_window（毫秒） | 首条数据开启窗口，到期提交非空批 |
| triggers[] DYNAMIC_GROUP | primitive.function、primitive.control_group、primitive.data_group | 两个非空、内部无重复、互不重叠的 Key 列表；收齐两组后触发，只传 data_group |

新规则拒绝未知字段、空标识、非法引用和数值越界。毫秒使用附录 B 的换算边界；每个规则输入上限 4096 项。DIRECT 优先；没有 DIRECT 时对同一 App 的每条汇聚依赖和 Bucket Trigger 独立判断。汇聚依赖只计入合法非空 sourceFuncName；通知 Bucket 照常用于定位 App。依赖固定 Value；Bucket Trigger 对每项应用 trigger_option。

## F.2 状态、并发和数据地址

- 状态按 App / 规则 / SessionID 隔离，Bucket 规则还包含 Bucket。依赖按不同来源计数、Bucket 规则按不同 Key 计数；同组首次有效输入保留，重复不增加计数。收齐类 / 投票类每个 Session 每条规则至多触发一次；K-of-N 使用先到的 K 个不同来源。依赖列表 / Key 列表按配置顺序输出所选项；批处理按接收顺序。锁内原子更新和摘取，锁外构建与路由；多目标各有独立 Args 切片。
- 批次收满立即结束；窗口从首项开始，使用单调时钟，到期与新事件同时到达时先结束旧批。空批不触发。完成后同 Session 可开下一批；去重仅限同一批。BY_TIME 达 4096 项提前提交，避免无界聚集。
- 使用内部单调时钟管理到期及状态回收，测试可替换。非批次规则和无窗口的未满批从首项开始最多保留 60 秒，到期丢弃并记日志；已触发的单次规则保留去重标记至首项后 60 秒。SDK 必须保证同一业务 Session 的此类通知都落在这 60 秒内，过期 SessionID 不得复用或继续通知；否则无法用有限状态保证不重复触发。全局最多 4096 个活跃规则状态（含去重标记）；满时拒绝新状态，但允许已有状态继续；关闭时取消定时器并清理自有元数据。
- Args 每项仍为 Key、DataAddr、DataSize。普通触发保持一项；扩展触发传所选多项（K=1 或部分批可能只有一项）。保留地址、不复制业务数据。SDK 保证从首次通知直到触发等待、延时等待和异步消费全部结束期间地址有效；SDK 自行负责完成判定及回收。没有可靠有效期保障的接入不能启用新规则；Mock 保留所有映射至测试清理完成。

## F.3 资源准入

CPU / Memory 总是检查。GPU 声明 required=true 或任一 GPU 数值非零时检查（device_count=0 时按 1）；disk_mb、network_mbps 大于零时检查对应维度。新增可选顶层 scheduler.resource_probe；有磁盘需求必须填写 disk_path（已存在的绝对路径），有网络需求必须填写 network_interface、network_limit_mbps（有限正整数，部署方提供的有效带宽上限）。多个 App 可指定不同资源域，快照按资源域隔离，不混用。

| 维度 | 计算规则 |
|---|---|
| GPU | 从部署提供的设备遥测读取当前进程可见设备的稳定 ID 与可用显存；按 ID 去重。至少 device_count 个设备分别满足每卡 memory_mb（MiB），不跨卡加总。遥测适配器为模块内部依赖，不新增 Pool API |
| 磁盘 | Linux statfs(disk_path) 的可用块数 Bavail × 块大小，换算后比较 disk_mb（MiB）；路径必须落在实际任务使用的文件系统。仅反映可读到的文件系统可用量，不承诺用户配额或预留 |
| 网络 | 按配置网卡读取 RX/TX 字节累计计数，使用共享单调采样窗口；按十进制 Mbps，分别计算两个方向速率，使用两者最大值。可用量 = max(0, network_limit_mbps − max(RX Mbps, TX Mbps))，同需求比较 |

所有要求维度都满足才调用 TryEnqueue；相等通过。无请求维度不探测，无需 GPU 设备或网卡才能运行旧配置。按 §14.2 的共享采样与有效期处理，首次及每次重试重新判断；不可读、采样无效、计数回退或溢出记录 RESOURCE_PROBE_FAILED，可靠样本不足记录 RESOURCE_INSUFFICIENT（即样本有效、容量不够）。两者都沿用 enabled 和固定 deadline。缺静态配置、单位换算越界由 TriggerInit 拒绝；没有可用遥测不得按充足放行。准入不分配 GPU、不锁定磁盘或网络，也不改变 Pool 的执行权。

可将以下对象分别并入现有 app.json 的 dependencies、triggers 和 scheduler（函数与 Bucket 引用沿用对应 App 配置）：

~~~json
{
  "dependencies": [
    {"type":"K_OUT_OF_N","src_functions":["replica_a","replica_b","replica_c"],"tgt_functions":["vote"],"description":{"k":2,"n":3}}
  ],
  "triggers": [
    {"bucket":"b_shared","trigger_name":"t_batch","primitive_type":"BY_BATCH_SIZE","primitive":{"function":"processor","count":32,"time_window":100},"trigger_option":0}
  ],
  "scheduler": {
    "resource_probe":{"disk_path":"/var/lib/pheromone","network_interface":"eth0","network_limit_mbps":1000}
  }
}
~~~

这里的 scheduler 示例为新增子字段，原 delay_queue 仍必填。时间窗口表达的是收到首条数据后的成批等待，不引入无数据也定时运行的 PERIODIC 调度。

## F.4 增量验收

| 需求 | 最小验收用例 |
|---|---|
| AGG-01 汇聚 | TEST-AGG-01：两路不同来源收齐；重复、跨 Session、缺一路及 DIRECT 优先均不误触发 |
| AGG-02 投票 | TEST-AGG-02：3 选 2，重复来源不计数，第二个有效来源触发一次；验证 Args 和目标业务函数最终投票输出 |
| AGG-03 批处理 | TEST-AGG-03：满批、到期余批、空窗口、边界同时到达、下一批及同批去重；TEST-AGG-04：BY_SET、REDUNDANT、DYNAMIC_GROUP 的缺项和满足条件 |
| AGG-04 状态与地址 | TEST-AGG-05：并发最后两项仅触发一次，检查顺序、独立描述符、多地址、退出、状态满及过期；Mock 用受控时钟和持久 mmap |
| RTE-10 GPU | TEST-RTE-28：两卡分别满足；总量足但单卡不足；重复设备、不可见设备、无遥测及无需求 |
| RTE-11 磁盘 | TEST-RTE-29：有权限可用块、等于需求、路径非法、块乘法溢出及无需求 |
| RTE-12 网络 | TEST-RTE-30：RX/TX 双向、等于需求、计数回退、网卡不可用、缺上限及无需求 |
| RTE-13 多资源联合 | TEST-RTE-31：任一维度不足或探测失败不得提交；启用 / 关闭延时、恢复后提交一次、到期不提交、配置缺失初始化失败 |

先用原始样本、可控时钟和 Fake Pool 完成独立及 Mock E2E 验收，再用部署 GPU 遥测、目标文件系统和真实网卡验证采集语义。测试设计不代表已执行。
