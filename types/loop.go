package types

import (
	"time"
)

// ========== 循环日志结构化事件模型（Day 3 + Day 6） ==========

// LoopEvent 循环事件，遵循文档建议的字段模型
type LoopEvent struct {
	Timestamp     time.Time              `json:"timestamp"`
	Level         string                 `json:"level"`          // INFO/WARN/ERROR
	Event         string                 `json:"event"`          // iteration.completed/tool.call.failed 等
	TaskID        string                 `json:"task_id"`        // 任务 ID
	CorrelationID string                 `json:"correlation_id"` // 关联 ID（跨轮次追踪）
	Iteration     int                    `json:"iteration"`      // 当前轮次
	Stage         string                 `json:"stage"`          // detect/diagnose/plan/patch/verify/release
	Action        string                 `json:"action"`         // 具体动作
	Outcome       string                 `json:"outcome"`        // success/failed/skipped
	DurationMs    int64                  `json:"duration_ms"`    // 耗时
	Error         string                 `json:"error,omitempty"`
	ErrorCode     string                 `json:"error_code,omitempty"` // 错误码
	ArtifactRef   string                 `json:"artifact_ref,omitempty"` // 产物引用
	FeedbackCount int                    `json:"feedback_count,omitempty"`
	Extra         map[string]interface{} `json:"extra,omitempty"`
}

// ========== 循环状态机（Day 5） ==========

// LoopPhase 循环阶段
type LoopPhase string

const (
	PhaseDetect   LoopPhase = "detect"
	PhaseDiagnose LoopPhase = "diagnose"
	PhasePlan     LoopPhase = "plan"
	PhasePatch    LoopPhase = "patch"
	PhaseVerify   LoopPhase = "verify"
	PhaseRelease  LoopPhase = "release"
	PhaseLearn    LoopPhase = "learn"
)

// LoopState 循环状态（运行状态 checkpoint）
type LoopState struct {
	SchemaVersion int            `json:"schema_version"`
	TaskID        string         `json:"task_id"`
	Goal          string         `json:"goal"`           // 目标描述
	Phase         LoopPhase      `json:"phase"`          // 当前阶段
	Iteration     int            `json:"iteration"`      // 当前轮次
	MaxIterations int            `json:"max_iterations"` // 最大轮次
	Completed     []string       `json:"completed"`      // 已完成阶段
	Artifacts     map[string]string `json:"artifacts"`   // 产物路径
	Decisions     []Decision     `json:"decisions"`      // 决策记录
	NextAction    string         `json:"next_action"`    // 下一步动作
	RetryCount    int            `json:"retry_count"`    // 重试次数
	UpdatedAt     time.Time      `json:"updated_at"`
}

// Decision 决策记录
type Decision struct {
	ID      string `json:"id"`      // 决策 ID
	Choice  string `json:"choice"`  // 选择
	Reason  string `json:"reason"`  // 原因
	Evidence string `json:"evidence"` // 证据
}

// ========== 错误分类（Day 7） ==========

// ErrorCategory 错误类别
type ErrorCategory string

const (
	ErrorTransient     ErrorCategory = "transient"      // 瞬时错误：网络抖动、429、503
	ErrorDeterministic ErrorCategory = "deterministic"  // 确定性错误：语法错误、参数不合法
	ErrorBusiness      ErrorCategory = "business"       // 业务拒绝：权限不足、状态冲突
	ErrorUnknown       ErrorCategory = "unknown"        // 未知错误：未分类异常
)

// ErrorDecision 错误决策
type ErrorDecision struct {
	Category   ErrorCategory `json:"category"`
	Strategy   string        `json:"strategy"`   // retry/fix/stop/escalate
	MaxRetries int           `json:"max_retries"`
	BackoffMs  int           `json:"backoff_ms"` // 退避时间
	Reason     string        `json:"reason"`
}

// ========== 评测集（Day 9） ==========

// EvalSample 评测样本
type EvalSample struct {
	ID          string            `json:"id"`
	Type        string            `json:"type"`        // normal/boundary/fault/adversarial
	Description string            `json:"description"`
	Input       string            `json:"input"`       // 输入代码或场景
	Expected    string            `json:"expected"`    // 期望结果
	Assertions  []EvalAssertion   `json:"assertions"`  // 断言列表
}

// EvalAssertion 评测断言
type EvalAssertion struct {
	Type     string `json:"type"`     // contains/not_contains/equals/regex
	Target   string `json:"target"`   // 检查目标（output/log/diff）
	Value    string `json:"value"`    // 期望值
	Critical bool   `json:"critical"` // 是否关键断言
}

// EvalResult 评测结果
type EvalResult struct {
	SampleID    string `json:"sample_id"`
	Passed      bool   `json:"passed"`
	Assertions  []EvalAssertionResult `json:"assertions"`
	DurationMs  int64  `json:"duration_ms"`
	ErrorReason string `json:"error_reason,omitempty"`
}

// EvalAssertionResult 断言结果
type EvalAssertionResult struct {
	Assertion EvalAssertion `json:"assertion"`
	Passed    bool          `json:"passed"`
	Actual    string        `json:"actual"`
}

// ========== 安全门禁（Day 9） ==========

// SafetyGate 安全门禁配置
type SafetyGate struct {
	ProtectedPaths   []string `json:"protected_paths"`    // 保护区路径
	MaxFilesChanged  int      `json:"max_files_changed"`  // 最大修改文件数
	RequireApproval  []string `json:"require_approval"`   // 需要人工审批的操作
	SecretScanEnabled bool    `json:"secret_scan_enabled"` // 密钥扫描
}

// SafetyCheckResult 安全检查结果
type SafetyCheckResult struct {
	Passed   bool     `json:"passed"`
	Violations []SafetyViolation `json:"violations"`
}

// SafetyViolation 安全违规
type SafetyViolation struct {
	Type     string `json:"type"`     // protected_path/max_files/secret_found/approval_required
	Path     string `json:"path"`
	Detail   string `json:"detail"`
	Severity string `json:"severity"` // critical/high/medium
}

// ========== 循环指标（Day 6） ==========

// LoopMetrics 循环指标
type LoopMetrics struct {
	TotalTasks       int     `json:"total_tasks"`
	SuccessTasks     int     `json:"success_tasks"`
	FailedTasks      int     `json:"failed_tasks"`
	SuccessRate      float64 `json:"success_rate"`       // 成功率
	AvgIterations    float64 `json:"avg_iterations"`     // 平均轮次
	P95Iterations    int     `json:"p95_iterations"`     // P95 轮次
	NoProgressRatio  float64 `json:"no_progress_ratio"`  // 未收敛比例
	AvgDurationMs    int64   `json:"avg_duration_ms"`    // 平均耗时
	P95DurationMs    int64   `json:"p95_duration_ms"`    // P95 耗时
	RetryRate        float64 `json:"retry_rate"`         // 重试率
}
