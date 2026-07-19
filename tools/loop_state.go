package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Mark-Panda/eino-loop/types"
)

// LoopLogger 结构化循环日志器（Day 3 + Day 6）
type LoopLogger struct {
	taskID        string
	correlationID string
	logFile       *os.File
	encoder       *json.Encoder
}

// NewLoopLogger 创建循环日志器
func NewLoopLogger(logDir, taskID string) (*LoopLogger, error) {
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, fmt.Errorf("创建日志目录失败: %w", err)
	}

	logPath := filepath.Join(logDir, fmt.Sprintf("%s.jsonl", taskID))
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("打开日志文件失败: %w", err)
	}

	return &LoopLogger{
		taskID:        taskID,
		correlationID: fmt.Sprintf("c-%s-%d", taskID, time.Now().Unix()),
		logFile:       f,
		encoder:       json.NewEncoder(f),
	}, nil
}

// LogEvent 记录结构化事件
func (l *LoopLogger) LogEvent(event types.LoopEvent) {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}
	if event.TaskID == "" {
		event.TaskID = l.taskID
	}
	if event.CorrelationID == "" {
		event.CorrelationID = l.correlationID
	}
	l.encoder.Encode(event)
}

// LogIteration 记录迭代完成事件
func (l *LoopLogger) LogIteration(iteration int, stage, action, outcome string, durationMs int64, errCode string) {
	l.LogEvent(types.LoopEvent{
		Level:      "INFO",
		Event:      "iteration.completed",
		Iteration:  iteration,
		Stage:      stage,
		Action:     action,
		Outcome:    outcome,
		DurationMs: durationMs,
		ErrorCode:  errCode,
	})
}

// LogToolCall 记录工具调用事件
func (l *LoopLogger) LogToolCall(iteration int, stage, toolName, outcome string, durationMs int64, errCode string) {
	l.LogEvent(types.LoopEvent{
		Level:      "INFO",
		Event:      "tool.call",
		Iteration:  iteration,
		Stage:      stage,
		Action:     toolName,
		Outcome:    outcome,
		DurationMs: durationMs,
		ErrorCode:  errCode,
	})
}

// LogError 记录错误事件
func (l *LoopLogger) LogError(iteration int, stage, action, errMsg, errCode string) {
	l.LogEvent(types.LoopEvent{
		Level:     "ERROR",
		Event:     "error",
		Iteration: iteration,
		Stage:     stage,
		Action:    action,
		Outcome:   "failed",
		Error:     errMsg,
		ErrorCode: errCode,
	})
}

// LogStop 记录停止事件
func (l *LoopLogger) LogStop(iteration int, reason string) {
	l.LogEvent(types.LoopEvent{
		Level:     "INFO",
		Event:     "loop.stopped",
		Iteration: iteration,
		Outcome:   reason,
	})
}

// Close 关闭日志器
func (l *LoopLogger) Close() {
	if l.logFile != nil {
		l.logFile.Close()
	}
}

// ========== 状态机 + Checkpoint（Day 5） ==========

// CheckpointManager checkpoint 管理器
type CheckpointManager struct {
	stateDir string
}

// NewCheckpointManager 创建 checkpoint 管理器
func NewCheckpointManager(stateDir string) *CheckpointManager {
	return &CheckpointManager{stateDir: stateDir}
}

// Save 保存循环状态到 checkpoint
func (cm *CheckpointManager) Save(state *types.LoopState) error {
	if err := os.MkdirAll(cm.stateDir, 0755); err != nil {
		return fmt.Errorf("创建状态目录失败: %w", err)
	}

	state.UpdatedAt = time.Now()
	statePath := filepath.Join(cm.stateDir, fmt.Sprintf("%s.json", state.TaskID))

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化状态失败: %w", err)
	}

	return os.WriteFile(statePath, data, 0644)
}

// Load 从 checkpoint 恢复循环状态
func (cm *CheckpointManager) Load(taskID string) (*types.LoopState, error) {
	statePath := filepath.Join(cm.stateDir, fmt.Sprintf("%s.json", taskID))
	data, err := os.ReadFile(statePath)
	if err != nil {
		return nil, fmt.Errorf("读取 checkpoint 失败: %w", err)
	}

	var state types.LoopState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("解析 checkpoint 失败: %w", err)
	}

	return &state, nil
}

// HasCheckpoint 检查是否有可恢复的 checkpoint
func (cm *CheckpointManager) HasCheckpoint(taskID string) bool {
	statePath := filepath.Join(cm.stateDir, fmt.Sprintf("%s.json", taskID))
	_, err := os.Stat(statePath)
	return err == nil
}

// Delete 删除 checkpoint
func (cm *CheckpointManager) Delete(taskID string) error {
	statePath := filepath.Join(cm.stateDir, fmt.Sprintf("%s.json", taskID))
	return os.Remove(statePath)
}

// ========== 错误分类与恢复（Day 7） ==========

// ErrorClassifier 错误分类器
type ErrorClassifier struct{}

// Classify 对错误进行分类，返回决策
func (ec *ErrorClassifier) Classify(errMsg string) types.ErrorDecision {
	// 瞬时错误
	if isTransientError(errMsg) {
		return types.ErrorDecision{
			Category:   types.ErrorTransient,
			Strategy:   "retry",
			MaxRetries: 3,
			BackoffMs:  1000,
			Reason:     "瞬时错误，限次重试 + 指数退避",
		}
	}

	// 确定性错误（编译错误、语法错误）
	if isDeterministicError(errMsg) {
		return types.ErrorDecision{
			Category:   types.ErrorDeterministic,
			Strategy:   "fix",
			MaxRetries: 2,
			BackoffMs:  0,
			Reason:     "确定性错误，修正代码后重新验证",
		}
	}

	// 业务拒绝
	if isBusinessError(errMsg) {
		return types.ErrorDecision{
			Category:   types.ErrorBusiness,
			Strategy:   "stop",
			MaxRetries: 0,
			BackoffMs:  0,
			Reason:     "业务拒绝，停止并请求授权",
		}
	}

	// 未知错误
	return types.ErrorDecision{
		Category:   types.ErrorUnknown,
		Strategy:   "escalate",
		MaxRetries: 0,
		BackoffMs:  0,
		Reason:     "未知错误，冻结副作用并人工接管",
	}
}

func isTransientError(msg string) bool {
	transientPatterns := []string{
		"timeout", "connection refused", "429", "503", "502",
		"temporary", "unavailable", "EOF", "i/o timeout",
	}
	for _, p := range transientPatterns {
		if containsIgnoreCase(msg, p) {
			return true
		}
	}
	return false
}

func isDeterministicError(msg string) bool {
	deterministicPatterns := []string{
		"syntax error", "cannot compile", "undefined:",
		"imported and not used", "declared but not used",
		"type mismatch", "cannot use",
	}
	for _, p := range deterministicPatterns {
		if containsIgnoreCase(msg, p) {
			return true
		}
	}
	return false
}

func isBusinessError(msg string) bool {
	businessPatterns := []string{
		"permission denied", "unauthorized", "forbidden",
		"conflict", "precondition failed", "not allowed",
	}
	for _, p := range businessPatterns {
		if containsIgnoreCase(msg, p) {
			return true
		}
	}
	return false
}

func containsIgnoreCase(s, substr string) bool {
	return len(s) >= len(substr) &&
		(s == substr || len(s) > 0 && containsLower(s, substr))
}

func containsLower(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		match := true
		for j := 0; j < len(substr); j++ {
			sc := s[i+j]
			tc := substr[j]
			if sc >= 'A' && sc <= 'Z' {
				sc += 32
			}
			if tc >= 'A' && tc <= 'Z' {
				tc += 32
			}
			if sc != tc {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
