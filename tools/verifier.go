package tools

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/Mark-Panda/eino-loop/types"
	"github.com/Mark-Panda/eino-loop/config"
)

// VerifyCompile 在工作树上运行 go build 并返回编译错误。
func VerifyCompile(ctx context.Context, worktreePath string) (bool, []types.CompileError, error) {
	cmd := exec.CommandContext(ctx, "go", "build", "./...")
	cmd.Dir = worktreePath

	output, err := cmd.CombinedOutput()
	if err == nil {
		return true, nil, nil
	}

	errors := parseCompileErrors(string(output))
	return false, errors, nil
}

// compileErrorPattern 匹配 Go 编译器错误，如：./file.go:12:3: error message
var compileErrorPattern = regexp.MustCompile(`^\.?/?([^:]+):(\d+)(?::(\d+))?:\s+(.+)$`)

// parseCompileErrors 从 go build 输出中提取结构化错误。
func parseCompileErrors(output string) []types.CompileError {
	var errors []types.CompileError
	scanner := bufio.NewScanner(strings.NewReader(output))

	for scanner.Scan() {
		line := scanner.Text()
		matches := compileErrorPattern.FindStringSubmatch(line)
		if matches == nil {
			continue
		}

		lineNum, _ := strconv.Atoi(matches[2])
		errors = append(errors, types.CompileError{
			File:    matches[1],
			Line:    lineNum,
			Message: matches[4],
		})
	}

	return errors
}

// VerifyRescan 重新扫描工作树中剩余的日志问题，并与原始问题进行比较。
func VerifyRescan(ctx context.Context, worktreePath string, original []types.FileLocation, logFuncs []LogFunc) (bool, []types.FileLocation, error) {
	// 重新扫描整个工作树
	current, err := FindLogsWithoutContext(ctx, worktreePath, logFuncs)
	if err != nil {
		return false, nil, fmt.Errorf("rescan: %w", err)
	}

	// 构建原始问题的集合用于比较
	type issueKey struct {
		File string
		Line int
	}
	originalSet := make(map[issueKey]bool)
	for _, loc := range original {
		originalSet[issueKey{File: loc.File, Line: loc.Line}] = true
	}

	// 检查哪些原始问题仍然存在
	var remaining []types.FileLocation
	for _, loc := range current {
		// 规范化文件路径（移除工作树前缀）
		normalizedFile := loc.File
		if strings.HasPrefix(normalizedFile, worktreePath) {
			normalizedFile = strings.TrimPrefix(normalizedFile, worktreePath)
			normalizedFile = strings.TrimPrefix(normalizedFile, "/")
		}

		// 检查这是否是原始问题（通过匹配文件后缀和行号）
		for origKey := range originalSet {
			if strings.HasSuffix(normalizedFile, origKey.File) || strings.HasSuffix(origKey.File, normalizedFile) {
				// 同一文件 — 如果行号相同或接近，则很可能是同一个问题
				if loc.Line == origKey.Line || (loc.Line >= origKey.Line-2 && loc.Line <= origKey.Line+2) {
					remaining = append(remaining, loc)
					break
				}
			}
		}
	}

	allFixed := len(remaining) == 0
	return allFixed, remaining, nil
}

// VerifyRegression 在工作树上运行 go vet 以及可选的 go test。
func VerifyRegression(ctx context.Context, worktreePath string, runTests bool) (vetPass bool, testPass bool, errors []string, err error) {
	// 第 3 级 a：go vet
	vetCmd := exec.CommandContext(ctx, "go", "vet", "./...")
	vetCmd.Dir = worktreePath
	vetOutput, vetErr := vetCmd.CombinedOutput()
	vetPass = vetErr == nil
	if vetErr != nil {
		errors = append(errors, fmt.Sprintf("go vet failed: %s", string(vetOutput)))
	}

	// 第 3 级 b：go test（可选）
	testPass = true // 如果不运行测试则默认通过
	if runTests {
		testCmd := exec.CommandContext(ctx, "go", "test", "-count=1", "-timeout", "5m", "./...")
		testCmd.Dir = worktreePath
		testOutput, testErr := testCmd.CombinedOutput()
		testPass = testErr == nil
		if testErr != nil {
			errors = append(errors, fmt.Sprintf("go test failed: %s", string(testOutput)))
		}
	}

	return vetPass, testPass, errors, nil
}

// RollbackFix 使用 git checkout 回滚对特定文件的更改。
func RollbackFix(ctx context.Context, worktreePath, file string) error {
	cmd := exec.CommandContext(ctx, "git", "checkout", "--", file)
	cmd.Dir = worktreePath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git checkout %s: %w: %s", file, err, string(output))
	}
	return nil
}

// RollbackAll 回滚工作树中的所有更改。
func RollbackAll(ctx context.Context, worktreePath string) error {
	cmd := exec.CommandContext(ctx, "git", "checkout", "--", ".")
	cmd.Dir = worktreePath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git checkout all: %w: %s", err, string(output))
	}
	return nil
}

// VerifyAndRetry 实现单个仓库的完整验证-重试循环。
// 返回所有重试尝试后的最终验证结果。
func VerifyAndRetry(ctx context.Context, cfg *config.Config, worktreePath string, fixResult types.FixResult, fixFn func(ctx context.Context) (int, error)) types.VerifyResult {
	maxRetries := cfg.MaxRetries

	// 将 config.LogFunc 转换为 tools.LogFunc
	logFuncs := make([]LogFunc, len(cfg.LogFunctions))
	for i, lf := range cfg.LogFunctions {
		logFuncs[i] = LogFunc{
			Library:   lf.Library,
			Functions: lf.Functions,
			CtxForm:   lf.CtxForm,
		}
	}

	for round := 0; round <= maxRetries; round++ {
		// 第 1 级：编译验证
		compileOK, compileErrors, err := VerifyCompile(ctx, worktreePath)
		if err != nil {
			return types.VerifyResult{
				CompileOK:  false,
				RetryCount: round,
				MaxRetries: maxRetries,
				NeedsHuman: true,
			}
		}

		if !compileOK {
			// 回滚并重试修复
			RollbackAll(ctx, worktreePath)
			if round < maxRetries {
				fixFn(ctx)
				continue
			}
			return types.VerifyResult{
				CompileOK:     false,
				CompileErrors: compileErrors,
				RetryCount:    round,
				MaxRetries:    maxRetries,
				NeedsHuman:    true,
			}
		}

		// 第 2 级：重新扫描验证
		allFixed, remaining, err := VerifyRescan(ctx, worktreePath, fixResult.OriginalIssues, logFuncs)
		if err != nil {
			return types.VerifyResult{
				CompileOK:      true,
				AllIssuesFixed: false,
				RetryCount:     round,
				MaxRetries:     maxRetries,
				NeedsHuman:     true,
			}
		}

		if !allFixed {
			if round < maxRetries {
				// 修复剩余问题
				fixFn(ctx)
				continue
			}
			return types.VerifyResult{
				CompileOK:      true,
				AllIssuesFixed: false,
				Remaining:      remaining,
				RetryCount:     round,
				MaxRetries:     maxRetries,
				NeedsHuman:     true,
			}
		}

		// 第 3 级：回归验证
		vetPass, _, regressErrors, _ := VerifyRegression(ctx, worktreePath, false)
		if !vetPass {
			return types.VerifyResult{
				CompileOK:      true,
				AllIssuesFixed: true,
				RegressionFree: false,
				RetryCount:     round,
				MaxRetries:     maxRetries,
				NeedsHuman:     true,
			}
		}

		// 所有检查通过！
		_ = regressErrors
		return types.VerifyResult{
			CompileOK:      true,
			AllIssuesFixed: true,
			RegressionFree: true,
			RetryCount:     round,
			MaxRetries:     maxRetries,
		}
	}

	return types.VerifyResult{
		RetryCount: maxRetries,
		MaxRetries: maxRetries,
		NeedsHuman: true,
	}
}
