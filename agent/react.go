package agent

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
	openai "github.com/cloudwego/eino-ext/components/model/openai"

	"github.com/Mark-Panda/eino-loop/config"
	"github.com/Mark-Panda/eino-loop/prompts"
	"github.com/Mark-Panda/eino-loop/tools"
)

// LoopAgent 是基于 eino ReAct Agent 的循环修复 Agent
type LoopAgent struct {
	reactAgent *react.Agent
	cfg        *config.Config
}

// NewLoopAgent 创建一个新的 ReAct Agent 实例
// Agent 包含所有注册的工具，由 LLM 决策每一步调用哪个工具
func NewLoopAgent(ctx context.Context, cfg *config.Config) (*LoopAgent, error) {
	// 创建 ChatModel（OpenAI 兼容）
	chatModel, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		APIKey:  cfg.LLMAPIKey,
		Model:   cfg.LLMModel,
		BaseURL: cfg.LLMBaseURL,
	})
	if err != nil {
		return nil, fmt.Errorf("创建 ChatModel 失败: %w", err)
	}

	// 注册所有工具
	allTools := tools.RegisterAll(cfg)

	// 创建 ReAct Agent
	reactAgent, err := react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel: chatModel,
		ToolsConfig: compose.ToolsNodeConfig{
			Tools: allTools,
		},
		MaxStep: cfg.LLMMaxStep,
	})
	if err != nil {
		return nil, fmt.Errorf("创建 ReAct Agent 失败: %w", err)
	}

	return &LoopAgent{
		reactAgent: reactAgent,
		cfg:        cfg,
	}, nil
}

// Run 执行 Agent 任务
// 给 Agent 一个任务描述，它会自主决策调用哪些工具来完成任务
func (a *LoopAgent) Run(ctx context.Context, task string) (string, error) {
	// 构建消息列表：系统提示词 + 用户任务
	messages := []*schema.Message{
		schema.SystemMessage(prompts.SystemPrompt),
		schema.UserMessage(task),
	}

	// 调用 Agent 执行（Agent 会自主决策调用工具）
	resp, err := a.reactAgent.Generate(ctx, messages)
	if err != nil {
		return "", fmt.Errorf("Agent 执行失败: %w", err)
	}

	return resp.Content, nil
}

// BuildTask 构建标准的修复任务描述
func BuildTask(cfg *config.Config) string {
	return fmt.Sprintf(
		"请扫描 %s 目录下的所有 Go 代码仓库，检查日志调用中缺少 WithContext 的问题并自动修复。\n\n"+
			"目标分支: %s\n"+
			"最大仓库数: %d\n"+
			"最大重试次数: %d\n\n"+
			"请按照以下步骤操作：\n"+
			"1. 用 scan_repositories 扫描仓库\n"+
			"2. 对每个仓库用 pull_latest 拉取最新代码\n"+
			"3. 用 find_log_issues 检测问题\n"+
			"4. 对每个问题用 analyze_callsite 分析\n"+
			"5. 如果需要修复，用 apply_fix 执行\n"+
			"6. 用 verify_compile 验证编译\n"+
			"7. 用 verify_rescan 检查是否全部修复\n"+
			"8. 全部通过后用 commit_and_push 提交\n"+
			"9. 最后用 generate_report 生成报告\n"+
			"10. 如果飞书已启用，用 send_feishu 发送通知",
		cfg.RepoRoot,
		cfg.TargetBranch,
		cfg.MaxRepos,
		cfg.MaxRetries,
	)
}
