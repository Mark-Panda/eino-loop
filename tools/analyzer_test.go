package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAnalyzeLogCallsite_GoLoggerWithCtx(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "handler.go")

	content := `package main

import (
	"context"
	ycLogger "gitlab.yc345.tv/backend/go-logger/logger"
)

func HandleRequest(ctx context.Context) {
	ycLogger.Info("processing request")
}
`
	os.WriteFile(filePath, []byte(content), 0644)

	result, err := AnalyzeLogCallsite(filePath, 10, "ycLogger.Info")
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}

	if !result.HasCtx {
		t.Error("期望 HasCtx=true")
	}
	if result.NearestCtx != "ctx" {
		t.Errorf("期望 NearestCtx='ctx'，得到 '%s'", result.NearestCtx)
	}
	if result.RiskLevel != "low" {
		t.Errorf("期望 RiskLevel='low'，得到 '%s'", result.RiskLevel)
	}
	if result.FixType != "logger_receiver" {
		t.Errorf("期望 FixType='logger_receiver'，得到 '%s'", result.FixType)
	}
}

func TestAnalyzeLogCallsite_GoLoggerWithoutCtx(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "worker.go")

	content := `package main

import ycLogger "gitlab.yc345.tv/backend/go-logger/logger"

func BackgroundTask() {
	ycLogger.Info("running background task")
}
`
	os.WriteFile(filePath, []byte(content), 0644)

	result, err := AnalyzeLogCallsite(filePath, 6, "ycLogger.Info")
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}

	if result.HasCtx {
		t.Error("期望 HasCtx=false")
	}
	if result.RiskLevel != "high" {
		t.Errorf("期望 RiskLevel='high'，得到 '%s'", result.RiskLevel)
	}
	if result.FixType != "skip" {
		t.Errorf("期望 FixType='skip'，得到 '%s'", result.FixType)
	}
}

func TestAnalyzeLogCallsite_GormWithCtx(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "repo.go")

	content := `package data

import (
	"context"
	"gorm.io/gorm"
)

type Data struct {
	db *gorm.DB
}

func (d *Data) GetUser(ctx context.Context, id int64) error {
	var user User
	return d.db.Model(&User{}).Where("id = ?", id).First(&user).Error
}
`
	os.WriteFile(filePath, []byte(content), 0644)

	result, err := AnalyzeLogCallsite(filePath, 15, "gorm.First")
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}

	if !result.HasCtx {
		t.Error("期望 HasCtx=true")
	}
	if result.LogLib != "gorm" {
		t.Errorf("期望 LogLib='gorm'，得到 '%s'", result.LogLib)
	}
}

func TestDetermineFixType(t *testing.T) {
	tests := []struct {
		lib  string
		want string
	}{
		{"go-logger", "logger_receiver"},
		{"gorm", "logger_receiver"},
		{"slog", "context_param"},
		{"fiber", "logger_receiver"},
		{"logrus", "logger_receiver"},
		{"unknown", "skip"},
	}

	for _, tt := range tests {
		got := determineFixType(tt.lib)
		if got != tt.want {
			t.Errorf("determineFixType(%q) = %q，期望 %q", tt.lib, got, tt.want)
		}
	}
}

func TestDetectLogLib(t *testing.T) {
	tests := []struct {
		funcName string
		want     string
	}{
		{"ycLogger.Info", "go-logger"},
		{"logger.Error", "go-logger"},
		{"gorm.First", "gorm"},
		{"slog.Info", "slog"},
		{"log.Info", "fiber"},
		{"Info", "logrus"},
	}

	for _, tt := range tests {
		got := detectLogLib(tt.funcName)
		if got != tt.want {
			t.Errorf("detectLogLib(%q) = %q，期望 %q", tt.funcName, got, tt.want)
		}
	}
}
