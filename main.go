package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/Mark-Panda/eino-loop/agent"
	"github.com/Mark-Panda/eino-loop/config"
)

func main() {
	cfg := config.Load()

	if cfg.DryRun {
		log.Println("[DRY-RUN] Only scanning, no fixes will be applied")
	}

	loop, err := agent.BuildLoopGraph(cfg)
	if err != nil {
		log.Fatalf("Failed to build loop graph: %v", err)
	}

	// 立即运行一次
	runLoop(loop, cfg)

	// 然后按间隔运行
	ticker := time.NewTicker(cfg.ScanInterval)
	defer ticker.Stop()

	for range ticker.C {
		runLoop(loop, cfg)
	}
}

func runLoop(loop agent.LoopRunner, cfg *config.Config) {
	log.Println("=== Starting loop execution ===")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	report, err := loop.Invoke(ctx, agent.LoopInput{RepoRoot: cfg.RepoRoot})
	if err != nil {
		log.Printf("Loop execution failed: %v", err)
		return
	}

	// 将报告输出到标准输出
	fmt.Fprintln(os.Stdout, report)
	log.Println("=== Loop execution completed ===")
}
