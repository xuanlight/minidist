# Week 1：网络与并发基础（v0.0-net）

> 环境：WSL2 + Ubuntu，8 核，Go 1.27.2，`ulimit -n 65535`，客户端与服务端同机。
> 注意：同机压测时 hey 与服务端共享 CPU，QPS 仅代表本机极限，不是生产指标。

## 1. 请求路径图

```
curl
  │ TCP 三次握手（内核完成）
  ▼
accept 队列 ──► ln.Accept() ──► conn
                                   │
                                   ▼  每条连接一个 goroutine（net/http 内部）
                           ServeMux 路由
                                   │
                                   ▼  洋葱模型
                     requestID → logging → recover → handler
                                   │
                                   ▼
                           handler ──► kv.Store（RWMutex）
                                   │
                                   ▼
                     WriteHeader + body ──► 客户端
```

## 2. 两种并发模型对比

| | blocking | goroutine-per-connection |
|---|---|---|
| Accept 之后 | 直接 `handle(conn)` | `go handle(conn)` |
| 第二条连接 | 已握手，但躺在内核 accept 队列里等 | 立刻被服务 |
| 资源单位 | 一个执行流 | 每条连接一个 goroutine（2KB 栈起） |
| 主要代价 | 队头阻塞：一个空闲客户端可拖垮整个服务 | goroutine 无上限；共享状态需同步 |

**实测证据（练习 2）**：

- `ss -ltn` 的 `Recv-Q = 1`：有一条连接已完成握手、等待 accept；
- `ss -tn state established` 能看到 **2 条** ESTABLISHED，而服务端只 accept 了 1 条；
- B 发送数据后其连接 `Recv-Q > 0`：字节已到内核缓冲区，但没有应用代码去读。

结论：**TCP 层面一切正常，卡住的是应用层——没有第二条执行流去取 B。**

## 3. 阻塞 I/O 与 goroutine

- 线程在 `Read` 上阻塞：内核挂起该线程，这个内核线程不能再干别的。
- goroutine 在 `Read` 上阻塞：runtime 把它 park，M（OS 线程）回到调度器继续跑其他 G，
  fd 交给 netpoller（Linux 上 epoll）。**数据到了再唤醒，代码看起来仍是同步的。**
- 切换成本：用户态协程 ~100ns 量级，陷入内核 ~10µs 量级。
- 1 万连接：线程模型 8MB 栈/个 → GB 级；goroutine 2KB 起 → 几十 MB。

一句话：**线程/goroutine 用「每连接一份栈」换编码简单，event loop 用「编码复杂」换内存效率；
goroutine 把前者的简单和后者的效率结合了起来。**

## 4. 压测结果

| 场景 | 并发 | QPS | p50 | p95 | p99 | 错误率 |
|---|---:|---:|---:|---:|---:|---:|
| healthz | 1000 | ~121,400 | 9.8 ms | 17.4 ms | 24.6 ms | 0% |
| PUT /kv/name | 200 | ~111,000 | 1.8 ms | 4.1 ms | 5.5 ms | 0% |
| GET /kv/name | 200 | ~115,900 | 1.7 ms | 3.9 ms | 5.3 ms | 0% |

（同一配置连跑两次，QPS 波动 ±8%，因此小于 20% 的差异不视为有效改进。）

**结论一：吞吐封顶，且与并发无关。**
并发从 200 提到 1000（×5），QPS 只从 ~111k 涨到 ~121k（+9%），p50 却从 1.8ms 涨到 9.8ms（×5.4）。
这是饱和的典型特征。**healthz 什么都不做也照样封顶在 10 万量级**，说明瓶颈不在 handler、
不在 KV 的锁，而在每个请求都要走的公共路径（日志：slog 编码 + 互斥锁 + 写 stdout）。

**结论二：延迟差异来自排队，不是处理慢。**
Little's Law：并发 ≈ QPS × 延迟

- healthz 1000c：1000 ÷ 121,400 ≈ 8.2ms ≈ 实测 p50 9.8ms
- KV 200c：200 ÷ 111,000 ≈ 1.8ms = 实测 p50 1.8ms

**结论三：写锁不是瓶颈。**
PUT 111k vs GET 116k，仅差 4%。所有 PUT 打同一个 key，写锁本应严格串行，
但和日志开销比可以忽略——先测量，再优化。

## 5. 背压

本周未实现信号量限流。记录设计思路与预期：

```go
sem := make(chan struct{}, 100)

func limit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case sem <- struct{}{}:
			defer func() { <-sem }()
			next.ServeHTTP(w, r)
		default:
			http.Error(w, "busy", http.StatusServiceUnavailable)
		}
	})
}
```

- `select + default` = 立即拒绝（保服务、牺牲请求，错误率上升、p99 下降）；
- 去掉 `default` = 排队（保请求、牺牲延迟，p99 上升、错误率接近 0）。

TODO（第 2 周）：实现并压测对照，填数字。

## 6. 本周踩坑

1. **`import "error"`**：`error` 是内建类型，不是包；要 `errors`（带 s）。
2. **`}func` 之间漏换行**：Go 靠换行插入分号，同一行会报 `expected ';'`。
3. **`undefined: greet`**：`go run .` 不编译 `*_test.go`，逻辑写进测试文件就会这样。
4. **两套中间件设计并存**：包级函数与方法混写，导致 `New redeclared`、`method undefined`。
   教训：中间件需要 Server 状态时统一用方法，一个包里同名声明只能有一份。
5. **`New` 漏了 `start: time.Now()`**：编译通过，但 `/stats` 的 uptime 变成 17 亿秒。
   教训：编译通过 ≠ 逻辑正确。
6. **`curl -s` 吞错误**：连不上时输出为空，误判成「服务返回空」。排错用 `-sS`。
7. **hey 输出的状态码计数不可信**（三组不同 QPS 都写 `1000000 responses`），
   用 `Total data ÷ 单响应字节` 交叉验证才发现。以后一律用 CSV 复核：
   `hey -o csv ... > a.csv && wc -l a.csv`。

## 7. 下周改进

- [ ] 实现信号量背压并压测对照（第 5 节 TODO）
- [ ] 日志改为分级/采样，压测时关闭请求日志，验证 QPS 天花板是否上移
- [ ] 补一次压测期间的 `/stats` 采样，记录 goroutine 峰值（预期 ≈ 并发数）
- [ ] 第 2 周：HTTP 分层拆 handler / service / store
