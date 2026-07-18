# Loop Engineering: 日志 WithContext 自动修复系统

## Context

需要实现一个基于 CloudWeGo Eino 框架的 **Loop Engineering** 系统，核心功能是：
1. **定期扫描**指定文件夹下所有 Go 代码仓库的 master 分支
2. **检测**日志调用中缺少 `WithContext(ctx)` 的问题
3. **自动修复**：创建 git worktree 新分支 → 执行修复 → 提交代码 → 生成报告

这是 Loop Engineering 的第一个实践，目标是建立一套可复用的 "扫描 → 诊断 → 修复" 自动化流水线。

---

## 整体架构

```
┌──────────────────────────────────────────────────────────────────┐
│                    Loop Scheduler (主循环)                        │
│  定时触发 → 遍历仓库 → 执行 Graph Pipeline → 汇总报告            │
└──────────┬───────────────────────────────────────────────────────┘
           │
           ▼
┌──────────────────────────────────────────────────────────────────┐
│              Eino Graph Pipeline (核心处理流)                     │
│                                                                   │
│  ┌──────────┐   ┌──────────┐   ┌──────────┐   ┌──────────────┐  │
│  │ Scanner  │──▶│ Analyzer │──▶│ Fixer    │──▶│  Verifier    │  │
│  │ (检测)   │   │ (诊断)   │   │ (修复)   │   │  (验证)      │  │
│  └──────────┘   └──────────┘   └──────────┘   └──────┬───────┘  │
│       │               │               ▲               │          │
│  scan_repo      analyze_logs     re-apply        ┌────┴─────┐   │
│  list_repos     check_ctx       (on failure)     │  PASS?   │   │
│  grep_logs      categorize                       └────┬─────┘   │
│                                                  YES  │   NO    │
│                                                   │   └──▶ 回到 │
│                                                   ▼      Fixer  │
│  ┌──────────┐                               (最多 N 轮重试)      │
│  │Reporter  │◀────────────────────────────────────┘              │
│  │ (报告)   │                                                    │
│  └──────────┘                                                    │
│  gen_report                                                      │
└──────────────────────────────────────────────────────────────────┘
```

### 验证闭环详细流程

```
Fixer 修复完成
      │
      ▼
┌─────────────────────────────────┐
│       Verifier (三级验证)        │
│                                  │
│  ① 编译验证: go build ./...     │
│     - 失败 → 回滚本轮修改        │
│       → 分析编译错误              │
│       → 重新修复 (最多 3 轮)      │
│                                  │
│  ② 重扫描验证: 再次 AST 分析     │
│     - 对比修复前后的问题清单      │
│     - 检查是否所有目标调用点      │
│       都已转为 WithContext 形式   │
│     - 若仍有遗留 → 回到 Fixer    │
│       重新修复遗留项 (最多 3 轮)  │
│                                  │
│  ③ 回归验证: go vet + 单元测试   │
│     - 确保修复没有引入新问题      │
│     - 失败 → 标记为需人工审查     │
│                                  │
└────────────┬────────────────────┘
             │
        全部通过？
        ┌────┴────┐
        │         │
       YES        NO (达到最大重试次数)
        │         │
        ▼         ▼
   Git Commit   标记为 "需人工审查"
   + 报告       + 记录失败原因
```

---

## 实现步骤

### Phase 1: 项目初始化

**文件结构：**

```
eino-loop/
├── go.mod
├── go.sum
├── main.go                          # 入口：定时调度器
├── config/
│   └── config.go                    # 配置（仓库路径、扫描间隔、飞书配置等）
├── tools/
│   ├── scanner.go                   # 扫描工具：列出仓库、拉取代码、grep 日志
│   ├── analyzer.go                  # 分析工具：判断日志是否缺少 WithContext
│   ├── fixer.go                     # 修复工具：AST 重写、worktree 管理、git 操作
│   ├── verifier.go                  # 验证工具：编译/重扫描/回归三级验证 + 重试
│   └── reporter.go                  # 报告工具：生成飞书文档 + 发送飞书消息
├── feishu/
│   ├── doc.go                       # 飞书文档创建（调用 lark-cli）
│   ├── message.go                   # 飞书消息卡片构建与发送
│   └── card.go                      # 消息卡片模板
├── agent/
│   └── loop.go                      # Eino Graph 编排：组装含验证闭环的 pipeline
├── prompts/
│   └── system.go                    # System prompt（给 LLM 的修复指令）
└── skill/
    └── fixing-trace-id-logs/        # 复制 skill 内容供程序读取
        └── SKILL.md
```

**go.mod 依赖：**

```go
module github.com/user/eino-loop

require (
    github.com/cloudwego/eino v0.3.x
    github.com/cloudwego/eino-ext v0.1.x
    // 其他依赖：go-git, golang.org/x/tools (AST), etc.
)
```

### Phase 2: 实现扫描工具 (tools/scanner.go)

**职责：** 发现仓库 + 拉取最新代码 + 定位有问题的日志文件

```
Tool: scan_repositories
  Input:  { repo_root: string }
  Output: { repos: []string }  // 仓库路径列表

Tool: pull_latest
  Input:  { repo_path: string, branch: string }
  Output: { success: bool, error: string }

Tool: find_log_without_context
  Input:  { repo_path: string, log_func: string }
  Output: { files: []FileLocation }
  // FileLocation: { file, line, func_name, log_expr }
```

**实现要点：**
- `scan_repositories`：读取目录，过滤含 `.git` 的子目录
- `pull_latest`：使用 `go-git` 库执行 `git checkout master && git pull`
- `find_log_without_context`：
  - 使用 `go/ast` + `go/parser` 解析 `.go` 文件
  - 遍历 CallExpr，匹配日志函数调用（非 variadic 形式，即 `slog.Info("msg")` 而非 `slog.Info("msg", args...)` 或 `slog.InfoContext(ctx, ...)`）
  - 排除：已有 `With(ctx)` 的 `*slog.Logger` 调用
  - 排除：已有 `WithContext` 的 fiber 调用

**识别规则（严格按 SKILL.md）：**

| 库 | 标准形式（✅ 要修） | WithContext 形式（✅ 已合规） |
|---|---|---|
| log/slog | `slog.Info(msg)` | `slog.InfoContext(ctx, msg)` |
| log/slog | `slog.Info(msg, args...)` | `slog.InfoContext(ctx, msg, args...)` |
| github.com/gofiber/fiber/v2/log | `log.Info(msg)` | `log.WithContext(c).Info(msg)` |
| logrus | `entry.Info(msg)` | `entry.WithContext(ctx).Info(msg)` |

### Phase 3: 实现分析工具 (tools/analyzer.go)

**职责：** 精确诊断每个发现的问题，判断修复方案

```
Tool: analyze_log_callsite
  Input:  { file: string, line: int }
  Output: {
    log_lib: string,          // slog / fiber-log / logrus
    func_name: string,        // Info/Warn/Error/Fatal 等
    is_variadic: bool,        // 是否已有任意参数
    fix_type: string,         // "context_param" / "logger_receiver" / "skip"
    has_ctx_param: bool,      // 函数签名中是否有 ctx
    nearest_ctx: string,      // 最近可用的 ctx 变量名
    risk_level: string,       // low / medium / high
  }
```

**实现要点：**
- 解析日志调用所在函数的 AST
- 检查函数签名是否有 `ctx context.Context` 参数
- 向上遍历作用域寻找可用的 `ctx` 变量
- 若函数内无 ctx → 标记为 `risk_level: high`，可能需要添加 ctx 参数
- 若函数内有 ctx → 标记为 `fix_type: context_param`，直接修复

### Phase 4: 实现修复工具 (tools/fixer.go)

**职责：** 创建 worktree、执行 AST 重写、提交修复

```
Tool: create_fix_branch
  Input:  { repo_path: string, fix_desc: string }
  Output: { branch_name: string, worktree_path: string }
  // 使用 go-git 的 Worktree 功能
  // 分支名: fix/slog-withcontext-{timestamp}

Tool: apply_log_fix
  Input:  {
    file: string,
    line: int,
    fix_type: string,       // "add_context_param" / "add_with_context"
    log_lib: string,
    ctx_var: string,
  }
  Output: { success: bool, diff: string }
  // 使用 golang.org/x/tools/go/ast 重写
  // slog.Info("msg") → slog.InfoContext(ctx, "msg")
  // log.Info("msg") → log.WithContext(ctx).Info("msg")

Tool: commit_and_push
  Input:  { worktree_path: string, message: string }
  Output: { success: bool, commit_hash: string, branch: string }
```

**AST 重写核心逻辑：**

```go
// 对于 slog 调用：
// Before: slog.Info("operation failed", "err", err)
// After:  slog.InfoContext(ctx, "operation failed", "err", err)
//
// 对于 fiber-log 调用：
// Before: log.Info("task started")
// After:  log.WithContext(c).Info("task started")
//
// 对于 logrus 调用：
// Before: entry.Info("msg")
// After:  entry.WithContext(ctx).Info("msg")
```

### Phase 5: 实现验证工具 (tools/verifier.go) ⭐ 新增

**职责：** 三级验证修复正确性，不通过则驱动重修循环

```
Tool: verify_compile
  Input:  { worktree_path: string }
  Output: { success: bool, errors: []CompileError }
  // 执行 go build ./...
  // 收集编译错误，解析为结构化信息

Tool: verify_rescan
  Input:  { worktree_path: string, original_issues: []FileLocation }
  Output: {
    all_fixed: bool,
    remaining: []FileLocation,     // 仍然存在的问题
    newly_introduced: []FileLocation, // 修复引入的新问题
  }
  // 重新执行 AST 扫描
  // 对比 original_issues 和新扫描结果
  // 确认所有原始问题已修复

Tool: verify_regression
  Input:  { worktree_path: string }
  Output: { vet_pass: bool, test_pass: bool, errors: []string }
  // 执行 go vet ./...
  // 执行 go test ./... (可选，可通过配置开关)

Tool: rollback_fix
  Input:  { worktree_path: string, file: string }
  Output: { success: bool }
  // git checkout -- <file> 回滚单个文件的修改
```

**验证流程与重试逻辑：**

```go
// VerifyResult 贯穿整个验证闭环
type VerifyResult struct {
    CompileOK      bool
    AllIssuesFixed bool
    RegressionFree bool
    Remaining      []FileLocation   // 遗留问题
    CompileErrors  []CompileError   // 编译错误
    RetryCount     int              // 当前重试次数
    MaxRetries     int              // 最大重试次数 (默认 3)
    NeedsHuman     bool             // 是否需要人工介入
}

// 验证闭环核心逻辑 (在 Graph 的 Lambda 中实现)
func verifyAndRetry(ctx context.Context, fixResult FixResult) (*VerifyResult, error) {
    for round := 0; round < maxRetries; round++ {
        // Level 1: 编译验证
        compileResult := verifyCompile(ctx, fixResult.WorktreePath)
        if !compileResult.Success {
            // 分析编译错误 → 重新修复
            rollbackFailedFiles(ctx, compileResult.Errors)
            refixFromCompileErrors(ctx, compileResult.Errors)
            continue
        }

        // Level 2: 重扫描验证 (对比修复前后)
        rescanResult := verifyRescan(ctx, fixResult.WorktreePath, fixResult.OriginalIssues)
        if !rescanResult.AllFixed {
            // 仍有遗留问题 → 重新修复遗留项
            refixRemaining(ctx, rescanResult.Remaining)
            continue
        }

        // Level 3: 回归验证
        regressionResult := verifyRegression(ctx, fixResult.WorktreePath)
        if !regressionResult.VetPass {
            // go vet 失败 → 标记需人工审查
            return &VerifyResult{NeedsHuman: true}, nil
        }

        // 全部通过
        return &VerifyResult{CompileOK: true, AllIssuesFixed: true, RegressionFree: true}, nil
    }

    // 达到最大重试次数
    return &VerifyResult{NeedsHuman: true, RetryCount: maxRetries}, nil
}
```

**Eino Graph 中的循环编排：**

```go
// 关键：在 Eino Graph 中使用 Branch + Loop 节点实现重试
func buildVerifyLoop(g *compose.Graph[...]) {
    // Fixer → Verifier
    g.AddEdge("fixer", "verifier")

    // Verifier 根据结果分支
    g.AddBranch("verifier", func(ctx context.Context, vr VerifyResult) (string, error) {
        if vr.CompileOK && vr.AllIssuesFixed && vr.RegressionFree {
            return "reporter", nil        // ✅ 全部通过 → 进入报告
        }
        if vr.NeedsHuman {
            return "human_review", nil    // ⚠️ 需人工 → 标记后进入报告
        }
        if vr.RetryCount < vr.MaxRetries {
            return "refix", nil           // 🔄 还可重试 → 回到 Fixer
        }
        return "reporter", nil            // ❌ 超限 → 进入报告（标记失败）
    })

    // 循环回路：refix → analyzer → fixer → verifier
    g.AddEdge("refix", "analyzer")
    g.AddEdge("analyzer", "fixer")
    // fixer → verifier 的边已在上面定义
}
```

### Phase 6: 实现报告工具 (tools/reporter.go)

**职责：** 汇总修复结果 → 生成飞书文档 → 发送飞书消息通知

```
Tool: generate_feishu_doc
  Input:  { results: []RepoFixResult }
  Output: { doc_url: string, doc_id: string }
  // 1. 将修复结果渲染为飞书文档格式（富文本 / Markdown）
  // 2. 调用飞书 CLI 创建飞书云文档
  // 3. 返回文档链接

Tool: send_feishu_message
  Input:  {
    doc_url: string,
    summary: ReportSummary,
    chat_id: string,           // 飞书群 ID（可配置）
  }
  Output: { success: bool, message_id: string }
  // 发送飞书消息卡片到指定群/用户
  // 卡片包含：摘要统计 + 文档链接 + 仓库状态一览
```

**飞书 CLI 集成方式：**

```go
// 使用飞书 CLI 创建文档并发送消息
// 飞书 CLI 文档: https://github.com/larksuite/cli

// Step 1: 生成飞书文档内容 (Markdown 格式，飞书 CLI 自动转换)
func generateDocContent(results []RepoFixResult) string {
    // 渲染为飞书兼容的 Markdown
    // 包含：摘要表格、仓库详情、验证结果、遗留问题
}

// Step 2: 通过飞书 CLI 创建云文档
// lark-cli doc create --title "Log WithContext 修复报告 2026-07-18" --content report.md
func createFeishuDoc(ctx context.Context, content string) (docURL string, err error) {
    cmd := exec.CommandContext(ctx, "lark-cli", "doc", "create",
        "--title", fmt.Sprintf("Log WithContext 修复报告 %s", time.Now().Format("2006-01-02")),
        "--content-file", tmpFile,
    )
    // 解析输出获取文档 URL
}

// Step 3: 通过飞书 CLI 发送消息卡片
// lark-cli message send --chat-id <id> --type interactive --card card.json
func sendFeishuCard(ctx context.Context, chatID, docURL string, summary ReportSummary) error {
    card := buildMessageCard(docURL, summary)
    cmd := exec.CommandContext(ctx, "lark-cli", "message", "send",
        "--chat-id", chatID,
        "--type", "interactive",
        "--card", card,
    )
}
```

**飞书消息卡片设计：**

```
┌─────────────────────────────────────────────┐
│  🔧 Log WithContext 自动修复报告             │
│  2026-07-18 14:30:00                        │
├─────────────────────────────────────────────┤
│                                              │
│  📊 扫描摘要                                 │
│  ┌──────────┬──────┐                         │
│  │ 扫描仓库  │  5   │                         │
│  │ 发现问题  │  3   │                         │
│  │ 修复通过  │  2 ✅│                         │
│  │ 需人工    │  1 ⚠️│                         │
│  │ 修复调用点│  47  │                         │
│  └──────────┴──────┘                         │
│                                              │
│  ✅ repo-a  — 编译通过，1轮修复              │
│  ✅ repo-b  — 编译通过，2轮修复              │
│  ⚠️ repo-c  — 2处遗留，需人工审查            │
│                                              │
│  [📄 查看完整报告](https://xxx.feishu.cn/...) │
│                                              │
└─────────────────────────────────────────────┘
```

**报告文档内容结构：**

```markdown
# Log WithContext 修复报告
生成时间: 2026-07-18 14:30:00

## 扫描摘要
| 指标 | 数量 |
|------|------|
| 扫描仓库数 | 5 |
| 发现问题仓库 | 3 |
| 修复文件数 | 12 |
| 修复调用点 | 47 |
| 跳过（无 ctx 可用）| 3 |
| 验证通过 | 2 ✅ |
| 需人工审查 | 1 ⚠️ |

## 仓库详情

### 📦 repo-a ✅
- **分支**: fix/slog-withcontext-20260718
- **Commit**: abc1234
- **验证**: 编译✅ 重扫描✅ 回归✅
- **修复轮次**: 1 轮
- **修复文件**:
  | 文件 | 修复数 | 日志库 |
  |------|--------|--------|
  | internal/service/user.go | 5 | slog |
  | cmd/server/main.go | 2 | fiber-log |

### 📦 repo-c ⚠️ 需人工审查
- **分支**: fix/slog-withcontext-20260718
- **验证**: 编译✅ 重扫描❌ 回归⚠️
- **修复轮次**: 3 轮（已达上限）
- **遗留问题**:
  | 文件 | 行号 | 原因 |
  |------|------|------|
  | internal/handler/cron.go | 87 | 函数内无 ctx 可用 |
  | cmd/worker/main.go | 42 | init() 中的日志调用 |
- **建议**: 手动为 cron handler 添加 context 参数传递
```

### Phase 7: Eino Graph 编排 (agent/loop.go)

**核心：使用 Eino 的 compose.Graph 组装含验证闭环的 pipeline**

```go
func BuildLoopGraph(cfg *config.Config) (*compose.Runnable[LoopInput, LoopReport], error) {
    g := compose.NewGraph[LoopInput, LoopReport]()

    // 注册节点
    g.AddLambdaNode("scanner",      scannerLambda)      // 扫描仓库
    g.AddLambdaNode("puller",       pullLambda)         // 拉取最新代码
    g.AddLambdaNode("detector",     detectorLambda)     // 检测日志问题
    g.AddLambdaNode("analyzer",     analyzerLambda)     // 分析每个问题
    g.AddLambdaNode("fixer",        fixerLambda)        // 执行修复
    g.AddLambdaNode("verifier",     verifierLambda)     // ⭐ 验证修复结果
    g.AddLambdaNode("refix",        refixLambda)        // ⭐ 重修处理器
    g.AddLambdaNode("reporter",     reporterLambda)     // 生成报告

    // 主干边
    g.AddEdge(compose.START, "scanner")
    g.AddEdge("scanner", "puller")
    g.AddEdge("puller", "detector")
    g.AddEdge("detector", "analyzer")
    g.AddEdge("analyzer", "fixer")
    g.AddEdge("fixer", "verifier")

    // ⭐ 验证分支：根据验证结果决定走向
    g.AddBranch("verifier", func(ctx context.Context, vr VerifyResult) (string, error) {
        if vr.AllPassed() {
            return "reporter", nil       // ✅ 全部通过
        }
        if vr.CanRetry() {
            return "refix", nil          // 🔄 可以重试
        }
        return "reporter", nil           // ❌ 超限，标记失败后报告
    })

    // ⭐ 循环回路：refix → analyzer → fixer → verifier
    g.AddEdge("refix", "analyzer")
    g.AddEdge("analyzer", "fixer")
    g.AddEdge("fixer", "verifier")       // verifier 的分支边已定义

    g.AddEdge("reporter", compose.END)

    return g.Compile(context.Background())
}
```

**为什么选择 Graph Branch 而非简单 for 循环：**

> Eino 的 Graph 天然支持 Branch 条件分支，将验证闭环建模为
> Graph 的循环边（verifier → refix → analyzer → fixer → verifier）
> 比在 Lambda 内部写 for 循环更符合框架理念，且能利用 Eino 的
> 回调、追踪、checkpoint 等基础设施。每次循环在 Graph 可视化中
> 都有清晰的执行路径。

### Phase 7: 定时调度器 (main.go)

```go
func main() {
    cfg := config.Load()

    // 编译 Graph
    loop, err := agent.BuildLoopGraph(cfg)
    if err != nil { log.Fatal(err) }

    // 定时执行
    ticker := time.NewTicker(cfg.ScanInterval) // 如 1 小时
    defer ticker.Stop()

    // 启动时立即执行一次
    runLoop(loop, cfg)

    for range ticker.C {
        runLoop(loop, cfg)
    }
}

func runLoop(loop *compose.Runnable[LoopInput, LoopReport], cfg *config.Config) {
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
    defer cancel()

    report, err := loop.Invoke(ctx, LoopInput{RepoRoot: cfg.RepoRoot})
    if err != nil {
        log.Printf("Loop execution failed: %v", err)
        return
    }
    // 输出报告（终端/文件/webhook）
    fmt.Println(report)
}
```

### Phase 9: 配置管理 (config/config.go)

```go
type Config struct {
    RepoRoot      string        // 仓库根目录，如 ~/projects
    TargetBranch  string        // 目标分支，如 master / main
    FixBranchTpl  string        // 修复分支模板，如 "fix/slog-withcontext-{date}"
    ScanInterval  time.Duration // 扫描间隔，如 1h
    MaxRepos      int           // 单次最大扫描仓库数
    LogFunctions  []LogFunc     // 需检测的日志函数列表
    DryRun        bool          // true 则只检测不修复

    // 飞书配置
    FeishuChatID  string        // 飞书群 ID，接收修复报告
    FeishuCLIPath string        // lark-cli 路径，默认 "lark-cli"
    FeishuDocSpace string       // 飞书文档空间 ID（存放报告文档）
    FeishuEnabled bool          // 是否启用飞书通知（false 则只输出终端）
}

type LogFunc struct {
    Library    string   // "slog" / "fiber" / "logrus"
    Functions  []string // ["Info", "Warn", "Error", ...]
    CtxForm    string   // WithContext 调用形式
}
```

---

## 关键技术选型

| 组件 | 选型 | 理由 |
|------|------|------|
| 框架 | cloudwego/eino | 用户指定，Go 原生 AI 编排 |
| 编排模式 | compose.Graph (DAG) | 确定性流程，含验证闭环的 Branch 循环 |
| Git 操作 | go-git | 纯 Go 实现，支持 worktree |
| AST 分析 | go/ast + go/parser | 标准库，精确解析 Go 代码 |
| AST 重写 | golang.org/x/tools/go/ast/rewrite | 官方工具，可靠重写 |
| 飞书文档 | lark-cli (飞书 CLI) | CLI 创建云文档，支持 Markdown 富文本 |
| 飞书消息 | lark-cli message send | 发送交互式消息卡片到群/用户 |
| 配置 | YAML + env | 灵活配置，支持环境变量覆盖 |

---

## 风险与边界处理

| 风险 | 应对策略 |
|------|---------|
| 函数内无 ctx 可用 | 标记为 high risk，Verifier 重扫描时识别，报告中提示人工介入 |
| 仓库有未提交更改 | 跳过该仓库，报告中注明 |
| AST 重写导致编译失败 | Verifier Level 1 捕获 → 回滚 → 分析错误 → 重新修复（最多 3 轮） |
| 修复引入新问题 | Verifier Level 2 重扫描对比 → 发现遗留/新问题 → 重新修复 |
| 多轮重修仍不通过 | 达到 MaxRetries(3) 后标记为 "需人工审查"，报告中记录详细原因 |
| 重修循环无限执行 | MaxRetries 硬上限 + 全局 timeout 双重保障 |
| 回归测试破坏现有功能 | Verifier Level 3 go vet 检查，可选 go test |
| 并发安全 | 每个仓库使用独立 worktree，互不干扰 |
| 大仓库扫描慢 | 支持 `--max-repos` 限制，支持增量扫描（只扫修改过的文件） |

---

## 验证方式

1. **单元测试**：
   - `scanner_test.go`：测试日志检测的准确率（正例 + 反例）
   - `analyzer_test.go`：测试 ctx 查找逻辑
   - `fixer_test.go`：测试 AST 重写的正确性（对比 golden file）

2. **集成测试**：
   - 创建测试仓库，包含各种日志场景
   - 执行完整 pipeline，验证分支创建、代码修复、编译通过

3. **端到端验证**：
   ```bash
   # 准备测试仓库
   mkdir -p /tmp/test-repos/repo-a && cd /tmp/test-repos/repo-a
   git init && echo 'package main; func f() { slog.Info("test") }' > main.go
   git add . && git commit -m "init"

   # 运行 loop
   go run main.go --repo-root=/tmp/test-repos --dry-run
   # 预期：检测到 1 处 slog.Info 缺少 Context

   go run main.go --repo-root=/tmp/test-repos
   # 预期：创建 fix 分支，修复代码，编译通过
   ```

---

## 实现优先级与排期

| 阶段 | 内容 | 估时 |
|------|------|------|
| P0 | Phase 1-2: 项目初始化 + Scanner 工具 | 核心 |
| P0 | Phase 3-4: Analyzer + Fixer (AST 重写) | 核心 |
| P0 | Phase 5: Verifier (三级验证 + 重试闭环) | 核心 |
| P1 | Phase 6: Reporter (飞书文档 + 消息通知) | 重要 |
| P1 | Phase 7-8: Eino Graph 编排 + 定时调度器 | 重要 |
| P2 | Phase 9: 配置管理 + 命令行参数 | 增强 |
| P2 | 单元测试 + 集成测试 | 质量保障 |
