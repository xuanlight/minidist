# minidist

用 Go 从零实现一个迷你分布式 KV 的学习项目。**当前进度：Week 1 / `v0.0-net`** —— 已打通「TCP echo → HTTP 服务 → 中间件 → 内存 KV → 压测观测」这条最小链路，为后续的 WAL、Raft、CLI 打地基。

## 环境要求

| 项 | 要求 | 说明 |
|---|---|---|
| Go | **1.22+** | 用到 `net/http` 的 `"GET /kv/{key}"` 路由与 `r.PathValue` |
| 系统 | Linux / WSL2 | 需要 `nc`、`ss`、`ulimit`；macOS 可跑但调试命令不同 |
| 可选 | `hey`、`nc` | 压测、手工验证 TCP |
| 建议 | `ulimit -n 65535` | 1000 并发会打开上千个 fd |

安装依赖（Ubuntu / WSL2）：

```bash
sudo apt update && sudo apt install -y build-essential git curl jq netcat-openbsd
go install github.com/rakyll/hey@latest
```

## 目录结构

```
minidist/
├── cmd/
│   ├── hello/        # 语法热身：flag 解析、退出码、单元测试
│   ├── echo/         # TCP echo：blocking / goroutine 两种模型
│   └── httphello/    # HTTP KV 服务（本周主角）
├── internal/
│   ├── kv/           # 并发安全的内存 KV（map + RWMutex）
│   └── httpapi/      # 路由、handler、中间件
├── docs/
│   └── week1.md      # 架构笔记、压测记录、踩坑复盘
├── go.mod
└── README.md
```

`cmd/` 下每个子目录是一个独立的可执行程序（目录名 = 二进制名）；`internal/` 下的包只能被本模块引用。

## 快速开始

```bash
git clone <repo> && cd minidist
go build ./...
```

### 1. hello：命令行热身

```bash
go run ./cmd/hello                 # hello, world
go run ./cmd/hello -name alice     # hello, alice
go run ./cmd/hello -name ""        # 报错，退出码 1
go run ./cmd/hello -h              # 自动生成的用法
```

### 2. echo：TCP 回显服务

```bash
go run ./cmd/echo -mode=blocking
go run ./cmd/echo -mode=goroutine
go run ./cmd/echo -addr=:9000 -mode=goroutine
```

验证（开两个终端，都执行 `nc localhost 9000`）：

- `-mode=blocking`：**第二个连接不响应**，要等第一个断开——这是「一次只能服务一个连接」的实证；
- `-mode=goroutine`：两个连接互不阻塞；
- 任一连接空闲超过 30 秒会被自动断开（读 deadline）。

观察队列（第三个终端）：

```bash
ss -ltn '( sport = :9000 )'                        # Recv-Q = 等待 accept 的连接数
ss -tn state established '( sport = :9000 )'       # 已建立连接；Recv-Q = 未被读取的字节数
```

### 3. httphello：HTTP KV 服务

```bash
go run ./cmd/httphello -addr=:8080
```

## 接口

所有响应都带 `X-Request-Id`（客户端传了就沿用，没传则生成）。

| 方法 | 路径 | 请求体 | 成功 | 失败 |
|---|---|---|---|---|
| `GET` | `/healthz` | — | `200` + `OK` | — |
| `PUT` | `/kv/{key}` | 原始字符串，≤ 1 MB | `204` | `413` 超限 / `400` 读取失败 |
| `GET` | `/kv/{key}` | — | `200` + value | `404` 不存在 |
| `DELETE` | `/kv/{key}` | — | `204` | `404` 不存在 |
| `GET` | `/stats` | — | `200` + JSON | — |
| 其他 | `/kv/{key}` | — | — | `405` + `Allow` 头 |

`/stats` 返回：

```json
{"goroutines":6,"requests":42,"keys":1,"uptime_seconds":128}
```

示例：

```bash
curl -i -s localhost:8080/healthz
curl -i -s -X PUT localhost:8080/kv/name -d 'alice'      # 204
curl -i -s localhost:8080/kv/name                        # 200 alice
curl -i -s -X DELETE localhost:8080/kv/name              # 204
curl -i -s localhost:8080/kv/name                        # 404
curl -i -s -H 'X-Request-ID: trace-001' localhost:8080/healthz   # 响应头透传
curl -i -s -X POST localhost:8080/kv/name                # 405 + Allow
```

### 中间件链

```
requestID → logging → recover → mux
```

- **requestID**：读 `X-Request-ID`，没有就生成，写回响应头并放进 `context`；
- **logging**：结构化 JSON 日志，含 method / path / status / duration_ms / request_id；
- **recover**：捕获 panic，记堆栈，返回 `500`。

顺序不能随意调整：logging 必须在 requestID 内层（才拿得到 ID），recover 必须在 logging 内层（panic 后 logging 仍能记到 500）。

### 优雅关闭

`Ctrl-C`（`SIGINT`）或 `SIGTERM` 后：停止 accept → 等在途请求（最多 5 秒）→ 超时则强制 `Close()`。

```bash
go build -o /tmp/hh ./cmd/httphello && /tmp/hh   # Ctrl-C 后应在 5 秒内退出
```

## 测试

```bash
go test ./...            # 单元测试
go test -race ./...      # 数据竞争检测
go vet ./...             # 静态检查
gofmt -l .               # 应无输出
```

覆盖范围：KV 的基本语义、handler 的正常/错误路径（PUT/GET/DELETE/404/405/413）、request ID 的生成与透传。

## 压测

```bash
ulimit -n 65535
go build -o /tmp/hh ./cmd/httphello && /tmp/hh -addr=:8080

hey -z 10s -c 1000 http://127.0.0.1:8080/healthz
hey -z 10s -c 200 -m PUT -d 'alice' -T text/plain http://127.0.0.1:8080/kv/name
hey -z 10s -c 200 http://127.0.0.1:8080/kv/name
```

参考结果（单机 WSL2，客户端与服务端同机，8 核，Go 1.27.2；**仅代表本机极限**）：

| 场景 | 并发 | QPS | p50 | p95 | p99 | 错误率 |
|---|---:|---:|---:|---:|---:|---:|
| healthz | 1000 | ~121,400 | 9.8 ms | 17.4 ms | 24.6 ms | 0% |
| PUT /kv/name | 200 | ~111,000 | 1.8 ms | 4.1 ms | 5.5 ms | 0% |
| GET /kv/name | 200 | ~115,900 | 1.7 ms | 3.9 ms | 5.3 ms | 0% |

结论摘要（详见 `docs/week1.md`）：

- 吞吐在 **~12 万 QPS** 封顶，与并发数基本无关；并发 ×5 只换来延迟 ×5 —— 典型饱和；
- 瓶颈不在 handler 或 KV 锁，而在每请求必经的**同步日志**（slog 编码 + 锁 + stdout）；
- 写锁不是热点：PUT 与 GET 吞吐仅差 4%。

## 已知限制

- **无持久化**：数据全在内存，进程退出即丢；
- **单节点**：没有复制、共识、故障转移；
- **无鉴权、无 TLS**：不要暴露到公网；
- **`/stats` 泄露内部信息**：仅限本机调试；
- **无背压**：goroutine 无上限，慢客户端或极端并发可能耗尽资源；
- **同步日志**：当前吞吐天花板由日志决定，非业务逻辑；
- **key 无 TTL / 命名空间**，value 上限 1 MB。

## 下一周计划

- HTTP 分层：把 handler / service / store 拆开，引入接口抽象；
- 增加 CLI 客户端（`cmd/minidist-cli`），走 HTTP 调用服务端；
- 实现信号量背压，并压测对比「排队 vs 立即 503」；
- 日志分级 / 采样，验证关闭请求日志后 QPS 天花板是否上移；
- 补齐压测期间的 `/stats` 采样（goroutine 峰值应 ≈ 并发数）。

## 约定

- 代码必须 `gofmt` 过；提交使用 Conventional Commits（`feat:` / `fix:` / `test:` / `docs:` / `chore:`）。
- 提交信息示例：`feat: add tcp echo with goroutine mode`、`test: handler tests and stats`。
