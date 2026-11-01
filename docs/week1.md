
# Week 1：网络与并发基础

环境：WSL2 + Ubuntu，客户端与服务端同机，hey8核，Go 1.27.2， ulimit -n 65535
QPS仅代表本机极限
    1. 资源单位是什么？thread-per-connection的资源单位是OS线程。goroutine的资源单位是用户态协程
    2. 阻塞是发生什么？ 不是不处理，而是占用了不返回。线程在Read()函数上挂起，不继续运动。goroutine在Read上阻塞（也可能在Write上阻塞）。
    3. 线程用一个链接一份栈换来编码简单。 event loop用编码复杂换来内存效率。（调用用户态协程在100ns级别，比陷入内核需要的10微秒更快。

Day1 
## 1.请求路径图
curl --TCP--> accept queue --ln.Accept()-> conn
conn --go.routine()perRequest-> ServerMux ---> requestID-Logging-recover-handler
handler-kv.Store(RWMutex) ---> WriterHeader + body-custom

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

结论：**TCP 层面一切正常，卡住的是应用层——没有第二条执行流去取 B。**Day3

## 3. 阻塞 I/O 与 goroutine

- 线程在 `Read` 上阻塞：内核挂起该线程，这个内核线程不能再干别的。
- goroutine 在 `Read` 上阻塞：runtime 把它 park，M（OS 线程）回到调度器继续跑其他 G，
  fd 交给 netpoller（Linux 上 epoll）。**数据到了再唤醒，代码看起来仍是同步的。**
- 切换成本：用户态协程 ~100ns 量级，陷入内核 ~10µs 量级。
- 1 万连接：线程模型 8MB 栈/个 → GB 级；goroutine 2KB 起 → 几十 MB。

一句话：**线程/goroutine 用"每连接一份栈"换编码简单，event loop 用"编码复杂"换内存效率；
goroutine 把前者的简单和后者的效率结合了起来。**

Day3
    1. 尝试压测：给healthz1000并发压测，给kv200并发压测。使用hey。结果健康。0错误，p99在p50的3倍之内（约两倍）
    2. 吞吐量封顶。在200并发吞吐就饱和，延迟和并发成线性关系
    3. p50的差异出自于排队。原始数据是
| 场景 | 并发 | QPS | p50 | p99 |
| --- | --- | --- | --- | --- |
| healthz | 1000 | ~121400 | 9.8m | 17.4m |
| PUT/kv/name | 200 | ~111000 | 1.8m | 4.1m |
| GET/kv/name | 200 | ~115900 | 1.7m | 3.9m |
    1000并发的healthz延迟大约是kv的5倍，延迟呈5倍，符合排队饱和的线性关系
    4.



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

    sem := make(chan struct{}, 100)
    select {
    case sem <- struct{}{}: defer func(){ <-sem }(); next.ServeHTTP(w, r)
    default: http.Error(w, "busy", http.StatusServiceUnavailable)
    }

- `select + default` = 立即拒绝（保服务、牺牲请求，错误率上升、p99 下降）；
- 去掉 `default` = 排队（保请求、牺牲延迟，p99 上升、错误率接近 0）。
- TODO（第 2 周）：实现并压测对照，填数字。

## 6. 本周踩坑

1. **`import "error"`**：`error` 是内建类型，不是包；要 `errors`（带 s）。
2. **`}func` 之间漏换行**：Go 靠换行插入分号，同一行会报 `expected ';'`。
3. **`undefined: greet`**：`go run .` 不编译 `*_test.go`，逻辑写进测试文件就会这样。
4. **两套中间件设计并存**：包级函数和方法混写，导致 `New redeclared`、`method undefined`。
   教训：中间件需要 Server 状态时统一用方法，一个包里同名声明只能有一份。
5. **`New` 漏了 `start: time.Now()`**：编译通过，但 `/stats` 的 uptime 变成 17 亿秒。
   教训：编译通过 ≠ 逻辑正确。
6. **`curl -s` 吞错误**：连不上时输出为空，误判成"服务返回空"。排错用 `-sS`。
7. **hey 输出的状态码计数不可信**（三组不同 QPS 都写 `1000000 responses`），
   用 `Total data ÷ 单响应字节` 交叉验证才发现。以后一律用 CSV 复核：
   `hey -o csv ... > a.csv && wc -l a.csv`。

## 7. 下周改进

- [ ] 实现信号量背压并压测对照（第 5 节 TODO）
- [ ] 日志改为分级/采样，压测时关闭请求日志，验证 QPS 天花板是否上移
- [ ] TODO：补一次压测期间的 `/stats` 采样，记录 goroutine 峰值（预期 ≈ 并发数）
- [ ] 第 2 周：HTTP 分层拆 handler / service / store
