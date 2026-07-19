# eino-loop 服务架构图

## 整体架构

```mermaid
graph TB
    subgraph "入口层"
        MAIN["main.go<br/>定时调度 + 优雅退出"]
        ENV[".env 配置"]
    end

    subgraph "Agent 编排层 (agent/)"
        ORCH["主编排 Agent<br/>adk.NewChatModelAgent<br/>+ SKILL 指令注入"]
        RUNNER["adk.Runner<br/>+ CheckPointStore"]
        CB["LoopCallbackHandler<br/>eino callbacks.Hanler<br/>结构化事件日志"]
    end

    subgraph "SubAgent 层 (5 个专职 Agent)"
        SA["Scanner Agent<br/>扫描+拉取+检测"]
        AA["Analyzer Agent<br/>+ SKILL: fixing-trace-id-logs"]
        FA["Fixer Agent<br/>+ SKILL: fixing-trace-id-logs"]
        VA["Verifier Agent<br/>编译+重扫描+回归"]
        RA["Reporter Agent<br/>报告+飞书通知"]
    end

    subgraph "工具层 (tools/)"
        T1["scan_repositories"]
        T2["pull_latest"]
        T3["find_log_issues"]
        T4["analyze_callsite"]
        T5["apply_fix"]
        T6["verify_compile"]
        T7["verify_rescan"]
        T8["verify_regression"]
        T9["commit_and_push"]
        T10["generate_report"]
        T11["send_feishu"]
    end

    subgraph "核心能力"
        SCANNER["scanner.go<br/>go-logger/gorm/seelog/resty<br/>+ 结构体 logger 检测"]
        ANALYZER["analyzer.go<br/>ctx 查找 + 修复类型判断"]
        FIXER["fixer.go<br/>AST 重写修复"]
        VERIFIER["verifier.go<br/>三级验证"]
        VALIDATOR["validator.go<br/>路径安全校验"]
        WT["worktree.go<br/>git worktree 隔离"]
        MW["middleware.go<br/>Kratos/Gin/Echo 检测"]
    end

    subgraph "评测与安全 (Day 9)"
        EVAL["Evaluator<br/>8 样本评测集"]
        SAFE["SafetyChecker<br/>保护区 + 审批点"]
        CONV["ConvergenceChecker<br/>差距函数 + 收敛检查"]
    end

    subgraph "外部服务"
        GIT["Git 仓库<br/>go-git"]
        LLM["LLM API<br/>OpenAI 兼容"]
        FEISHU["飞书<br/>lark-cli"]
    end

    MAIN --> ORCH
    ENV --> MAIN
    ORCH --> RUNNER
    RUNNER --> CB
    ORCH --> SA & AA & FA & VA & RA

    SA --> T1 & T2 & T3
    AA --> T4
    FA --> T5 & T9
    VA --> T6 & T7 & T8
    RA --> T10 & T11

    T1 & T2 --> SCANNER
    T3 --> SCANNER
    T4 --> ANALYZER
    T5 --> FIXER & VALIDATOR & WT
    T6 & T7 & T8 --> VERIFIER
    T10 --> EVAL & SAFE & CONV
    T11 --> FEISHU

    SCANNER --> GIT
    ORCH --> LLM
    FIXER --> GIT
    T9 --> GIT
```

## 数据流

```mermaid
sequenceDiagram
    participant M as main.go
    participant O as Orchestrator
    participant S as Scanner
    participant A as Analyzer
    participant F as Fixer
    participant V as Verifier
    participant R as Reporter

    M->>O: 定时触发 (ticker)
    O->>S: 扫描仓库目录
    S-->>O: 仓库列表

    loop 每个仓库
        O->>S: 拉取最新代码 + 检测问题
        S-->>O: 问题列表 (FileLocation[])

        loop 每个问题
            O->>A: 分析修复方案 (SKILL)
            A-->>O: AnalyzeResult

            alt fix_type != skip
                O->>F: 执行 AST 修复 (SKILL)
                F-->>O: FixResult
            end
        end

        O->>V: 三级验证
        V-->>O: VerifyResult

        alt 验证失败 && 重试次数 < 3
            O->>A: 重新分析遗留问题
        end
    end

    O->>R: 生成报告
    R->>R: 创建飞书文档
    R->>R: 发送飞书消息
    R-->>M: 最终报告
```

## 检测能力矩阵

```mermaid
graph LR
    subgraph "检测目标"
        GL["go-logger<br/>ycLogger.Info()"]
        GM["gorm<br/>db.First()"]
        SL["seelog<br/>seelog.Info()"]
        RY["resty<br/>client.R().Get()"]
        ST["结构体 logger<br/>s.log.Info()"]
    end

    subgraph "修复方式"
        F1["WithContext(ctx)"]
        F2["标记需迁移"]
        F3["标记需添加 SetContext"]
    end

    GL -->|".WithContext(ctx).Info()"| F1
    GM -->|".WithContext(ctx).First()"| F1
    ST -->|".WithContext(ctx).Info()"| F1
    SL -->|"不支持 WithContext"| F2
    RY -->|"需要 SetContext(ctx)"| F3
```

## Loop Engineering 闭环

```mermaid
graph LR
    D["Detect<br/>检测"] --> Di["Diagnose<br/>诊断"]
    Di --> P["Plan<br/>规划"]
    P --> Pa["Patch<br/>修复"]
    Pa --> V["Verify<br/>验证"]
    V -->|"通过"| R["Release<br/>提交"]
    V -->|"失败"| L["Learn<br/>学习"]
    L --> Di
    R --> Re["Report<br/>报告"]
    Re --> F["飞书通知"]
```

## 文件结构

```
eino-loop/
├── main.go                    # 入口：定时调度 + 优雅退出
├── .env                       # 配置文件
├── scripts/
│   └── check-feishu.sh        # 飞书配置校验脚本
├── agent/
│   ├── multiagent.go          # 多 Agent 编排 (ADK + AgentTool)
│   └── callbacks.go           # eino callbacks + checkpoint + 错误分类
├── config/
│   └── config.go              # 配置管理 + 验证
├── prompts/
│   └── system.go              # Agent 指令 + SKILL 加载
├── tools/
│   ├── scanner.go             # 仓库扫描 + 5 类日志检测 + 增量扫描
│   ├── analyzer.go            # 调用点分析 (ctx 查找)
│   ├── fixer.go               # AST 重写修复
│   ├── verifier.go            # 三级验证 (编译/重扫描/回归)
│   ├── reporter.go            # 报告生成 (Markdown)
│   ├── feishu.go              # 飞书文档创建 + 消息发送
│   ├── validator.go           # 路径安全校验
│   ├── worktree.go            # git worktree 隔离
│   ├── middleware.go          # Kratos/Gin/Echo 中间件检测
│   ├── evaluator.go           # 评测集 + 安全门禁 + 收敛检查
│   ├── tools.go               # 工具注册 (11 个 InvokableTool)
│   └── tool_eino.go           # eino 泛型适配器
└── types/
    ├── types.go               # 共享类型
    └── loop.go                # 循环工程核心类型
```
