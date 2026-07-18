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

// VerifyCompile runs go build on the worktree and returns compilation errors.
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

// compileErrorPattern matches Go compiler errors like: ./file.go:12:3: error message
var compileErrorPattern = regexp.MustCompile(`^\.?/?([^:]+):(\d+)(?::(\d+))?:\s+(.+)$`)

// parseCompileErrors extracts structured errors from go build output.
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

// VerifyRescan re-scans the worktree for remaining log issues and compares with original.
func VerifyRescan(ctx context.Context, worktreePath string, original []types.FileLocation, logFuncs []LogFunc) (bool, []types.FileLocation, error) {
	// Re-scan the entire worktree
	current, err := FindLogsWithoutContext(ctx, worktreePath, logFuncs)
	if err != nil {
		return false, nil, fmt.Errorf("rescan: %w", err)
	}

	// Build a set of original issues for comparison
	type issueKey struct {
		File string
		Line int
	}
	originalSet := make(map[issueKey]bool)
	for _, loc := range original {
		originalSet[issueKey{File: loc.File, Line: loc.Line}] = true
	}

	// Check which original issues still remain
	var remaining []types.FileLocation
	for _, loc := range current {
		// Normalize file path (remove worktree prefix if present)
		normalizedFile := loc.File
		if strings.HasPrefix(normalizedFile, worktreePath) {
			normalizedFile = strings.TrimPrefix(normalizedFile, worktreePath)
			normalizedFile = strings.TrimPrefix(normalizedFile, "/")
		}

		// Check if this was an original issue (by matching file suffix and line)
		for origKey := range originalSet {
			if strings.HasSuffix(normalizedFile, origKey.File) || strings.HasSuffix(origKey.File, normalizedFile) {
				// Same file — if same line or nearby line, it's likely the same issue
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

// VerifyRegression runs go vet and optionally go test on the worktree.
func VerifyRegression(ctx context.Context, worktreePath string, runTests bool) (vetPass bool, testPass bool, errors []string, err error) {
	// Level 3a: go vet
	vetCmd := exec.CommandContext(ctx, "go", "vet", "./...")
	vetCmd.Dir = worktreePath
	vetOutput, vetErr := vetCmd.CombinedOutput()
	vetPass = vetErr == nil
	if vetErr != nil {
		errors = append(errors, fmt.Sprintf("go vet failed: %s", string(vetOutput)))
	}

	// Level 3b: go test (optional)
	testPass = true // Default to pass if not running tests
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

// RollbackFix reverts changes to a specific file using git checkout.
func RollbackFix(ctx context.Context, worktreePath, file string) error {
	cmd := exec.CommandContext(ctx, "git", "checkout", "--", file)
	cmd.Dir = worktreePath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git checkout %s: %w: %s", file, err, string(output))
	}
	return nil
}

// RollbackAll reverts all changes in the worktree.
func RollbackAll(ctx context.Context, worktreePath string) error {
	cmd := exec.CommandContext(ctx, "git", "checkout", "--", ".")
	cmd.Dir = worktreePath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git checkout all: %w: %s", err, string(output))
	}
	return nil
}

// VerifyAndRetry implements the full verify-retry loop for a single repository.
// Returns the final verification result after all retry attempts.
func VerifyAndRetry(ctx context.Context, cfg *config.Config, worktreePath string, fixResult types.FixResult, fixFn func(ctx context.Context) (int, error)) types.VerifyResult {
	maxRetries := cfg.MaxRetries

	// Convert config.LogFunc to tools.LogFunc
	logFuncs := make([]LogFunc, len(cfg.LogFunctions))
	for i, lf := range cfg.LogFunctions {
		logFuncs[i] = LogFunc{
			Library:   lf.Library,
			Functions: lf.Functions,
			CtxForm:   lf.CtxForm,
		}
	}

	for round := 0; round <= maxRetries; round++ {
		// Level 1: Compile verification
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
			// Rollback and retry fixing
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

		// Level 2: Rescan verification
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
				// Fix remaining issues
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

		// Level 3: Regression verification
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

		// All checks passed!
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
