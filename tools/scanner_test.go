package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestScanRepositories(t *testing.T) {
	// 创建包含假仓库的临时目录
	tmpDir := t.TempDir()

	// 创建一个类似仓库的目录
	repoDir := filepath.Join(tmpDir, "my-repo")
	os.MkdirAll(filepath.Join(repoDir, ".git"), 0755)

	// 创建一个非仓库目录
	nonRepoDir := filepath.Join(tmpDir, "not-a-repo")
	os.MkdirAll(nonRepoDir, 0755)

	repos, err := ScanRepositories(context.Background(), tmpDir, 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(repos) != 1 {
		t.Fatalf("expected 1 repo, got %d", len(repos))
	}

	if repos[0] != repoDir {
		t.Errorf("expected %s, got %s", repoDir, repos[0])
	}
}

func TestScanRepositories_MaxRepos(t *testing.T) {
	tmpDir := t.TempDir()

	for i := 0; i < 5; i++ {
		os.MkdirAll(filepath.Join(tmpDir, "repo-"+string(rune('a'+i)), ".git"), 0755)
	}

	repos, err := ScanRepositories(context.Background(), tmpDir, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(repos) != 2 {
		t.Fatalf("expected 2 repos (max), got %d", len(repos))
	}
}

func TestFindLogsWithoutContext_Slog(t *testing.T) {
	tmpDir := t.TempDir()

	// 包含缺少上下文的 slog 调用的文件
	content := `package main

import "log/slog"

func doWork() {
	slog.Info("starting work")
	slog.Error("something failed", "err", "test")
}

func doWorkWithContext(ctx context.Context) {
	slog.InfoContext(ctx, "this is fine")
	slog.Info("but this is not", "key", "val")
}
`
	writeGoFile(t, filepath.Join(tmpDir, "main.go"), content)

	logFuncs := []LogFunc{
		{Library: "slog", Functions: []string{"Info", "Debug", "Warn", "Error"}, CtxForm: "Context"},
	}

	results, err := FindLogsWithoutContext(context.Background(), tmpDir, logFuncs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 应该找到 3 个问题：slog.Info（第 6 行）、slog.Error（第 7 行）、slog.Info（第 12 行）
	if len(results) != 3 {
		t.Fatalf("expected 3 log issues, got %d", len(results))
		for i, r := range results {
			t.Logf("  [%d] %s:%d %s", i, r.File, r.Line, r.FuncName)
		}
	}

	// 验证合规的调用未被标记
	for _, r := range results {
		if r.FuncName == "slog.InfoContext" {
			t.Errorf("should not flag slog.InfoContext at line %d", r.Line)
		}
	}
}

func TestFindLogsWithoutContext_Fiber(t *testing.T) {
	tmpDir := t.TempDir()

	content := `package main

import "github.com/gofiber/fiber/v2/log"

func handler() {
	log.Info("request received")
	log.Error("something bad")
}
`
	writeGoFile(t, filepath.Join(tmpDir, "handler.go"), content)

	logFuncs := []LogFunc{
		{Library: "fiber", Functions: []string{"Info", "Debug", "Warn", "Error", "Fatal", "Panic"}, CtxForm: "WithContext"},
	}

	results, err := FindLogsWithoutContext(context.Background(), tmpDir, logFuncs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 fiber log issues, got %d", len(results))
	}
}

func TestFindLogsWithoutContext_Logrus(t *testing.T) {
	tmpDir := t.TempDir()

	content := `package main

import "github.com/sirupsen/logrus"

func process(entry *logrus.Entry) {
	entry.Info("processing started")
	entry.WithContext(ctx).Info("this is fine")
}
`
	writeGoFile(t, filepath.Join(tmpDir, "process.go"), content)

	logFuncs := []LogFunc{
		{Library: "logrus", Functions: []string{"Info", "Debug", "Warn", "Error"}, CtxForm: "WithContext"},
	}

	results, err := FindLogsWithoutContext(context.Background(), tmpDir, logFuncs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 应该找到 1 个问题：entry.Info（而非 entry.WithContext(ctx).Info）
	if len(results) != 1 {
		t.Fatalf("expected 1 logrus log issue, got %d", len(results))
	}
}

func TestFindLogsWithoutContext_SkipsTestFiles(t *testing.T) {
	tmpDir := t.TempDir()

	content := `package main

import "log/slog"

func TestSomething(t *testing.T) {
	slog.Info("this is in a test file")
}
`
	writeGoFile(t, filepath.Join(tmpDir, "main_test.go"), content)

	logFuncs := []LogFunc{
		{Library: "slog", Functions: []string{"Info"}, CtxForm: "Context"},
	}

	results, err := FindLogsWithoutContext(context.Background(), tmpDir, logFuncs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(results) != 0 {
		t.Fatalf("expected 0 issues (test files skipped), got %d", len(results))
	}
}

func TestFindLogsWithoutContext_SkipsVendor(t *testing.T) {
	tmpDir := t.TempDir()

	vendorDir := filepath.Join(tmpDir, "vendor", "pkg")
	os.MkdirAll(vendorDir, 0755)

	content := `package pkg

import "log/slog"

func Do() {
	slog.Info("in vendor")
}
`
	writeGoFile(t, filepath.Join(vendorDir, "lib.go"), content)

	logFuncs := []LogFunc{
		{Library: "slog", Functions: []string{"Info"}, CtxForm: "Context"},
	}

	results, err := FindLogsWithoutContext(context.Background(), tmpDir, logFuncs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(results) != 0 {
		t.Fatalf("expected 0 issues (vendor skipped), got %d", len(results))
	}
}

func writeGoFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write file %s: %v", path, err)
	}
}
