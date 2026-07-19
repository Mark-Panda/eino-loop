package tools

import (
	"fmt"
	"strings"

	"github.com/Mark-Panda/eino-loop/types"
)

// ========== 评测集（Day 9） ==========

// Evaluator 评测器
type Evaluator struct {
	samples []types.EvalSample
}

// NewEvaluator 创建评测器
func NewEvaluator() *Evaluator {
	return &Evaluator{
		samples: defaultEvalSamples(),
	}
}

// Evaluate 对修复结果进行评测
func (e *Evaluator) Evaluate(output string) []types.EvalResult {
	var results []types.EvalResult
	for _, sample := range e.samples {
		result := e.evaluateSample(sample, output)
		results = append(results, result)
	}
	return results
}

// evaluateSample 评测单个样本
func (e *Evaluator) evaluateSample(sample types.EvalSample, output string) types.EvalResult {
	result := types.EvalResult{
		SampleID: sample.ID,
		Passed:   true,
	}

	for _, assertion := range sample.Assertions {
		ar := types.EvalAssertionResult{
			Assertion: assertion,
		}

		switch assertion.Type {
		case "contains":
			ar.Passed = strings.Contains(output, assertion.Value)
			ar.Actual = truncateStr(output, 200)
		case "not_contains":
			ar.Passed = !strings.Contains(output, assertion.Value)
			ar.Actual = truncateStr(output, 200)
		case "equals":
			ar.Passed = strings.TrimSpace(output) == strings.TrimSpace(assertion.Value)
			ar.Actual = truncateStr(output, 200)
		default:
			ar.Passed = false
			ar.Actual = "unsupported assertion type"
		}

		result.Assertions = append(result.Assertions, ar)
		if !ar.Passed {
			result.Passed = false
			if assertion.Critical {
				result.ErrorReason = fmt.Sprintf("关键断言失败: %s", assertion.Value)
			}
		}
	}

	return result
}

// defaultEvalSamples 返回默认评测集（至少 8 个样本）
func defaultEvalSamples() []types.EvalSample {
	return []types.EvalSample{
		// 正常样本
		{
			ID:          "normal-01",
			Type:        "normal",
			Description: "go-logger 缺少 WithContext",
			Input:       `ycLogger.Info("msg")`,
			Expected:    `ycLogger.WithContext(ctx).Info("msg")`,
			Assertions: []types.EvalAssertion{
				{Type: "contains", Target: "output", Value: "WithContext(ctx)", Critical: true},
			},
		},
		{
			ID:          "normal-02",
			Type:        "normal",
			Description: "gorm 缺少 WithContext",
			Input:       `db.First(&u, id)`,
			Expected:    `db.WithContext(ctx).First(&u, id)`,
			Assertions: []types.EvalAssertion{
				{Type: "contains", Target: "output", Value: "WithContext(ctx)", Critical: true},
			},
		},
		// 边界样本
		{
			ID:          "boundary-01",
			Type:        "boundary",
			Description: "已有 WithContext 不应重复修改",
			Input:       `ycLogger.WithContext(ctx).Info("msg")`,
			Expected:    `ycLogger.WithContext(ctx).Info("msg")`,
			Assertions: []types.EvalAssertion{
				{Type: "not_contains", Target: "output", Value: "WithContext(ctx).WithContext(ctx)", Critical: true},
			},
		},
		{
			ID:          "boundary-02",
			Type:        "boundary",
			Description: "函数内无 ctx 应跳过",
			Input:       `func f() { ycLogger.Info("msg") }`,
			Expected:    `skipped`,
			Assertions: []types.EvalAssertion{
				{Type: "contains", Target: "output", Value: "skip", Critical: false},
			},
		},
		// 故障样本
		{
			ID:          "fault-01",
			Type:        "fault",
			Description: "编译失败应回滚",
			Input:       `invalid go code`,
			Expected:    `rollback`,
			Assertions: []types.EvalAssertion{
				{Type: "not_contains", Target: "output", Value: "panic", Critical: true},
			},
		},
		{
			ID:          "fault-02",
			Type:        "fault",
			Description: "路径越界应拒绝",
			Input:       `/etc/passwd`,
			Expected:    `path validation failed`,
			Assertions: []types.EvalAssertion{
				{Type: "contains", Target: "output", Value: "安全校验失败", Critical: true},
			},
		},
		// 对抗样本
		{
			ID:          "adversarial-01",
			Type:        "adversarial",
			Description: "seelog 调用应标记为需迁移",
			Input:       `seelog.Info("msg")`,
			Expected:    `标记为需迁移`,
			Assertions: []types.EvalAssertion{
				{Type: "contains", Target: "output", Value: "seelog", Critical: false},
			},
		},
		{
			ID:          "adversarial-02",
			Type:        "adversarial",
			Description: "resty 缺少 SetContext 应标记",
			Input:       `resty.New().R().Get(url)`,
			Expected:    `标记为需添加 SetContext`,
			Assertions: []types.EvalAssertion{
				{Type: "contains", Target: "output", Value: "resty", Critical: false},
			},
		},
	}
}

// ========== 安全门禁（Day 9） ==========

// SafetyChecker 安全检查器
type SafetyChecker struct {
	gate types.SafetyGate
}

// NewSafetyChecker 创建安全检查器
func NewSafetyChecker(gate types.SafetyGate) *SafetyChecker {
	return &SafetyChecker{gate: gate}
}

// CheckFileChange 检查文件变更是否符合安全门禁
func (sc *SafetyChecker) CheckFileChange(changedFiles []string) *types.SafetyCheckResult {
	result := &types.SafetyCheckResult{Passed: true}

	// 检查修改文件数
	if sc.gate.MaxFilesChanged > 0 && len(changedFiles) > sc.gate.MaxFilesChanged {
		result.Passed = false
		result.Violations = append(result.Violations, types.SafetyViolation{
			Type:     "max_files",
			Detail:   fmt.Sprintf("修改文件数 %d 超过限制 %d", len(changedFiles), sc.gate.MaxFilesChanged),
			Severity: "high",
		})
	}

	// 检查保护区
	for _, file := range changedFiles {
		for _, protected := range sc.gate.ProtectedPaths {
			if strings.HasPrefix(file, protected) || strings.Contains(file, protected) {
				result.Passed = false
				result.Violations = append(result.Violations, types.SafetyViolation{
					Type:     "protected_path",
					Path:     file,
					Detail:   fmt.Sprintf("文件 %s 在保护区内", file),
					Severity: "critical",
				})
			}
		}
	}

	return result
}

// CheckApprovalRequired 检查操作是否需要人工审批
func (sc *SafetyChecker) CheckApprovalRequired(action string) bool {
	for _, approval := range sc.gate.RequireApproval {
		if strings.Contains(action, approval) {
			return true
		}
	}
	return false
}

// DefaultSafetyGate 返回默认安全门禁配置
func DefaultSafetyGate() types.SafetyGate {
	return types.SafetyGate{
		ProtectedPaths: []string{
			"infra/prod/",
			".env",
			".env.production",
			"secrets/",
			"deploy/",
		},
		MaxFilesChanged:   20,
		RequireApproval:   []string{"dependency_upgrade", "db_migration", "external_publish"},
		SecretScanEnabled: true,
	}
}

// ========== 收敛度量（Day 4） ==========

// ConvergenceChecker 收敛检查器
type ConvergenceChecker struct {
	maxNoProgressRounds int // 最大无进展轮次
}

// NewConvergenceChecker 创建收敛检查器
func NewConvergenceChecker(maxNoProgressRounds int) *ConvergenceChecker {
	if maxNoProgressRounds <= 0 {
		maxNoProgressRounds = 2 // 默认连续 2 轮无进展则停止
	}
	return &ConvergenceChecker{maxNoProgressRounds: maxNoProgressRounds}
}

// ConvergenceResult 收敛检查结果
type ConvergenceResult struct {
	ShouldStop       bool    `json:"should_stop"`
	Reason           string  `json:"reason"`
	NoProgressRounds int     `json:"no_progress_rounds"`
	CurrentGap       int     `json:"current_gap"`     // 当前问题数
	PreviousGap      int     `json:"previous_gap"`    // 上轮问题数
	GapReduction     float64 `json:"gap_reduction"`   // 差距缩减率
}

// Check 检查是否应该停止循环
// 基于差距函数：每轮比较问题数是否下降
func (cc *ConvergenceChecker) Check(iteration int, currentIssues, previousIssues int, consecutiveNoProgress int) *ConvergenceResult {
	gapReduction := 0.0
	if previousIssues > 0 {
		gapReduction = float64(previousIssues-currentIssues) / float64(previousIssues)
	}

	result := &ConvergenceResult{
		CurrentGap:       currentIssues,
		PreviousGap:      previousIssues,
		GapReduction:     gapReduction,
		NoProgressRounds: consecutiveNoProgress,
	}

	// 成功：所有问题已修复
	if currentIssues == 0 {
		result.ShouldStop = true
		result.Reason = "所有问题已修复"
		return result
	}

	// 连续无进展
	if consecutiveNoProgress >= cc.maxNoProgressRounds {
		result.ShouldStop = true
		result.Reason = fmt.Sprintf("连续 %d 轮无进展，停止循环", consecutiveNoProgress)
		return result
	}

	return result
}

// truncateStr 截断字符串
func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
