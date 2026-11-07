---
tags:
  - minidist
  - week2
  - http
created: 2026-11-07
---

# Week 2：HTTP 服务骨架（v0.1-http-kv）

> 环境：WSL2 + Ubuntu，8 核，Go 1.27.2，`ulimit -n 65535`，客户端与服务端同机。
> QPS 不代表生产指标：同机压测时 hey 与服务端抢 CPU，数字只是"联合极限"。

## 1. 分层的边界

```
cmd/httphello   →  httpapi  →  service  →  kv
   （装配）        （传输）     （业务）    （数据）
```

**判据是依赖方向，不是 public/private。** 一个类型导出与否，决定的是"包外能不能用"；分层决定的是"谁允许知道谁"。

三个"不该"：

| 层 | 不该出现 | 为什么 |
|---|---|---|
| handler | `len(key) > 128` 这类业务规则 | 规则要在 service 里，CLI / gRPC 入口才能复用 |
| service | `http.StatusNotFound` | 一出现就说明 `import "net/http"`，业务层从此依赖 HTTP |
| kv | JSON、状态码、request ID | 它是纯数据层，换成 WAL 后也不该知道外面的事 |

> 反例最值得记：如果哪天为了"方便"，在 `service` 里直接 `return http.StatusBadRequest`，那么第 5 周加 gRPC 入口时，业务层得同时认识两套协议。

## 2. 接口为什么定义在使用方

```go
// internal/service/service.go —— 接口在"使用方"
type Store interface {
	Put(key, value string)
	Get(key string) (string, bool)
	Delete(key string) bool
	Len() int
}
```

```
service ──依赖──► Store（接口）◄──隐式实现── kv.Store
```

`kv` 从没有声明"我实现 service.Store"，但方法签名对得上，编译器在 `service.New(store)` 那一刻才检查。

三个实际收益：

1. **测试替身**：`service_test.go` 里用 `fakeStore`（20 行 map）就能测全部业务规则，不用真存储；
2. **实现可替换**：第 3 周把 kv 换成 WAL 实现，只要方法签名不变，`service` 一行不用改；
3. **方向不反转**：`kv` 不 import `service`，编译期的依赖图上不会出现环。

**如果反过来把接口定义在 kv 包里**：`kv` 就得描述"别人需要我做什么"，而调用 `service.New` 时又要 kv 认识 service 的类型——依赖方向变成双向，测试替身也得实现 kv 包的接口，绕了一圈没有收益。

## 3. 错误模型

```json
{"error":{"code":"not_found","message":"get \"nope\": key not found","request_id":"3f9a..."}}
```

为什么不直接 `http.Error(w, "not found", 404)`：

| | 纯文本 | 带 code |
|---|---|---|
| 客户端判断 | 只能匹配字符串 | `switch code` 稳定分支 |
| 新增同类错误 | 改文案就破坏客户端 | 加 code 即可 |
| 日志聚合 | 按字符串分组，易碎 | 按 code 统计 |
| 文档 | 无契约 | code 列表就是契约 |

**`code` 是给客户端做决策的，`request_id` 才是用来溯源的**——两者别混。

重试决策（客户端视角）：

| code | 该不该重试 |
|---|---|
| `invalid_key` / `bad_json` / `too_large` | ❌ 重试无意义，改请求 |
| `not_found` | ❌ 除非业务上"等写入后再读" |
| `internal`（500） | ⚠️ 有限次重试 + 退避 + jitter |
| `draining`（503） | ✅ 换实例重试 |
| 超时 / 连接错误 | ✅ 仅幂等操作可重试 |

**500 不回显 `err.Error()`**：内部错误信息可能含路径、SQL、内存地址，只进日志。

## 4. 4xx 与 5xx 的分界

| 场景 | 状态码 | 归因 | 后果 |
|---|---|---|---|
| key 不存在 | 404 | 客户端 | 不告警 |
| body 不是合法 JSON | 400 | 客户端 | 不告警 |
| value 超过 1MB | 413 | 客户端 | 不告警 |
| 方法不支持 | 405 | 客户端 | 不告警 |
| 存储写入失败 | 500 | **服务端** | 告警 |
| panic | 500 | **服务端** | 告警 + 堆栈 |
| 正在关停 | 503 | 服务端（计划内） | 不告警，但要能从 LB 摘除 |

**分错的代价**：

- 把客户端错误报成 5xx → 告警风暴，真正的故障被淹没；
- 把服务端错误报成 4xx → 客户端以为自己的问题，反复重试，故障没人发现。

## 5. 两种超时的关系

| 手段 | 在哪一层 | 谁执行 | 超时后发生什么 | 局限 |
|---|---|---|---|---|
| `context.WithTimeout` | 中间件 → handler | **你的代码** | `ctx.Done()` 关闭，handler 自己返回 | 不检查就完全无效 |
| `http.TimeoutHandler` | 包裹 handler | 标准库强制 | 直接回 503 | 缓冲整个响应，大响应用不了 |
| `Server.WriteTimeout` | 连接层 | net/http | 断连接 | 客户端只看到连接错误，没有结构化响应 |

三者是**叠加**关系，不是替代。

本项目的实现（`middleware.go`）：

```go
ctx, cancel := context.WithTimeout(r.Context(), s.requestTimeout)
defer cancel()
next.ServeHTTP(w, r.WithContext(ctx))
```

- 用 `r.Context()` 当父，是为了**继承上游的取消**（客户端断开、服务关停）；
- `defer cancel()` 释放定时器，`go vet` 的 `lostcancel` 会检查；
- `r.WithContext(ctx)` 返回**新的请求对象**，必须传给 `next`，写成 `next.ServeHTTP(w, r)` 就等于没加。

**证据**：`TestTimeoutCancelsContext` 里 handler 阻塞在 `<-r.Context().Done()`，50ms 后返回——说明信号确实传下去了。但反过来说，**如果 handler 不检查 ctx，超时不会中断任何东西**。

## 6. liveness vs readiness

| 端点 | 语义 | 失败时 | 关停时 |
|---|---|---|---|
| `/healthz` | 进程活着 | 容器被**重启** | 仍返回 200 |
| `/readyz` | 可以接流量 | 从负载均衡**摘除** | 返回 503 `draining` |

**`/healthz` 里绝不能查依赖**：数据库抖一下 → liveness 失败 → 容器重启 → 冷启动更慢 → 雪崩。依赖健康属于 readiness。

关停顺序：

```
SIGTERM
   │
   ├─ api.SetDraining()      readyz → 503
   ├─ sleep 2s               ← 等 LB 把流量摘掉（本地用 sleep 模拟）
   ├─ srv.Shutdown(5s)       停止 accept，等在途请求
   └─ 超时 → srv.Close()      强杀剩余连接
```

为什么不能直接 `Shutdown`：`Shutdown` 是"立即停止接收新连接"。在真实集群里，LB 需要几百毫秒到几秒才发现实例不可用——这段窗口里新请求仍会被打过来，然后连接被拒。`drain → wait → shutdown` 就是为了把这段窗口吃掉。

## 7. API 契约变更（v0.0-net → v0.1-http-kv）

| | v0.0-net | v0.1-http-kv |
|---|---|---|
| 路径 | `/kv/{key}` | `/v1/kv/{key}` |
| PUT body | 原始字符串 | `{"value":"..."}` |
| GET 响应 | 原始字符串 | `{"key":"...","value":"..."}` |
| 错误响应 | 纯文本 | `{"error":{"code","message","request_id"}}` |

这次改动波及：`server.go`、`server_test.go`（9 条矩阵全部重写）、`main.go`、`README.md`、`docs/week1.md` 的 curl 示例。

> 教训：**接口是契约。** 改一次要同步改五处，这就是为什么 `/v1` 这种版本前缀从第一天就该有。

## 8. 测试

| 包 | 覆盖率 |
|---|---|
| `internal/httpapi` | 93.2% |
| `internal/config` | 88.2% |
| `internal/service` | 88.0% |
| `internal/kv` | 82.4% |
| `cmd/*` | 0.0% |

**`cmd/*` 的 0% 是合理的**：`main` 里全是装配代码（读配置、建对象、起 goroutine），单测它等于测"Go 语法对不对"。真要测，得把 `main` 拆成 `run() error` —— 记进第 3 周的改进。

**覆盖率 ≠ 测过**：`withRecover` 一度只有 50%，因为它每次请求都被调用（覆盖率不为 0），但 `recover` 那半边从没执行过。补一个"handler 主动 panic"的用例才覆盖到。同理 `withLogging` 缺的是"handler 不写状态码"的兜底分支。

## 9. 压测与瓶颈定位

| 场景 | 并发 | QPS | p50 | p99 | 错误率 |
|---|---:|---:|---:|---:|---:|
| healthz | 1000 | 216,244 | 2.2 ms | 25.0 ms | 0% |
| PUT /v1/kv/name | 200 | 187,853 | 0.5 ms | 6.0 ms | 0% |
| GET /v1/kv/name | 200 | 208,135 | 0.5 ms | 4.7 ms | 0% |

**与第 1 周不可直接比较**：第 1 周的进程在前台终端运行、日志写终端；本轮日志重定向到文件。

同一份代码的对照实验（healthz 1000c）：

| 日志去向 | QPS | p50 | p99 |
|---|---:|---:|---:|
| 终端 | 118,672 | 9.9 ms | 25.7 ms |
| 文件 | 213,668 | 2.1 ms | 25.6 ms |
| 关闭 | 214,995 | 1.9 ms | 25.6 ms |

三个结论：

1. **第 1 周的 121k 是终端 I/O 决定的**，与本轮代码差异无关（终端 118,672 vs 第 1 周 121,400，差 2%）；
2. **日志成本落在 p50，不落在 p99**（9.9ms vs 2.1ms）。写日志是每请求的固定开销；p99 由排队和调度抖动决定，被淹没了。→ 以后 **p50 变差 p99 不变 = 每请求固定成本**；**p99 变差 p50 不变 = 偶发事件**；
3. **关掉日志 QPS 不涨**，说明 215k 仍不是服务端极限，瓶颈已经转移到 hey 与内核。

可跨版本比较的唯一信号：**PUT 与 GET 的 QPS 差距从 4% 扩大到 10%**，方向符合"PUT 多了 JSON 解码 + key 校验"的预期。

## 10. 本周踩坑

| 现象 | 原因 | 以后怎么防 |
|---|---|---|
| `package minidist/internal/flag is not in std` | 编辑器补全塞进来的幻觉 import | 看到 `minidist/internal/xxx` 先 `ls internal/` |
| `undefined: Logger` | 定义的是 `logger`，Go 大小写敏感 | 变量名一律小写驼峰；报错行就是使用点 |
| `cfg.requestTimeout undefined` | 跨包字段必须**导出** | 编译器提示里已经写了"but does have field RequestTimeout" |
| `no new variables on left side of :=` | 同一作用域重复声明 | 粘贴代码后看一遍变量名 |
| `undefined: bytes / time` | 用了包但没 import | 错误里带包名 = 缺 import |
| 文件里出现两份 `type Server` / `func New` | 把带 `// ----`、`// 空1` 注释的**草稿骨架**整段粘进文件 | 草稿永远不整体粘贴，只摘需要的函数 |
| `gofmt -l` 反复列出文件 | `-l` 只列不改 | `-w` 才写；`-d` 看差异 |

## 11. 下周（WAL）要改什么

`service.Store.Put` 现在签名是 `Put(key, value string)`，写 WAL 之后必须变成：

```go
Put(key, value string) error
```

连锁反应：

| 文件 | 改什么 |
|---|---|
| `internal/kv/store.go` | `Put` 返回 error（写日志/刷盘失败） |
| `internal/service/service.go` | `Store` 接口 + `Service.Put` 向上传递 |
| `service_test.go` | `fakeStore.Put` 签名 + 新增"存储失败"用例 |
| `internal/httpapi/server.go` | `handlePut` 多一个 500 分支 |

**这也是一个 ADR 素材**：接口一旦发布，改动会沿依赖链传播——所以第 2 周就把它定义在使用方，把影响面压到最小。

## 12. 复盘

1. **分层之后最明显的变化**：`handlePut` 从 20 行（读 body、判大小、写 store）缩到 12 行，只剩"解码 + 调 service + 映射错误"，业务判断全在 `service.Put`。
2. **破坏性变更的成本**：改了 5 个文件（server / server_test / main / README / week1 文档），其中测试重写了 9 条。
3. **加 `GET /v1/kv?prefix=` 够不够用**：够。service 加 `Scan(prefix)`，kv 加迭代方法，handler 只做参数解析——三层各加一个函数，不用动结构。
4. **最花时间的事**：把草稿骨架当成代码粘贴，导致重复声明和半截语法，来回修了三轮。下次先把草稿写在注释里，再逐个函数搬。
