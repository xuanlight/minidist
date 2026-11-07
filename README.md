# minidist

用 Go 从零实现一个迷你分布式 KV 的学习项目。**当前进度：Week 2 / `v0.1-http-kv`** —— 已完成 TCP echo、HTTP 服务骨架、`handler → service → store` 分层、统一错误模型、配置化与优雅关停；下一步是 WAL 持久化。

## 环境要求

| 项 | 要求 | 说明 |
|---|---|---|
| Go | **1.22+** | 用到 `net/http` 的 `"PUT /v1/kv/{key}"` 路由与 `r.PathValue` |
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
│   └── httphello/    # HTTP KV 服务（装配点：config → store → service → httpapi）
├── internal/
│   ├── kv/           # 数据层：map + RWMutex，不知道 HTTP / JSON
│   ├── service/      # 业务层：校验、领域错误；接口定义在这里（使用方）
│   ├── httpapi/      # 传输层：路由、中间件、JSON 编解码、状态码映射
│   └── config/       # 配置：flag + env + 校验
├── docs/
│   ├── week1.md      # 第 1 周：网络与并发
│   ├── week2.md      # 第 2 周：分层、错误模型、压测
│   └── adr/
│       └── 0001-http-json-first.md
├── go.mod
└── README.md
```

依赖方向单向：`cmd → httpapi → service → kv`。`kv` 不 import `service`，`service` 不 import `net/http`。

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
```

### 2. echo：TCP 回显服务

```bash
go run ./cmd/echo -mode=blocking
go run ./cmd/echo -mode=goroutine
```

验证（两个终端各跑 `nc localhost 9000`）：

- `-mode=blocking`：**第二个连接不响应**，要等第一个断开——「一次只能服务一个连接」的实证；
- `-mode=goroutine`：两个连接互不阻塞；
- 空闲超过 30 秒自动断开（读 deadline）。

### 3. httphello：HTTP KV 服务

```bash
go run ./cmd/httphello -addr=:8080
```

## 配置

优先级：**flag > 环境变量 > 默认值**。

| 配置 | flag | 环境变量 | 默认 |
|---|---|---|---|
| 监听地址 | `-addr` | `MINIDIST_ADDR` | `:8080` |
| 日志级别 | `-log-level` | `MINIDIST_LOG_LEVEL` | `info` |
| 请求超时 | `-request-timeout` | — | `3s` |
| 关停上限 | `-shutdown-grace` | — | `5s` |

```bash
MINIDIST_ADDR=:9100 go run ./cmd/httphello                    # env
MINIDIST_ADDR=:9100 go run ./cmd/httphello -addr=:9200        # flag 覆盖 env
go run ./cmd/httphello -log-level=error                       # 减少日志 I/O
```

配置非法时以退出码 `2` 结束，不会带着错误配置启动。

## 接口

所有响应都带 `X-Request-Id`（客户端传了就沿用，没传则生成）。

| 方法 | 路径 | 请求体 | 成功 | 失败 |
|---|---|---|---|---|
| `GET` | `/healthz` | — | `200` + `OK` | — |
| `GET` | `/readyz` | — | `200` + `ready` | 关停中 `503` `draining` |
| `PUT` | `/v1/kv/{key}` | `{"value":"..."}`，value ≤ 1 MB | `204` | `400` / `413` |
| `GET` | `/v1/kv/{key}` | — | `200` + `{"key","value"}` | `404` |
| `DELETE` | `/v1/kv/{key}` | — | `204` | `404` |
| `GET` | `/stats` | — | `200` + JSON | — |
| 其他 | `/v1/kv/{key}` | — | — | `405` + `Allow` 头 |

错误体统一：

```json
{"error":{"code":"not_found","message":"get \"nope\": key not found","request_id":"3f9a..."}}
```

| `code` | 状态码 | 含义 |
|---|---|---|
| `not_found` | 404 | key 不存在 |
| `invalid_key` | 400 | key 为空 / 超长 / 含非法字符 |
| `bad_json` | 400 | 请求体不是合法 JSON，或有未知字段 |
| `too_large` | 413 | body 超过读取上限，或 value 超过 1 MB |
| `draining` | 503 | 服务正在关停 |
| `internal` | 500 | panic 或未知错误 |

示例：

```bash
curl -i -s localhost:8080/healthz
curl -i -s -X PUT localhost:8080/v1/kv/name \
     -H 'Content-Type: application/json' -d '{"value":"alice"}'     # 204
curl -i -s localhost:8080/v1/kv/name                                # 200 {"key":"name","value":"alice"}
curl -i -s -X DELETE localhost:8080/v1/kv/name                      # 204
curl -i -s localhost:8080/v1/kv/name                                # 404 not_found
curl -i -s -X PUT localhost:8080/v1/kv/name -d 'not-json'           # 400 bad_json
curl -i -s -X POST localhost:8080/v1/kv/name                        # 405
curl -i -s -H 'X-Request-ID: trace-001' localhost:8080/healthz      # 响应头透传
```

### 中间件链

```
requestID → logging → timeout → recover → mux
```

- **requestID**：读 `X-Request-ID`，没有就生成，写回响应头并放进 `context`；
- **logging**：JSON 日志，含 method / path / status / duration_ms / request_id；
- **timeout**：为每个请求派生带超时的 `context`（默认 3s）；
- **recover**：捕获 panic，记堆栈，返回 `500`。

顺序有讲究：logging 在 requestID 内层才拿得到 ID；recover 最贴 handler，panic 后 logging 仍能记到 500。

### 优雅关闭

`Ctrl-C`（`SIGINT`）或 `SIGTERM` 后：

1. `SetDraining()` —— `/readyz` 立即返回 503，让负载均衡把流量摘走；
2. 等待 2 秒（本地模拟摘流量窗口）；
3. `Shutdown(5s)` —— 停止 accept，等在途请求；
4. 超时则 `Close()` 强制断开。

`/healthz` 全程返回 200：它只表示“进程活着”，不代表“可以接流量”。

## 测试

```bash
go test ./...            # 单元测试
go test -race ./...      # 数据竞争检测
go vet ./...             # 静态检查
gofmt -l .               # 应无输出
go test ./... -cover     # 覆盖率
```

| 包 | 覆盖率 |
|---|---|
| `internal/httpapi` | 93.2% |
| `internal/config` | 88.2% |
| `internal/service` | 88.0% |
| `internal/kv` | 82.4% |
| `cmd/*` | 0%（只做装配，靠手工验收） |

覆盖内容：状态码矩阵（9 条）、PUT/GET/DELETE 主流程、request ID 生成与透传、panic → 500、超时取消、readiness 切换、配置优先级与校验。

## 压测

```bash
ulimit -n 65535
go build -o /tmp/hh ./cmd/httphello
/tmp/hh -addr=:8080 2>/tmp/server.log        # 日志重定向，否则终端 I/O 会拖垮吞吐

hey -z 10s -c 1000 http://127.0.0.1:8080/healthz
hey -z 10s -c 200 -m PUT -H 'Content-Type: application/json' \
     -d '{"value":"alice"}' http://127.0.0.1:8080/v1/kv/name
hey -z 10s -c 200 http://127.0.0.1:8080/v1/kv/name
```

参考结果（单机 WSL2，客户端与服务端同机，8 核，Go 1.27.2；**仅代表本机极限**）：

| 场景 | 并发 | QPS | p50 | p99 | 错误率 |
|---|---:|---:|---:|---:|---:|
| healthz | 1000 | 216,244 | 2.2 ms | 25.0 ms | 0% |
| PUT /v1/kv/name | 200 | 187,853 | 0.5 ms | 6.0 ms | 0% |
| GET /v1/kv/name | 200 | 208,135 | 0.5 ms | 4.7 ms | 0% |

**日志去向对吞吐影响巨大**（healthz 1000c）：

| 日志去向 | QPS | p50 |
|---|---:|---:|
| 终端 | 118,672 | 9.9 ms |
| 文件 | 213,668 | 2.1 ms |
| 关闭 | 214,995 | 1.9 ms |

结论摘要（详见 `docs/week2.md`）：

- 压测时**务必把日志重定向**，写终端会让吞吐掉一半；
- 关掉日志 QPS 不再上涨，说明当前瓶颈在压测客户端与内核，服务端仍有富余；
- 日志成本体现在 **p50**（每请求固定开销），不影响 p99；
- PUT 比 GET 低约 10%，来自请求体 JSON 解码与 key 校验。

## 已知限制

- **无持久化**：数据全在内存，进程退出即丢；
- **单节点**：没有复制、共识、故障转移；
- **无鉴权、无 TLS**：不要暴露到公网；
- **`/stats` 泄露内部信息**：仅限本机调试；
- **无背压**：goroutine 无上限，慢客户端或极端并发可能耗尽资源；
- **API 已版本化为 `/v1`**：破坏性变更通过版本前缀引入，旧版本不保证兼容；
- **key 无 TTL / 命名空间**，value 上限 1 MB。

## 下一周（第 3 周：WAL）

- WAL 记录格式：magic / version / length / crc32 / op / key / value；
- `fsync` 策略（每条 / 批量 / 周期）与吞吐测量；
- 启动 replay、处理半截记录、全量快照；
- `kill -9` 恢复测试：已确认的写不丢；
- `service.Store.Put` 改为返回 `error`，打通"写盘失败"路径；
- ADR-0002：WAL 格式与 fsync 取舍。

## 约定

- 代码必须 `gofmt` 过；提交使用 Conventional Commits（`feat:` / `fix:` / `test:` / `docs:` / `chore:`）。
- 打 tag 前检查：`gofmt -l .` 为空、`go test -race ./...` 全绿、README 与接口一致。

## License

MIT © 2026 lejourenfeu
