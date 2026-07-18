---
name: fixing-trace-id-logs
description: Use when Go service logs, HTTP access logs, business logs, SQL logs, async/MQ logs, or downstream client logs are missing trace_id, span_id, traceparent, Log-ID, or cannot be correlated in the log platform. Applies by framework pattern: Kratos, Gin, Echo, GORM/gen, resty, seelog, go-logger, cron, MQ, and lightweight Go services.
---

# 修复 Go 服务日志 trace_id 丢失

## 核心原则

链路串联字段以 `trace_id` 为主，`span_id` 用于定位具体 span，`log_id` 只作为访问日志辅助字段。修复时不要手工生成随机 `trace_id`，也不要只在日志文本里拼一个 `TraceID` 假装完成；应保证同一个 `context.Context` 从入口一路传到业务日志、数据库调用、下游调用、事务、批量任务和异步任务。

如果当前使用的 logger、ORM/DAO、HTTP client 或中间件封装没有 `WithContext(ctx)`、`SetContext(ctx)` 或等价的 ctx 传播能力，不要在业务代码里临时拼接 trace 字段绕过；应提示升级对应包版本，或切换到项目内已支持 ctx 传播的封装。

字段命名要兼容服务现状：

- 常见字段：`trace_id`、`span_id`
- 部分历史服务可能使用 `trace.id`、`span.id`
- HTTP 传播头以 `traceparent` 为准；`Log-ID`/`log_id` 不是主链路字段。

## 先识别服务类型

开始修改前先看 `go.mod`、入口文件和 server/middleware 文件，按框架选择检查路径：

| 框架/形态 | 识别方式 | 常见入口/关键文件 | 链路入口重点 |
|---|---|---|---|
| Kratos HTTP/gRPC | 依赖 `go-kratos/kratos`，入口有 `kratos.New` | `cmd/**/main.go`、`internal/server/http.go`、`internal/server/grpc.go`、`internal/data/**client*.go`、`internal/data/**/rpc/**`、`internal/data/**/thirdparty/**` | `tracingcommon.Init`、Kratos logger 注入 trace valuer、`tracing.Server()`、`tracing.Client()` |
| Gin HTTP | 依赖 `gin-gonic/gin`，入口有 `gin.New`/`gin.Default` | `app.go`、`main.go`、`middleware/**`、`routes/**`、`util/resty.go` | `tracing/common.Init`、`tracing/gin.EnableTrace()`、go-logger Gin middleware、resty `SetContext(ctx)` |
| Echo HTTP | 依赖 `labstack/echo`，入口有 `echo.New` | `main.go`、`middleware/**`、`router/**`、`common/resty.go` | `tracing/echo.EnableTrace()`、go-logger Echo middleware、从 `c.Request().Context()` 传递 ctx |
| 任务/cron/MQ/consumer | 有 cron、MQ consumer、离线任务或轻量 `main.go` | `internal/server/*consumer*.go`、`internal/server/*cron*.go`、`task/**`、`mq/**`、`main.go` | 有上游就提取并传递 trace；无上游就开启独立链路，不伪造请求 trace |

如果多个框架同时出现，以真正对外提供 HTTP/gRPC 的入口为主；定时任务、MQ、后台 goroutine 另外按异步链路处理。

## 入口检查

### Kratos 服务

优先检查：

- `cmd/main/main.go` 或实际入口：应初始化 `tracingcommon.Init(...)`，并在 Kratos logger 上注入 trace 字段。
- `internal/server/http.go`：HTTP middleware 应包含 `tracing.Server()` 和 `go-logger` 的 Kratos middleware。
- `internal/server/grpc.go`：gRPC middleware 应包含 `tracing.Server()` 和日志 middleware。
- `internal/server/http.go`、`internal/server/grpc.go`：Kratos middleware 必须加载 `recovery.Recovery(...)`，并带 `recovery.WithHandler(...)` 打印 `debug.Stack()`；`recovery` 顺序必须在 `tracing.Server()`、日志 middleware 之后，确保 panic 日志也能带上链路字段。
- `internal/data/client.go`、`internal/data/rpc/**`、`internal/data/thirdparty/**`：Kratos HTTP/gRPC client 应包含 `tracing.Client()`，调用时必须传入当前请求的 `ctx`。
- `internal/data/data.go` 和 repo：数据库日志依赖 `db.WithContext(ctx)` 或 `dao.WithContext(ctx)`。

Kratos logger 推荐形态：

```go
logger := log.With(ycLogger.GetKratosLogger(),
    "service.id", id,
    "service.name", bc.Name,
    "service.version", Version,
    "trace_id", tracing.TraceID(),
    "span_id", tracing.SpanID(),
)
```

如果服务既有字段是 `trace.id/span.id`，不要在同一次修复里强行全局改名；先保持平台已有字段，必要时同时补充 `trace_id/span_id` 需确认日志平台查询规则。

Kratos HTTP/gRPC middleware 推荐顺序：

```go
http.Middleware(
    tracing.Server(),
    metrics.KratosMiddleware(),
    mdLogger.Logger(config),
    recovery.Recovery(
        recovery.WithHandler(func(ctx context.Context, req, err interface{}) error {
            return errors.InternalServer(string(debug.Stack()), fmt.Sprintf("%+v", err))
        }),
    ),
    // ... requestCancel、metadata、validate、ratelimit 等其他 middleware
)
```

关键点：`recovery` 必须在 `tracing`、`logger` 中间件之后，这样 panic 恢复日志和堆栈信息才能关联到当前请求链路。

### Gin 服务

- `app.go` 应初始化 `tracingcommon.Init(...)`。
- `router.Use(...)` 中应包含 `loggerMiddleware.GinMiddleware(...)` 和 `tracing/gin.EnableTrace()`。
- 下游 resty 请求优先使用封装：`util.NewClient().SetContext(ctx)`，它会 `SetContext(ctx)` 并 `InjectHeader(ctx, req.Header)`。
- `seelog` 不会自动从 `ctx` 取 trace。请求内业务日志优先迁移到 `go-logger` 的 context 写法；无法迁移时，手工拼 `TraceID` 只能作为过渡兼容，不能替代 ctx 传播。

### Echo 服务

- `main.go` 应初始化 `tracingcommon.Init(...)`。
- Echo middleware 应包含 `loggerMiddleware.EchoMiddleware(...)` 和 `tracing/echo.EnableTrace()`。
- 认证/ACL middleware 修改 request context 时，必须基于 `c.Request().Context()` 派生，再 `c.SetRequest(c.Request().WithContext(newContext))`，不要用空 ctx 覆盖。
- 下游 resty client 应来自 `common.NewRestyClient()`，请求必须 `SetContext(ctx)` 或 `SetContext(c.Request().Context())`。

### 任务、cron、MQ、consumer

- HTTP 请求触发的异步任务：优先保留当前 ctx 的 trace value，但不要继承取消信号。
- cron、MQ 消费、离线工具没有上游请求时，可以开启独立链路或使用消息 trace 信息；不要把 `msgID`、订单号、随机 UUID 写成 `trace_id`。
- MQ 消费如需和生产者串联，检查消息 header 是否携带 traceparent，并在消费入口提取到 ctx 后再传给业务、DB 和下游。

如果入口配置缺失，优先修入口；如果入口正常，再查具体日志丢失位置。

## 定位缺失类型

按日志类型判断原因：

| 现象 | 常见原因 | 修复方向 |
|---|---|---|
| Kratos HTTP/gRPC 访问日志无 `trace_id` | `tracing.Server()` 未执行、日志 middleware 顺序异常、logger 未注入 trace valuer | 检查 server middleware 和入口 logger |
| Gin/Echo 访问日志无 `trace_id` | 未启用对应 tracing middleware，或 logger middleware 先于 trace 初始化且无法读取 ctx | 检查 `EnableTrace()` 与 go-logger middleware |
| 业务日志无 `trace_id` | 使用 `uc.log.Info/Error`、`seelog`、标准库 `log`，未使用 ctx | 改为 `WithContext(ctx)`；旧日志库只能过渡兼容 |
| SQL 日志无 `trace_id` | DB 操作未调用 `WithContext(ctx)` | 改为 `db.WithContext(ctx)` 或 `dao.WithContext(ctx)` |
| 下游服务日志断链 | 调用下游时没有传当前 `ctx`，或 client 未注入 trace header | client 调用传原始 `ctx`，并确认 `tracing.Client()`/resty middleware/`InjectHeader` |
| 依赖包没有 ctx 传播方法 | logger、DAO、resty 封装或中间件版本过旧 | 提示升级对应包版本，优先使用支持 `WithContext(ctx)`/`SetContext(ctx)` 的封装 |
| goroutine 日志断链 | 使用 `context.Background()` 或新建空 ctx | 复制当前 ctx 的 value |
| MQ/cron 日志无法和 HTTP 链路串联 | 无上游 trace，或未从消息 header 提取 trace | 有上游就提取/传递；无上游就开启独立链路 |
| 日志平台查不到 | 字段名混用 `trace_id` 与 `trace.id` | 按服务现状和日志平台字段名验证 |

## 正确写法

### Kratos 业务日志

```go
// Good: trace_id/span_id 由 Kratos logger 从 ctx 中读取
uc.log.WithContext(ctx).Infof("create order orderId=%s", orderID)
uc.log.WithContext(ctx).Errorf("create order failed err=%v", err)

// Bad: 缺少 ctx，日志无法串到 HTTP 请求
uc.log.Infof("create order orderId=%s", orderID)
```

### 直接使用 go-logger/zap

```go
import ycLogger "gitlab.yc345.tv/backend/go-logger/logger"

ycLogger.WithContext(ctx).Info("create order", zap.String("orderId", orderID))
```

不要在业务代码里用 `logger.GetLogger()` 后直接打日志，除非显式补 `logger.WithContext(ctx)`。

### Gin/Echo handler

```go
// Gin
func Handler(c *gin.Context) {
    ctx := c.Request.Context()
    err := service.Do(ctx, req)
}

// Echo
func Handler(c echo.Context) error {
    ctx := c.Request().Context()
    resp, err := svc.Do(ctx, req)
}
```

不要在 controller/service 边界把请求 ctx 换成 `context.Background()`。

### 数据库日志

```go
// Good
err := r.data.db.WithContext(ctx).
    Model(&model.OperationLog{}).
    Where("id = ?", id).
    First(&row).Error

// Good for gorm/gen
row, err := r.data.query.OperationLog.WithContext(ctx).
    Where(q.ID.Eq(id)).
    First()

// Bad
err := r.data.db.Model(&model.OperationLog{}).Where("id = ?", id).First(&row).Error
```

所有 repo 方法都应接收 `ctx context.Context`，并把它传给 GORM。

### 下游调用

```go
// Kratos client: tracing.Client() 会通过 ctx 传播 traceparent
reply, err := uc.someClient.SomeAPI(ctx, req)

// resty v2 with middleware
resp, err := common.NewRestyClient().R().
    SetContext(ctx).
    SetResult(&result).
    Get(url)

// resty v1 wrapper
resp, err := util.NewClient().SetContext(ctx).
    SetResult(&result).
    Get(url)
```

Bad：

```go
reply, err := uc.someClient.SomeAPI(context.Background(), req)
resp, err := resty.New().R().Get(url)
```

### 异步任务

请求结束后仍要继续执行的 goroutine，可以保留 trace value，但不要继承取消信号：

```go
asyncCtx := common.ValueOnlyContext{Context: ctx}
go func() {
    uc.log.WithContext(asyncCtx).Info("async task done")
}()
```

如果异步任务需要独立链路，显式创建新的 span；不要复用空 ctx 后再手工拼字段。

### seelog 旧代码

`seelog.Infof/Errorf` 无法自动感知 ctx。修复优先级：

1. 能迁移的请求内日志，改为支持 `WithContext(ctx)` 的 logger。
2. 暂不能迁移的旧路径，确保 DB 和下游调用已经传 ctx；文本中拼 `TraceID` 只能帮助人工检索，不算链路修复完成。
3. 不要新增只有 `seelog`、没有 ctx 参数的业务函数；必要时先把函数签名补上 `ctx context.Context`。

## 修改流程

1. 找到缺失 `trace_id` 的日志样本，确认它是 HTTP、业务、SQL、下游还是异步日志。
2. 识别服务类型：Kratos/Gin/Echo/任务服务，并检查对应入口配置。
3. 从日志所在代码向上追 `ctx` 参数，确认入口 `ctx` 是否被覆盖为 `context.Background()`、`context.TODO()` 或自建空 ctx。
4. 修复最早断开的地方，优先传递原始 `ctx`，不要只在日志行上补字段。
5. 对业务日志使用 `WithContext(ctx)`；对 zap 直打使用 `logger.WithContext(ctx)`；对 seelog 旧代码谨慎迁移。
6. 对数据库调用统一使用 `WithContext(ctx)`；事务、gorm/gen repo、批量查询也要传入同一个 `ctx`。
7. 对下游 client 调用传入同一个 `ctx`，确认 client 已配置 `tracing.Client()`、resty tracing middleware 或 `InjectHeader`。
8. Kratos HTTP/gRPC 服务检查 `recovery.Recovery(...)` 已加载并能打印 `debug.Stack()`；`recovery` 必须排在 `tracing.Server()`、日志 middleware 之后。
9. 如果相关包或封装没有 `WithContext(ctx)`、`SetContext(ctx)` 等方法，先记录为依赖能力缺失，并提示升级对应包版本。
10. 对 goroutine、cron、MQ 明确是继承上游 trace 还是开启独立链路。
11. 修改后运行受影响服务的测试或最小编译；再检查 lint。

## 工作区快速检查命令

优先用这些搜索定位断链点：

```bash
rg -n "context\\.Background\\(|context\\.TODO\\(|\\.Infof\\(|\\.Errorf\\(|seelog\\.|resty\\.New\\(|SetContext\\(|WithContext\\(ctx\\)" .
rg -n "tracing\\.Server\\(|tracing\\.Client\\(|EnableTrace\\(|TraceID\\(|SpanID\\(|tracingcommon\\.Init" .
rg -n "go-logger|GetKratosLogger|GinMiddleware|EchoMiddleware|KratosMiddleware" .
```

判断结果：

- 有入口 trace middleware，但业务日志缺失：优先修日志调用和 ctx 传递。
- SQL 日志缺失：优先修 repo/DAO 的 `WithContext(ctx)`。
- 下游断链：优先修 client 初始化和调用处 `ctx`。
- 只有 cron/MQ 日志：确认是否本来没有 HTTP 上游，不要强行要求与 HTTP trace 串联。

## 代码审查检查表

- [ ] 没有新增 `context.Background()`、`context.TODO()` 参与请求内业务链路。
- [ ] 新增或修改的日志都通过 `WithContext(ctx)` 输出。
- [ ] repo 层数据库操作都通过 `db.WithContext(ctx)` 或 `dao.WithContext(ctx)`。
- [ ] 下游 client、事务函数、分页查询、批量查询、批量导出都传递了 `ctx`。
- [ ] Kratos client 有 `tracing.Client()`；resty client 有 tracing middleware 或显式 `InjectHeader`。
- [ ] Kratos HTTP/gRPC middleware 已加载 `recovery.Recovery(...)`，能打印 `debug.Stack()`，且 `recovery` 位于 `tracing.Server()`、日志 middleware 之后。
- [ ] 如果 logger、DAO、HTTP client 缺少 ctx 传播方法，已提示升级对应包版本或切换到支持 ctx 的项目封装。
- [ ] Gin/Echo handler 从 request 获取 ctx，并向 service/biz/data 继续传递。
- [ ] goroutine 明确说明是继承请求链路还是开启独立链路。
- [ ] MQ consumer 明确是否提取消息 trace header；没有上游时不伪造 HTTP trace。
- [ ] 没有把 `span-id` 请求头当作 OpenTelemetry `span_id` 使用。
- [ ] 没有把 `log_id` 当作主链路字段；跨日志检索统一使用 `trace_id`。
- [ ] 字段名 `trace_id`/`trace.id` 与当前服务和日志平台保持一致。

## 常见误区

| 误区 | 正确处理 |
|---|---|
| 手工生成 UUID 填 `trace_id` | 使用 OpenTelemetry context 中的 trace |
| 只在 HTTP 日志里有 `trace_id` 就认为完成 | 还要检查业务日志和 SQL 日志 |
| repo 方法不传 ctx，内部自己 `context.Background()` | repo 方法签名接收 ctx 并向下传 |
| goroutine 直接使用原 ctx | 请求取消会影响异步任务；按需使用 `ValueOnlyContext` |
| 用请求头 `span-id` 串链路 | 该字段是自定义 UUID，不是 OTEL span |
| `resty.New()` 后直接请求 | 使用项目封装 client，并 `SetContext(ctx)` |
| seelog 文本里拼了 `TraceID` 就结束 | 这只是检索辅助，DB/下游/异步仍要靠 ctx |
| 看到 `trace.id` 就全局改成 `trace_id` | 先确认日志平台字段，避免破坏现有查询 |

## 输出要求

完成修复后，在回复中说明：

- 修复了哪类日志的断链问题。
- 断链根因是哪个 `ctx` 丢失点。
- 改动后哪些日志会共享同一个 `trace_id`。
- 当前服务类型和检查过的入口文件。
- 运行了哪些验证；如果未运行，说明原因。
