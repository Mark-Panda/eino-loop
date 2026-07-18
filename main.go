package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/joho/godotenv"

	"github.com/Mark-Panda/eino-loop/agent"
	"github.com/Mark-Panda/eino-loop/config"
)

func main() {
	// 加载 .env 文件（如果存在）
	if err := godotenv.Load(); err != nil {
		log.Printf("[启动] 未找到 .env 文件，使用环境变量: %v", err)
	}

	cfg := config.Load()

	if cfg.DryRun {
		log.Println("[DRY-RUN] 仅扫描模式，不会执行修复")
	}

	// 检查 LLM 配置
	if cfg.LLMAPIKey == "" {
		log.Fatal("[错误] 未配置 LLM API 密钥，请设置 EINO_LOOP_LLM_API_KEY 环境变量")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	// 创建 ReAct Agent
	loopAgent, err := agent.NewLoopAgent(ctx, cfg)
	if err != nil {
		log.Fatalf("创建 Agent 失败: %v", err)
	}

	// 构建任务描述
	task := agent.BuildTask(cfg)
	log.Printf("[Agent] 开始执行任务")
	log.Printf("[Agent] 目标仓库目录: %s", cfg.RepoRoot)
	log.Printf("[Agent] LLM 模型: %s", cfg.LLMModel)

	// 运行 Agent（Agent 会自主决策调用工具）
	report, err := loopAgent.Run(ctx, task)
	if err != nil {
		log.Fatalf("Agent 执行失败: %v", err)
	}

	// 输出最终报告
	fmt.Fprintln(os.Stdout, "\n"+report)
	log.Println("[Agent] 任务执行完成")
}
