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

	loop, err := agent.BuildLoopGraph(cfg)
	if err != nil {
		log.Fatalf("构建循环图失败: %v", err)
	}

	// 启动时立即执行一次
	runLoop(loop, cfg)

	// 按配置的间隔定时执行
	ticker := time.NewTicker(cfg.ScanInterval)
	defer ticker.Stop()

	for range ticker.C {
		runLoop(loop, cfg)
	}
}

func runLoop(loop agent.LoopRunner, cfg *config.Config) {
	log.Println("=== 开始执行循环 ===")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	report, err := loop.Invoke(ctx, agent.LoopInput{RepoRoot: cfg.RepoRoot})
	if err != nil {
		log.Printf("循环执行失败: %v", err)
		return
	}

	// 输出报告到终端
	fmt.Fprintln(os.Stdout, report)
	log.Println("=== 循环执行完成 ===")
}
