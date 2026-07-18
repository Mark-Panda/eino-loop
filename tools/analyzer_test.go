package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAnalyzeLogCallsite_SlogWithCtx(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "handler.go")

	content := `package main

import (
	"context"
	"log/slog"
)

func HandleRequest(ctx context.Context) {
	slog.Info("processing request")
}
`
	os.WriteFile(filePath, []byte(content), 0644)

	result, err := AnalyzeLogCallsite(filePath, 9, "slog.Info")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.HasCtx {
		t.Error("expected HasCtx=true")
	}
	if result.NearestCtx != "ctx" {
		t.Errorf("expected NearestCtx='ctx', got '%s'", result.NearestCtx)
	}
	if result.RiskLevel != "low" {
		t.Errorf("expected RiskLevel='low', got '%s'", result.RiskLevel)
	}
	if result.FixType != "context_param" {
		t.Errorf("expected FixType='context_param', got '%s'", result.FixType)
	}
}

func TestAnalyzeLogCallsite_SlogWithoutCtx(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "worker.go")

	content := `package main

import "log/slog"

func BackgroundTask() {
	slog.Info("running background task")
}
`
	os.WriteFile(filePath, []byte(content), 0644)

	result, err := AnalyzeLogCallsite(filePath, 6, "slog.Info")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.HasCtx {
		t.Error("expected HasCtx=false")
	}
	if result.RiskLevel != "high" {
		t.Errorf("expected RiskLevel='high', got '%s'", result.RiskLevel)
	}
	if result.FixType != "skip" {
		t.Errorf("expected FixType='skip', got '%s'", result.FixType)
	}
}

func TestAnalyzeLogCallsite_FiberLog(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "fiber.go")

	// Note: no ctx in function → risk=high, fixType=skip
	content := `package main

import "github.com/gofiber/fiber/v2/log"

func handler() {
	log.Info("request received")
}
`
	os.WriteFile(filePath, []byte(content), 0644)

	result, err := AnalyzeLogCallsite(filePath, 6, "log.Info")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.LogLib != "fiber" {
		t.Errorf("expected LogLib='fiber', got '%s'", result.LogLib)
	}
	// No ctx available → skip
	if result.FixType != "skip" {
		t.Errorf("expected FixType='skip', got '%s'", result.FixType)
	}
	if result.RiskLevel != "high" {
		t.Errorf("expected RiskLevel='high', got '%s'", result.RiskLevel)
	}
}

func TestAnalyzeLogCallsite_FiberLogWithCtx(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "fiber.go")

	content := `package main

import "github.com/gofiber/fiber/v2/log"

func handler(c *fiber.Ctx) error {
	log.Info("request received")
	return nil
}
`
	os.WriteFile(filePath, []byte(content), 0644)

	result, err := AnalyzeLogCallsite(filePath, 7, "log.Info")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.LogLib != "fiber" {
		t.Errorf("expected LogLib='fiber', got '%s'", result.LogLib)
	}
	if result.FixType != "logger_receiver" {
		t.Errorf("expected FixType='logger_receiver', got '%s'", result.FixType)
	}
}

func TestAnalyzeLogCallsite_CtxInBody(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "service.go")

	content := `package main

import (
	"context"
	"log/slog"
)

func Process() {
	ctx := context.Background()
	slog.Info("processing")
}
`
	os.WriteFile(filePath, []byte(content), 0644)

	result, err := AnalyzeLogCallsite(filePath, 10, "slog.Info")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.NearestCtx != "ctx" {
		t.Errorf("expected NearestCtx='ctx', got '%s'", result.NearestCtx)
	}
	if result.RiskLevel != "medium" {
		t.Errorf("expected RiskLevel='medium', got '%s'", result.RiskLevel)
	}
}

func TestDetermineFixType(t *testing.T) {
	tests := []struct {
		lib  string
		want string
	}{
		{"slog", "context_param"},
		{"fiber", "logger_receiver"},
		{"logrus", "logger_receiver"},
		{"unknown", "skip"},
	}

	for _, tt := range tests {
		got := determineFixType(tt.lib)
		if got != tt.want {
			t.Errorf("determineFixType(%q) = %q, want %q", tt.lib, got, tt.want)
		}
	}
}

func TestDetectLogLib(t *testing.T) {
	tests := []struct {
		funcName string
		want     string
	}{
		{"slog.Info", "slog"},
		{"slog.Error", "slog"},
		{"log.Info", "fiber"},
		{"log.Fatal", "fiber"},
		{"Info", "logrus"},
		{"Error", "logrus"},
	}

	for _, tt := range tests {
		got := detectLogLib(tt.funcName)
		if got != tt.want {
			t.Errorf("detectLogLib(%q) = %q, want %q", tt.funcName, got, tt.want)
		}
	}
}
