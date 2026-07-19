package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/schema"
	"github.com/Mark-Panda/eino-loop/types"
)

// ========== 复用 eino callbacks 实现结构化日志 ==========

// LoopCallbackHandler 实现 eino callbacks.Handler，自动采集循环事件。
// 替代自建的 LoopLogger，复用 eino 的回调机制。
type LoopCallbackHandler struct {
	taskID    string
	logFile   *os.File
	encoder   *json.Encoder
	iteration int
}

// NewLoopCallbackHandler 创建基于 eino callbacks 的循环事件处理器
func NewLoopCallbackHandler(logDir, taskID string) (*LoopCallbackHandler, error) {
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, fmt.Errorf("创建日志目录失败: %w", err)
	}

	logPath := filepath.Join(logDir, fmt.Sprintf("%s.jsonl", taskID))
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("打开日志文件失败: %w", err)
	}

	return &LoopCallbackHandler{
		taskID:  taskID,
		logFile: f,
		encoder: json.NewEncoder(f),
	}, nil
}

// OnStart eino 回调：组件开始执行时触发
func (h *LoopCallbackHandler) OnStart(ctx context.Context, info *callbacks.RunInfo, input callbacks.CallbackInput) context.Context {
	h.emit(types.LoopEvent{
		Level:     "INFO",
		Event:     fmt.Sprintf("%s.started", info.Component),
		Stage:     info.Name,
		Action:    "start",
		Outcome:   "running",
		Iteration: h.iteration,
	})
	return ctx
}

// OnEnd eino 回调：组件执行完成时触发
func (h *LoopCallbackHandler) OnEnd(ctx context.Context, info *callbacks.RunInfo, output callbacks.CallbackOutput) context.Context {
	h.emit(types.LoopEvent{
		Level:     "INFO",
		Event:     fmt.Sprintf("%s.completed", info.Component),
		Stage:     info.Name,
		Action:    "end",
		Outcome:   "success",
		Iteration: h.iteration,
	})
	return ctx
}

// OnError eino 回调：组件执行出错时触发
func (h *LoopCallbackHandler) OnError(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
	decision := classifyError(err.Error())

	h.emit(types.LoopEvent{
		Level:     "ERROR",
		Event:     fmt.Sprintf("%s.error", info.Component),
		Stage:     info.Name,
		Action:    "error",
		Outcome:   decision.Strategy,
		Error:     err.Error(),
		ErrorCode: string(decision.Category),
		Iteration: h.iteration,
		Extra: map[string]interface{}{
			"error_category": string(decision.Category),
			"strategy":       decision.Strategy,
			"max_retries":    decision.MaxRetries,
		},
	})
	return ctx
}

// OnStartWithStreamInput 流式输入回调
func (h *LoopCallbackHandler) OnStartWithStreamInput(ctx context.Context, info *callbacks.RunInfo, input *schema.StreamReader[callbacks.CallbackInput]) context.Context {
	return ctx
}

// OnEndWithStreamOutput 流式输出回调
func (h *LoopCallbackHandler) OnEndWithStreamOutput(ctx context.Context, info *callbacks.RunInfo, output *schema.StreamReader[callbacks.CallbackOutput]) context.Context {
	return ctx
}

// SetIteration 设置当前迭代轮次
func (h *LoopCallbackHandler) SetIteration(iteration int) {
	h.iteration = iteration
}

// emit 发送结构化事件
func (h *LoopCallbackHandler) emit(event types.LoopEvent) {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}
	if event.TaskID == "" {
		event.TaskID = h.taskID
	}
	h.encoder.Encode(event)
}

// Close 关闭处理器
func (h *LoopCallbackHandler) Close() {
	if h.logFile != nil {
		h.logFile.Close()
	}
}

// ========== 错误分类（集成到 callbacks.OnError） ==========

// classifyError 对错误进行分类，返回恢复策略
func classifyError(errMsg string) types.ErrorDecision {
	transientPatterns := []string{"timeout", "connection refused", "429", "503", "502", "EOF", "i/o timeout"}
	for _, p := range transientPatterns {
		if containsIgnoreCase(errMsg, p) {
			return types.ErrorDecision{Category: types.ErrorTransient, Strategy: "retry", MaxRetries: 3, BackoffMs: 1000}
		}
	}

	deterministicPatterns := []string{"syntax error", "cannot compile", "undefined:", "imported and not used"}
	for _, p := range deterministicPatterns {
		if containsIgnoreCase(errMsg, p) {
			return types.ErrorDecision{Category: types.ErrorDeterministic, Strategy: "fix", MaxRetries: 2}
		}
	}

	businessPatterns := []string{"permission denied", "unauthorized", "forbidden", "conflict"}
	for _, p := range businessPatterns {
		if containsIgnoreCase(errMsg, p) {
			return types.ErrorDecision{Category: types.ErrorBusiness, Strategy: "stop"}
		}
	}

	return types.ErrorDecision{Category: types.ErrorUnknown, Strategy: "escalate"}
}

func containsIgnoreCase(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		match := true
		for j := 0; j < len(substr); j++ {
			sc, tc := s[i+j], substr[j]
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

// ========== 复用 eino CheckPointStore 实现状态持久化 ==========

// FileCheckPointStore 实现 eino 的 CheckPointStore 接口。
// 使用 Get/Set 方法，与 eino ADK Runner 的 checkpoint 机制完全兼容。
type FileCheckPointStore struct {
	stateDir string
}

// NewFileCheckPointStore 创建基于文件的 checkpoint 存储
func NewFileCheckPointStore(stateDir string) *FileCheckPointStore {
	return &FileCheckPointStore{stateDir: stateDir}
}

// Get 获取 checkpoint（实现 eino core.CheckPointStore 接口）
func (s *FileCheckPointStore) Get(ctx context.Context, checkPointID string) ([]byte, bool, error) {
	statePath := filepath.Join(s.stateDir, fmt.Sprintf("%s.json", checkPointID))
	data, err := os.ReadFile(statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("读取 checkpoint 失败: %w", err)
	}
	return data, true, nil
}

// Set 保存 checkpoint（实现 eino core.CheckPointStore 接口）
func (s *FileCheckPointStore) Set(ctx context.Context, checkPointID string, checkPoint []byte) error {
	if err := os.MkdirAll(s.stateDir, 0755); err != nil {
		return fmt.Errorf("创建状态目录失败: %w", err)
	}
	statePath := filepath.Join(s.stateDir, fmt.Sprintf("%s.json", checkPointID))
	return os.WriteFile(statePath, checkPoint, 0644)
}

// HasCheckpoint 检查是否有可恢复的 checkpoint
func (s *FileCheckPointStore) HasCheckpoint(checkPointID string) bool {
	statePath := filepath.Join(s.stateDir, fmt.Sprintf("%s.json", checkPointID))
	_, err := os.Stat(statePath)
	return err == nil
}

// Delete 删除 checkpoint
func (s *FileCheckPointStore) Delete(ctx context.Context, checkPointID string) error {
	statePath := filepath.Join(s.stateDir, fmt.Sprintf("%s.json", checkPointID))
	return os.Remove(statePath)
}
