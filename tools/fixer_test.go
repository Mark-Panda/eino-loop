package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mark-Panda/eino-loop/types"
)

func TestApplyLogFix_SlogWithContext(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "handler.go")

	original := `package main

import (
	"context"
	"log/slog"
)

func HandleRequest(ctx context.Context) {
	slog.Info("processing request")
}
`
	os.WriteFile(filePath, []byte(original), 0644)

	analysis := types.AnalyzeResult{
		Location: types.FileLocation{
			File:     filePath,
			Line:     9,
			FuncName: "slog.Info",
		},
		LogLib:     "slog",
		FixType:    "context_param",
		HasCtx:     true,
		NearestCtx: "ctx",
		RiskLevel:  "low",
	}

	applied, _, err := ApplyLogFix(context.Background(), tmpDir, analysis)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !applied {
		t.Fatal("expected fix to be applied")
	}

	// 读取修复后的文件
	content, _ := os.ReadFile(filePath)
	fixed := string(content)

	if !strings.Contains(fixed, "slog.InfoContext(ctx,") {
		t.Errorf("expected slog.InfoContext(ctx, ...) in fixed file, got:\n%s", fixed)
	}
	if strings.Contains(fixed, "slog.Info(") {
		t.Errorf("expected slog.Info to be replaced, still found in:\n%s", fixed)
	}
}

func TestApplyLogFix_SlogWithArgs(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "service.go")

	original := `package main

import (
	"context"
	"log/slog"
)

func Process(ctx context.Context) {
	slog.Error("failed", "err", "test")
}
`
	os.WriteFile(filePath, []byte(original), 0644)

	analysis := types.AnalyzeResult{
		Location: types.FileLocation{
			File:     filePath,
			Line:     9,
			FuncName: "slog.Error",
		},
		LogLib:     "slog",
		FixType:    "context_param",
		HasCtx:     true,
		NearestCtx: "ctx",
		RiskLevel:  "low",
	}

	applied, _, err := ApplyLogFix(context.Background(), tmpDir, analysis)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !applied {
		t.Fatal("expected fix to be applied")
	}

	content, _ := os.ReadFile(filePath)
	fixed := string(content)

	if !strings.Contains(fixed, "slog.ErrorContext(ctx,") {
		t.Errorf("expected slog.ErrorContext(ctx, ...) in fixed file, got:\n%s", fixed)
	}
}

func TestApplyLogFix_FiberLog(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "fiber.go")

	original := `package main

import "github.com/gofiber/fiber/v2/log"

func handler() {
	log.Info("request received")
}
`
	os.WriteFile(filePath, []byte(original), 0644)

	analysis := types.AnalyzeResult{
		Location: types.FileLocation{
			File:     filePath,
			Line:     6,
			FuncName: "log.Info",
		},
		LogLib:     "fiber",
		FixType:    "logger_receiver",
		HasCtx:     false,
		NearestCtx: "c",
		RiskLevel:  "low",
	}

	applied, _, err := ApplyLogFix(context.Background(), tmpDir, analysis)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !applied {
		t.Fatal("expected fix to be applied")
	}

	content, _ := os.ReadFile(filePath)
	fixed := string(content)

	if !strings.Contains(fixed, "log.WithContext(c).Info(") {
		t.Errorf("expected log.WithContext(c).Info(...) in fixed file, got:\n%s", fixed)
	}
}

func TestApplyLogFix_SkipWhenNoCtx(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "worker.go")

	original := `package main

import "log/slog"

func BackgroundTask() {
	slog.Info("running")
}
`
	os.WriteFile(filePath, []byte(original), 0644)

	analysis := types.AnalyzeResult{
		Location: types.FileLocation{
			File:     filePath,
			Line:     6,
			FuncName: "slog.Info",
		},
		LogLib:     "slog",
		FixType:    "skip",
		HasCtx:     false,
		NearestCtx: "",
		RiskLevel:  "high",
	}

	applied, _, err := ApplyLogFix(context.Background(), tmpDir, analysis)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if applied {
		t.Error("expected fix to be skipped when no ctx available")
	}
}

func TestApplyLogFix_MultipleLogCalls(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "multi.go")

	original := `package main

import (
	"context"
	"log/slog"
)

func DoWork(ctx context.Context) {
	slog.Info("starting")
	slog.Info("step 1")
	slog.Error("oops", "err", "test")
}
`
	os.WriteFile(filePath, []byte(original), 0644)

	logFuncs := []LogFunc{
		{Library: "slog", Functions: []string{"Info", "Error"}, CtxForm: "Context"},
	}

	// 迭代修复所有调用 — 每次修复后重新扫描，因为行号会发生变化
	for i := 0; i < 10; i++ { // 安全上限
		remaining, _ := FindLogsWithoutContext(context.Background(), tmpDir, logFuncs)
		if len(remaining) == 0 {
			break
		}

		// 修复第一个剩余问题
		loc := remaining[0]
		analysis := types.AnalyzeResult{
			Location:    loc,
			LogLib:      "slog",
			FixType:     "context_param",
			HasCtx:      true,
			NearestCtx:  "ctx",
			RiskLevel:   "low",
		}
		_, _, err := ApplyLogFix(context.Background(), tmpDir, analysis)
		if err != nil {
			t.Fatalf("fix %s:%d: %v", loc.File, loc.Line, err)
		}
	}

	content, _ := os.ReadFile(filePath)
	fixed := string(content)

	if strings.Contains(fixed, "slog.Info(") || strings.Contains(fixed, "slog.Error(") {
		t.Errorf("expected all slog calls to be fixed, still found in:\n%s", fixed)
	}
	if !strings.Contains(fixed, "slog.InfoContext(ctx,") {
		t.Errorf("expected slog.InfoContext in fixed file:\n%s", fixed)
	}
	if !strings.Contains(fixed, "slog.ErrorContext(ctx,") {
		t.Errorf("expected slog.ErrorContext in fixed file:\n%s", fixed)
	}
}
