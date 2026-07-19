package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
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

	// 配置验证
	if err := cfg.Validate(); err != nil {
		log.Fatalf("[配置错误] %v", err)
	}

	if cfg.DryRun {
		log.Println("[DRY-RUN] 仅扫描模式，不会执行修复")
	}

	// 检查 LLM 配置
	if cfg.LLMAPIKey == "" {
		log.Fatal("[错误] 未配置 LLM API 密钥，请设置 EINO_LOOP_LLM_API_KEY 环境变量")
	}

	// 优雅退出
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("[退出] 收到终止信号，正在优雅退出...")
		cancel()
	}()

	// 创建多 Agent 编排系统
	loop, err := agent.NewMultiAgentLoop(ctx, cfg)
	if err != nil {
		log.Fatalf("创建多 Agent 系统失败: %v", err)
	}

	log.Printf("[MultiAgent] 目标仓库目录: %s", cfg.RepoRoot)
	log.Printf("[MultiAgent] LLM 模型: %s", cfg.LLMModel)
	log.Printf("[MultiAgent] 扫描间隔: %s", cfg.ScanInterval)
	log.Printf("[MultiAgent] SubAgent: scanner, analyzer, fixer, verifier, reporter")

	// 启动时立即执行一次
	runOnce(ctx, loop, cfg)

	// 定时执行
	ticker := time.NewTicker(cfg.ScanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("[退出] 任务完成")
			return
		case <-ticker.C:
			runOnce(ctx, loop, cfg)
		}
	}
}

// runOnce 执行一次完整的扫描-修复流程
func runOnce(ctx context.Context, loop *agent.MultiAgentLoop, cfg *config.Config) {
	if ctx.Err() != nil {
		return
	}

	log.Println("=== 开始执行循环 ===")
	start := time.Now()

	// 构建任务描述
	task := agent.BuildTask(cfg)

	// 运行多 Agent 任务
	report, err := loop.Run(ctx, task)
	if err != nil {
		log.Printf("[错误] 多 Agent 执行失败: %v", err)
		return
	}

	elapsed := time.Since(start)
	log.Printf("=== 循环执行完成 (耗时: %s) ===", elapsed)

	// 输出报告
	fmt.Fprintln(os.Stdout, "\n"+report)
}
