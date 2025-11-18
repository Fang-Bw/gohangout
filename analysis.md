# 一.说明
- gohangout 是一个声明式的流处理管道：Inputs → Filters (Processors) → Outputs，以 worker（并发 goroutine）为单位执行，每个 worker 拥有自己的 filter/output 实例；支持插件扩展、配置热加载、prometheus/pprof 监控

- 架构

```
                           +---------------------+
                           |     CLI / main      |
                           |  (gohangout.go)     |
                           +----------+----------+
                                      |
                       load config / topology / plugins
                                      |
               +----------------------+---------------------+
               |                                            |
         +-----v-----+                                +-----v-----+
         |  Manager  |                                |  Metrics  |
         | (topology)|                                |  / pprof  |
         +-----+-----+                                +-----------+
               |
   spawn N workers (--worker arg, default 1)
               |
    +----------+----------+   ...   +----------+----------+
    | Worker 1 (goroutine) |        | Worker N (goroutine)|
    |  - Inputs (instance) |        |  - Inputs (instance) |
    |  - Filters pipeline  |        |  - Filters pipeline  |
    |  - Outputs (instance)|        |  - Outputs (instance)|
    +----------+----------+        +----------+----------+
               |                             |
               |      message (map/json)     |
               +------------>processing------+

╭────────────────────────────────────────────────────────────────────────────╮
│ 每个 Worker 内部结构                                                       │
│                                                                            │
│    +-------------------+         +--------------------+         +--------+ │
│    |    InputBox       |         |   Filter Pipeline  |         | Output | │
│    |  (per input cfg)  |  --->   |  FilterBox 列表    |  --->   |  集合  | │
│    +---------+---------+         +----+----+----+-----+         +---+----+ │
│              |                        |    |    |                   |      │
│    ReadOneEvent()                     v    v    v                   v      │
│              |                     Filter1 Filter2 ...            Emit()   │
│            event                                                             │
╰────────────────────────────────────────────────────────────────────────────╯

╭────────────────────────────────────────────────────────────────────────────╮
│ InputBox 拓扑构建流程                                                      │
│                                                                            │
│  config.yaml                                                               │
│       |                                                                    │
│       v                                                                    │
│  buildPluginLink()                                                         │
│       |  (遍历 inputs 列表)                                                │
│       v                                                                    │
│  input.NewInputBox()  ──────────►  buildTopology()                         │
│                                      │                                     │
│                                      ├─ BuildFilterBoxes() → FilterBox*    │
│                                      └─ BuildOutputs()     → OutputBox*    │
│                                                                            │
│  结果：FilterBox/OutputBox 串成 Processor 链，由 InputBox 驱动              │
╰────────────────────────────────────────────────────────────────────────────╯

╭────────────────────────────────────────────────────────────────────────────╮
│ Prometheus / pprof 侧链                                                    │
│                                                                            │
│  --prometheus addr  ──► HTTP /metrics ──► Prometheus Server                │
│                                                                            │
│  --pprof + addr      ──► net/http/pprof 提供 profiling 接口                │
│                                                                            │
│  Input/Filter/Output 可选 prometheus_counter 配置，调用时计数。           │
╰────────────────────────────────────────────────────────────────────────────╯

```

# 二、短板
### 架构可能的短板

- **Filter/Output 串行路径**：同一 worker 内的 Filters 与 Outputs 串行执行，虽然逻辑清晰，但在处理耗时 Filter（如重正则）或慢 Output（如 ES Bulk）时会放大长尾，无法利用多核并行同一条数据的后半段处理。
- **每 worker 独立实例**：为隔离状态，每个 worker 拥有完整的 Filter/Output 实例，但这会导致高内存占用（多份配置、字典、连接池），在 filter/output 众多或 worker 数很高时，资源成本陡升。
- **缺乏采样与背压控制**：Input 读取后直接进入流水线，缺少全局队列或背压机制，链路中下游阻塞时只能依赖 worker goroutine 自旋等待，没有显式限速/丢弃策略，容易在突发流量下堆积或崩溃。
- **多 Output 是串行写**：当配置多个 Output 时，事件按顺序写出，某个 Output 变慢会拖累其他 Output，缺乏异步/缓冲隔离；且没有 per-output 的重试/失败隔离通道。
- **插件生命周期管理分散**：Input/Filter/Output 插件只通过接口约定来构造与关闭，缺少统一的资源管理器；第三方插件若未正确实现 Shutdown，容易造成 goroutine/连接泄漏。
- **配置结构扁平**：所有 Input/Filter/Output 公用同一 config map，依赖运行时断言，缺乏强类型 + 构建期校验；大配置文件难以模块化复用，也不便于做细粒度的版本管控。
- **监控粒度有限**：Prometheus counter 需要手动配置，且只支持简单累加；缺少延迟、失败率、队列长度等关键指标，难以迅速定位性能瓶颈。
- **错误处理弱**：Filter 返回 `(event,bool)` 但 bool 只在 PostProcess 用于 add/remove/ failTag，整体缺乏错误传播机制；Output 失败只简单日志，缺少统一重试与告警策略。
- **测试/调试成本高**：配置驱动 + 插件式架构导致某些行为需要运行期才能观察，缺少内建的 Dry-run、回放、断言工具，定位问题时需手动构造事件、检查日志。