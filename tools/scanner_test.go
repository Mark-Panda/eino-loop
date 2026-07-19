package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestScanRepositories(t *testing.T) {
	tmpDir := t.TempDir()

	// 创建一个有 .git 的仓库目录
	repoDir := filepath.Join(tmpDir, "my-repo")
	os.MkdirAll(filepath.Join(repoDir, ".git"), 0755)

	// 创建一个没有 .git 的目录
	nonRepoDir := filepath.Join(tmpDir, "not-a-repo")
	os.MkdirAll(nonRepoDir, 0755)

	repos, err := ScanRepositories(context.Background(), tmpDir, 50)
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}

	if len(repos) != 1 {
		t.Fatalf("期望 1 个仓库，得到 %d", len(repos))
	}

	if repos[0] != repoDir {
		t.Errorf("期望 %s，得到 %s", repoDir, repos[0])
	}
}

func TestScanRepositories_MaxRepos(t *testing.T) {
	tmpDir := t.TempDir()

	for i := 0; i < 5; i++ {
		os.MkdirAll(filepath.Join(tmpDir, "repo-"+string(rune('a'+i)), ".git"), 0755)
	}

	repos, err := ScanRepositories(context.Background(), tmpDir, 2)
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}

	if len(repos) != 2 {
		t.Fatalf("期望 2 个仓库（最大值），得到 %d", len(repos))
	}
}

func TestFindLogsWithoutContext_GoLogger(t *testing.T) {
	tmpDir := t.TempDir()

	// 使用 go-logger 包的日志调用，缺少 WithContext
	content := `package main

import ycLogger "gitlab.yc345.tv/backend/go-logger/logger"

func doWork() {
	ycLogger.Info("starting work")
	ycLogger.Error("something failed", "err", "test")
}

func doWorkWithContext(ctx context.Context) {
	ycLogger.WithContext(ctx).Info("this is fine")
	ycLogger.Infof("but this is not: %s", "test")
}
`
	writeGoFile(t, filepath.Join(tmpDir, "main.go"), content)

	logFuncs := []LogFunc{
		{Library: "go-logger", Functions: []string{"Info", "Error", "Infof"}, CtxForm: "WithContext"},
	}

	results, err := FindLogsWithoutContext(context.Background(), tmpDir, logFuncs)
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}

	// 应该发现 3 个问题：ycLogger.Info (行6), ycLogger.Error (行7), ycLogger.Infof (行12)
	if len(results) != 3 {
		t.Fatalf("期望 3 个日志问题，得到 %d", len(results))
		for i, r := range results {
			t.Logf("  [%d] %s:%d %s", i, r.File, r.Line, r.FuncName)
		}
	}

	// 验证合规的调用不会被标记
	for _, r := range results {
		if r.FuncName == "ycLogger.WithContext" {
			t.Errorf("不应标记 ycLogger.WithContext 在行 %d", r.Line)
		}
	}
}

func TestFindLogsWithoutContext_Gorm(t *testing.T) {
	tmpDir := t.TempDir()

	content := `package data

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

func (d *Data) GetUserOK(ctx context.Context, id int64) error {
	var u User
	return d.db.WithContext(ctx).First(&u, id).Error
}
`
	writeGoFile(t, filepath.Join(tmpDir, "data.go"), content)

	logFuncs := []LogFunc{
		{Library: "gorm", Functions: []string{"First", "Find", "Create", "Update", "Delete"}, CtxForm: "WithContext"},
	}

	results, err := FindLogsWithoutContext(context.Background(), tmpDir, logFuncs)
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}

	// 应该发现 1 个问题：d.db.First() (行18)
	// d.db.WithContext(ctx).First() (行23) 是合规的
	if len(results) != 1 {
		t.Fatalf("期望 1 个 gorm 问题，得到 %d", len(results))
		for i, r := range results {
			t.Logf("  [%d] %s:%d %s", i, r.File, r.Line, r.FuncName)
		}
	}
}

func TestFindLogsWithoutContext_SkipsTestFiles(t *testing.T) {
	tmpDir := t.TempDir()

	content := `package main

import ycLogger "gitlab.yc345.tv/backend/go-logger/logger"

func TestSomething(t *testing.T) {
	ycLogger.Info("this is in a test file")
}
`
	writeGoFile(t, filepath.Join(tmpDir, "main_test.go"), content)

	logFuncs := []LogFunc{
		{Library: "go-logger", Functions: []string{"Info"}, CtxForm: "WithContext"},
	}

	results, err := FindLogsWithoutContext(context.Background(), tmpDir, logFuncs)
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}

	if len(results) != 0 {
		t.Fatalf("期望 0 个问题（跳过测试文件），得到 %d", len(results))
	}
}

func TestFindLogsWithoutContext_SkipsVendor(t *testing.T) {
	tmpDir := t.TempDir()

	vendorDir := filepath.Join(tmpDir, "vendor", "pkg")
	os.MkdirAll(vendorDir, 0755)

	content := `package pkg

import ycLogger "gitlab.yc345.tv/backend/go-logger/logger"

func Do() {
	ycLogger.Info("in vendor")
}
`
	writeGoFile(t, filepath.Join(vendorDir, "lib.go"), content)

	logFuncs := []LogFunc{
		{Library: "go-logger", Functions: []string{"Info"}, CtxForm: "WithContext"},
	}

	results, err := FindLogsWithoutContext(context.Background(), tmpDir, logFuncs)
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}

	if len(results) != 0 {
		t.Fatalf("期望 0 个问题（跳过 vendor），得到 %d", len(results))
	}
}

func TestPathValidator(t *testing.T) {
	tmpDir := t.TempDir()
	validator := NewPathValidator(tmpDir)

	// 允许的路径
	validPath := filepath.Join(tmpDir, "repo", "main.go")
	if err := validator.ValidatePath(validPath); err != nil {
		t.Errorf("期望路径 %s 有效，得到错误: %v", validPath, err)
	}

	// 禁止的路径 - .git
	gitPath := filepath.Join(tmpDir, "repo", ".git", "config")
	if err := validator.ValidatePath(gitPath); err == nil {
		t.Errorf("期望路径 %s 被禁止，但通过了校验", gitPath)
	}

	// 禁止的路径 - go.mod
	modPath := filepath.Join(tmpDir, "repo", "go.mod")
	if err := validator.ValidatePath(modPath); err == nil {
		t.Errorf("期望路径 %s 被禁止，但通过了校验", modPath)
	}

	// 禁止的路径 - 测试文件
	testPath := filepath.Join(tmpDir, "repo", "main_test.go")
	if err := validator.ValidatePath(testPath); err == nil {
		t.Errorf("期望路径 %s 被禁止，但通过了校验", testPath)
	}

	// 超出范围的路径
	outsidePath := "/tmp/outside/main.go"
	if err := validator.ValidatePath(outsidePath); err == nil {
		t.Errorf("期望路径 %s 被禁止，但通过了校验", outsidePath)
	}
}

func TestFindLogsWithoutContext_StructLogger(t *testing.T) {
	tmpDir := t.TempDir()

	// 结构体嵌入 logger 字段调用缺少 WithContext
	content := `package service

import ycLogger "gitlab.yc345.tv/backend/go-logger/logger"

type UserService struct {
	log *ycLogger.Logger
}

func (s *UserService) GetUser(id int64) {
	s.log.Info("getting user")
	s.log.Errorf("user not found: %d", id)
}

func (s *UserService) GetUserOK(ctx context.Context, id int64) {
	s.log.WithContext(ctx).Info("getting user")
}
`
	writeGoFile(t, filepath.Join(tmpDir, "service.go"), content)

	logFuncs := []LogFunc{
		{Library: "go-logger", Functions: []string{"Info", "Error", "Errorf"}, CtxForm: "WithContext"},
	}

	results, err := FindLogsWithoutContext(context.Background(), tmpDir, logFuncs)
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}

	// 应该发现 2 个问题：s.log.Info (行12), s.log.Errorf (行13)
	// s.log.WithContext(ctx).Info (行17) 是合规的
	if len(results) != 2 {
		t.Fatalf("期望 2 个结构体 logger 问题，得到 %d", len(results))
		for i, r := range results {
			t.Logf("  [%d] %s:%d %s", i, r.File, r.Line, r.FuncName)
		}
	}
}

func TestFindLogsWithoutContext_Seelog(t *testing.T) {
	tmpDir := t.TempDir()

	content := `package main

import "github.com/cihub/seelog"

func doWork() {
	seelog.Info("starting work")
	seelog.Errorf("failed: %v", err)
}
`
	writeGoFile(t, filepath.Join(tmpDir, "main.go"), content)

	logFuncs := []LogFunc{
		{Library: "seelog", Functions: []string{"Info", "Error", "Errorf"}, CtxForm: ""},
	}

	results, err := FindLogsWithoutContext(context.Background(), tmpDir, logFuncs)
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}

	// 应该发现 2 个 seelog 调用
	if len(results) != 2 {
		t.Fatalf("期望 2 个 seelog 问题，得到 %d", len(results))
	}
}

func TestFindLogsWithoutContext_Resty(t *testing.T) {
	tmpDir := t.TempDir()

	content := `package client

import "github.com/go-resty/resty/v2"

func fetchData(url string) error {
	_, err := resty.New().R().SetResult(&result).Get(url)
	return err
}
`
	writeGoFile(t, filepath.Join(tmpDir, "client.go"), content)

	logFuncs := []LogFunc{}

	results, err := FindLogsWithoutContext(context.Background(), tmpDir, logFuncs)
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}

	// 应该发现 1 个 resty 调用缺少 SetContext
	if len(results) != 1 {
		t.Fatalf("期望 1 个 resty 问题，得到 %d", len(results))
	}
}

func writeGoFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("写入文件 %s 失败: %v", path, err)
	}
}
