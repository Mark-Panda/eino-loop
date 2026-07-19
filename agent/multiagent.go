package agent

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	openai "github.com/cloudwego/eino-ext/components/model/openai"

	"github.com/Mark-Panda/eino-loop/config"
	"github.com/Mark-Panda/eino-loop/prompts"
	"github.com/Mark-Panda/eino-loop/tools"
)

// MultiAgentLoop 是基于多 Agent 编排的循环修复系统
type MultiAgentLoop struct {
	runner *adk.Runner
	cfg    *config.Config
}

// NewMultiAgentLoop 创建多 Agent 编排系统
// 架构：主编排 Agent + Scanner/Analyzer/Fixer/Verifier/Reporter SubAgent
func NewMultiAgentLoop(ctx context.Context, cfg *config.Config) (*MultiAgentLoop, error) {
	// 创建共享的 ChatModel
	chatModel, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		APIKey:  cfg.LLMAPIKey,
		Model:   cfg.LLMModel,
		BaseURL: cfg.LLMBaseURL,
	})
	if err != nil {
		return nil, fmt.Errorf("创建 ChatModel 失败: %w", err)
	}

	// 加载 SKILL 内容
	skillContent := loadSkill(cfg)

	// 创建各 SubAgent
	scannerAgent, err := buildScannerAgent(ctx, chatModel, cfg)
	if err != nil {
		return nil, fmt.Errorf("创建 Scanner Agent 失败: %w", err)
	}

	analyzerAgent, err := buildAnalyzerAgent(ctx, chatModel, skillContent)
	if err != nil {
		return nil, fmt.Errorf("创建 Analyzer Agent 失败: %w", err)
	}

	fixerAgent, err := buildFixerAgent(ctx, chatModel, cfg, skillContent)
	if err != nil {
		return nil, fmt.Errorf("创建 Fixer Agent 失败: %w", err)
	}

	verifierAgent, err := buildVerifierAgent(ctx, chatModel, cfg)
	if err != nil {
		return nil, fmt.Errorf("创建 Verifier Agent 失败: %w", err)
	}

	reporterAgent, err := buildReporterAgent(ctx, chatModel, cfg)
	if err != nil {
		return nil, fmt.Errorf("创建 Reporter Agent 失败: %w", err)
	}

	// 创建主编排 Agent，SubAgent 作为 AgentTool 注册
	orchestrator, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "eino_loop_orchestrator",
		Description: "协调多个 SubAgent 完成日志修复任务",
		Instruction: prompts.SystemPrompt,
		Model:       chatModel,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: []tool.BaseTool{
					adk.NewAgentTool(ctx, scannerAgent),
					adk.NewAgentTool(ctx, analyzerAgent),
					adk.NewAgentTool(ctx, fixerAgent),
					adk.NewAgentTool(ctx, verifierAgent),
					adk.NewAgentTool(ctx, reporterAgent),
				},
			},
		},
		Exit: &adk.ExitTool{},
	})
	if err != nil {
		return nil, fmt.Errorf("创建编排 Agent 失败: %w", err)
	}

	// 创建 Runner
	runner := adk.NewRunner(ctx, adk.RunnerConfig{
		Agent: orchestrator,
	})

	return &MultiAgentLoop{
		runner: runner,
		cfg:    cfg,
	}, nil
}

// Run 执行多 Agent 任务
func (m *MultiAgentLoop) Run(ctx context.Context, task string) (string, error) {
	log.Printf("[MultiAgent] 开始执行任务")

	iter := m.runner.Query(ctx, task)

	var result string
	for {
		event, hasMore := iter.Next()
		if !hasMore {
			break
		}
		if event.Err != nil {
			return result, fmt.Errorf("Agent 执行错误: %w", event.Err)
		}
		if event.Output != nil && event.Output.MessageOutput != nil && event.Output.MessageOutput.Message != nil {
			content := event.Output.MessageOutput.Message.Content
			if content != "" {
				result = content
				log.Printf("[MultiAgent] %s: %s", event.AgentName, truncate(content, 200))
			}
		}
	}

	return result, nil
}

// buildScannerAgent 创建 Scanner SubAgent
func buildScannerAgent(ctx context.Context, chatModel *openai.ChatModel, cfg *config.Config) (adk.Agent, error) {
	return adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "scanner_agent",
		Description: "扫描仓库目录、拉取代码、检测缺少 WithContext 的日志调用",
		Instruction: prompts.ScannerAgentInstruction,
		Model:       chatModel,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: []tool.BaseTool{
					tools.RegisterScanTool(cfg),
					tools.RegisterPullTool(cfg),
					tools.RegisterFindIssuesTool(cfg),
				},
			},
		},
	})
}

// buildAnalyzerAgent 创建 Analyzer SubAgent
func buildAnalyzerAgent(ctx context.Context, chatModel *openai.ChatModel, skillContent string) (adk.Agent, error) {
	instruction := prompts.BuildSubAgentInstruction(
		"日志调用分析 Agent",
		prompts.AnalyzerAgentInstruction,
		skillContent,
	)

	return adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "analyzer_agent",
		Description: "分析日志调用点的修复方案，基于 fixing-trace-id-logs SKILL",
		Instruction: instruction,
		Model:       chatModel,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: []tool.BaseTool{
					tools.RegisterAnalyzeTool(),
				},
			},
		},
	})
}

// buildFixerAgent 创建 Fixer SubAgent
func buildFixerAgent(ctx context.Context, chatModel *openai.ChatModel, cfg *config.Config, skillContent string) (adk.Agent, error) {
	instruction := prompts.BuildSubAgentInstruction(
		"代码修复 Agent",
		prompts.FixerAgentInstruction,
		skillContent,
	)

	return adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "fixer_agent",
		Description: "执行 AST 重写修复日志调用，基于 fixing-trace-id-logs SKILL",
		Instruction: instruction,
		Model:       chatModel,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: []tool.BaseTool{
					tools.RegisterFixTool(cfg),
				},
			},
		},
	})
}

// buildVerifierAgent 创建 Verifier SubAgent
func buildVerifierAgent(ctx context.Context, chatModel *openai.ChatModel, cfg *config.Config) (adk.Agent, error) {
	return adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "verifier_agent",
		Description: "验证修复结果：编译、重扫描、回归三级验证",
		Instruction: prompts.VerifierAgentInstruction,
		Model:       chatModel,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: []tool.BaseTool{
					tools.RegisterCompileVerifyTool(),
					tools.RegisterRescanVerifyTool(cfg),
					tools.RegisterRegressionVerifyTool(),
				},
			},
		},
	})
}

// buildReporterAgent 创建 Reporter SubAgent
func buildReporterAgent(ctx context.Context, chatModel *openai.ChatModel, cfg *config.Config) (adk.Agent, error) {
	return adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "reporter_agent",
		Description: "生成修复报告并发送飞书通知",
		Instruction: prompts.ReporterAgentInstruction,
		Model:       chatModel,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: []tool.BaseTool{
					tools.RegisterReportTool(),
					tools.RegisterFeishuTool(cfg),
				},
			},
		},
	})
}

// loadSkill 加载 SKILL 文件内容
func loadSkill(cfg *config.Config) string {
	// 尝试多个可能的 SKILL 路径
	paths := []string{
		filepath.Join(cfg.RepoRoot, ".claude/skills/fixing-trace-id-logs-1.0.0"),
		".claude/skills/fixing-trace-id-logs-1.0.0",
		"skill-fixing-trace-id-logs",
	}

	for _, p := range paths {
		if _, err := os.Stat(filepath.Join(p, "SKILL.md")); err == nil {
			content, err := prompts.LoadSkillContent(p)
			if err == nil {
				log.Printf("[SKILL] 已加载: %s", p)
				return content
			}
		}
	}

	log.Printf("[SKILL] 未找到 SKILL 文件，使用内置规则")
	return ""
}

// truncate 截断字符串
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// BuildTask 构建标准的修复任务描述
func BuildTask(cfg *config.Config) string {
	return fmt.Sprintf(
		"请扫描 %s 目录下的所有 Go 代码仓库，检查日志调用中缺少 WithContext 的问题并自动修复。\n\n"+
			"目标分支: %s\n"+
			"最大仓库数: %d\n"+
			"最大重试次数: %d\n\n"+
			"请按照以下步骤操作：\n"+
			"1. 调用 scanner_agent 扫描仓库并检测问题\n"+
			"2. 调用 analyzer_agent 分析每个问题的修复方案\n"+
			"3. 调用 fixer_agent 执行修复\n"+
			"4. 调用 verifier_agent 验证修复结果\n"+
			"5. 如果验证不通过，回到步骤 2 重新分析（最多 3 轮）\n"+
			"6. 调用 reporter_agent 生成报告并发送通知",
		cfg.RepoRoot,
		cfg.TargetBranch,
		cfg.MaxRepos,
		cfg.MaxRetries,
	)
}
