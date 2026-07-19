package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mark-Panda/eino-loop/types"
)

func TestApplyLogFix_GoLoggerWithContext(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "handler.go")

	original := `package main

import (
	"context"
	ycLogger "gitlab.yc345.tv/backend/go-logger/logger"
)

func HandleRequest(ctx context.Context) {
	ycLogger.Info("processing request")
}
`
	os.WriteFile(filePath, []byte(original), 0644)

	analysis := types.AnalyzeResult{
		Location: types.FileLocation{
			File:     filePath,
			Line:     9,
			FuncName: "ycLogger.Info",
		},
		LogLib:     "go-logger",
		FixType:    "logger_receiver",
		HasCtx:     true,
		NearestCtx: "ctx",
		RiskLevel:  "low",
	}

	applied, _, err := ApplyLogFix(context.Background(), tmpDir, analysis)
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}

	if !applied {
		t.Fatal("期望修复被应用")
	}

	content, _ := os.ReadFile(filePath)
	fixed := string(content)

	if !strings.Contains(fixed, "WithContext(ctx).Info(") {
		t.Errorf("期望 WithContext(ctx).Info(...) 在修复后文件中，得到:\n%s", fixed)
	}
}

func TestApplyLogFix_GormWithContext(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "repo.go")

	// 简化的 gorm 调用，避免未定义类型问题
	original := `package data

import "gorm.io/gorm"

type User struct {
	ID int64
}

type Data struct {
	db *gorm.DB
}

func (d *Data) GetUser(id int64) error {
	var u User
	return d.db.First(&u, id).Error
}
`
	os.WriteFile(filePath, []byte(original), 0644)

	analysis := types.AnalyzeResult{
		Location: types.FileLocation{
			File:     filePath,
			Line:     15,
			FuncName: "gorm.First",
		},
		LogLib:     "gorm",
		FixType:    "logger_receiver",
		HasCtx:     true,
		NearestCtx: "ctx",
		RiskLevel:  "low",
	}

	applied, _, err := ApplyLogFix(context.Background(), tmpDir, analysis)
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}

	if !applied {
		t.Fatal("期望修复被应用")
	}

	content, _ := os.ReadFile(filePath)
	fixed := string(content)

	if !strings.Contains(fixed, "WithContext(ctx)") {
		t.Errorf("期望 WithContext(ctx) 在修复后文件中，得到:\n%s", fixed)
	}
}

func TestApplyLogFix_SkipWhenNoCtx(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "worker.go")

	original := `package main

import ycLogger "gitlab.yc345.tv/backend/go-logger/logger"

func BackgroundTask() {
	ycLogger.Info("running")
}
`
	os.WriteFile(filePath, []byte(original), 0644)

	analysis := types.AnalyzeResult{
		Location: types.FileLocation{
			File:     filePath,
			Line:     6,
			FuncName: "ycLogger.Info",
		},
		LogLib:     "go-logger",
		FixType:    "skip",
		HasCtx:     false,
		NearestCtx: "",
		RiskLevel:  "high",
	}

	applied, _, err := ApplyLogFix(context.Background(), tmpDir, analysis)
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}

	if applied {
		t.Error("期望修复被跳过（无可用 ctx）")
	}
}

func TestApplyLogFix_PathSafetyHook(t *testing.T) {
	tmpDir := t.TempDir()

	// 尝试修改 .git 目录外的文件
	outsidePath := "/tmp/outside/main.go"
	analysis := types.AnalyzeResult{
		Location: types.FileLocation{
			File:     outsidePath,
			Line:     1,
			FuncName: "ycLogger.Info",
		},
		LogLib:     "go-logger",
		FixType:    "logger_receiver",
		HasCtx:     true,
		NearestCtx: "ctx",
		RiskLevel:  "low",
	}

	applied, _, err := ApplyLogFix(context.Background(), tmpDir, analysis)
	if err == nil {
		t.Error("期望路径安全校验失败，但没有错误")
	}
	if applied {
		t.Error("不应修复路径外的文件")
	}
}

func TestApplyLogFix_MultipleGoLoggerCalls(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "multi.go")

	original := `package main

import (
	"context"
	ycLogger "gitlab.yc345.tv/backend/go-logger/logger"
)

func DoWork(ctx context.Context) {
	ycLogger.Info("starting")
	ycLogger.Info("step 1")
	ycLogger.Error("oops", "err", "test")
}
`
	os.WriteFile(filePath, []byte(original), 0644)

	logFuncs := []LogFunc{
		{Library: "go-logger", Functions: []string{"Info", "Error"}, CtxForm: "WithContext"},
	}

	// 迭代修复所有调用
	for i := 0; i < 10; i++ {
		remaining, _ := FindLogsWithoutContext(context.Background(), tmpDir, logFuncs)
		if len(remaining) == 0 {
			break
		}

		loc := remaining[0]
		analysis := types.AnalyzeResult{
			Location:    loc,
			LogLib:      "go-logger",
			FixType:     "logger_receiver",
			HasCtx:      true,
			NearestCtx:  "ctx",
			RiskLevel:   "low",
		}
		_, _, err := ApplyLogFix(context.Background(), tmpDir, analysis)
		if err != nil {
			t.Fatalf("修复 %s:%d 失败: %v", loc.File, loc.Line, err)
		}
	}

	content, _ := os.ReadFile(filePath)
	fixed := string(content)

	if strings.Contains(fixed, "ycLogger.Info(") || strings.Contains(fixed, "ycLogger.Error(") {
		t.Errorf("期望所有 ycLogger 调用已修复，仍发现:\n%s", fixed)
	}
	if !strings.Contains(fixed, "ycLogger.WithContext(ctx).Info(") {
		t.Errorf("期望 ycLogger.WithContext(ctx).Info 在修复后文件中:\n%s", fixed)
	}
	if !strings.Contains(fixed, "ycLogger.WithContext(ctx).Error(") {
		t.Errorf("期望 ycLogger.WithContext(ctx).Error 在修复后文件中:\n%s", fixed)
	}
}
