package tools

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"

	"github.com/Mark-Panda/eino-loop/types"
)

// ScanRepositories scans the repo root directory for Go repositories.
// Returns a list of absolute paths to repositories (directories containing .git).
func ScanRepositories(ctx context.Context, repoRoot string, maxRepos int) ([]string, error) {
	entries, err := os.ReadDir(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("read repo root %s: %w", repoRoot, err)
	}

	var repos []string
	for _, entry := range entries {
		if ctx.Err() != nil {
			return repos, ctx.Err()
		}
		if !entry.IsDir() {
			continue
		}
		gitDir := filepath.Join(repoRoot, entry.Name(), ".git")
		if _, err := os.Stat(gitDir); err == nil {
			repos = append(repos, filepath.Join(repoRoot, entry.Name()))
			if len(repos) >= maxRepos {
				break
			}
		}
	}
	return repos, nil
}

// PullLatest pulls the latest code for the target branch.
// Checks out the target branch and pulls latest changes.
func PullLatest(ctx context.Context, repoPath, branch string) error {
	repo, err := git.PlainOpen(repoPath)
	if err != nil {
		return fmt.Errorf("open repo %s: %w", repoPath, err)
	}

	w, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("get worktree: %w", err)
	}

	// Checkout target branch
	branchRef := plumbing.NewBranchReferenceName(branch)
	err = w.Checkout(&git.CheckoutOptions{
		Branch: branchRef,
		Force:  true,
	})
	if err != nil {
		// Fallback: try as remote branch
		err = w.Checkout(&git.CheckoutOptions{
			Branch: plumbing.NewRemoteReferenceName("origin", branch),
			Create: false,
		})
		if err != nil {
			return fmt.Errorf("checkout branch %s: %w", branch, err)
		}
	}

	// Pull latest
	err = w.PullContext(ctx, &git.PullOptions{
		RemoteName:    "origin",
		ReferenceName: branchRef,
		Force:         true,
	})
	if err != nil && err != git.NoErrAlreadyUpToDate {
		return fmt.Errorf("pull branch %s: %w", branch, err)
	}

	return nil
}

// FindLogsWithoutContext scans Go files for log calls missing WithContext.
// Detects slog, fiber-log, and logrus patterns per SKILL.md rules.
func FindLogsWithoutContext(ctx context.Context, repoPath string, logFuncs []LogFunc) ([]types.FileLocation, error) {
	var results []types.FileLocation

	err := filepath.Walk(repoPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip inaccessible files
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Skip vendor, .git, test files
		if info.IsDir() {
			base := info.Name()
			if base == "vendor" || base == ".git" || base == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !isGoFile(path) {
			return nil
		}

		locations, scanErr := scanFileForLogs(path, logFuncs)
		if scanErr != nil {
			return nil // Skip files with parse errors
		}
		results = append(results, locations...)
		return nil
	})

	if err != nil {
		return results, fmt.Errorf("walk repo %s: %w", repoPath, err)
	}
	return results, nil
}

// LogFunc describes a log library and its functions to detect.
type LogFunc struct {
	Library   string
	Functions []string
	CtxForm   string
}

// scanFileForLogs parses a Go file and finds log calls without context.
func scanFileForLogs(filePath string, logFuncs []LogFunc) ([]types.FileLocation, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filePath, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	// Build lookup maps for each log library
	slogFuncs := make(map[string]string)     // funcName -> library
	fiberFuncs := make(map[string]string)
	logrusFuncs := make(map[string]string)

	for _, lf := range logFuncs {
		switch lf.Library {
		case "slog":
			for _, f := range lf.Functions {
				slogFuncs[f] = lf.Library
			}
		case "fiber":
			for _, f := range lf.Functions {
				fiberFuncs[f] = lf.Library
			}
		case "logrus":
			for _, f := range lf.Functions {
				logrusFuncs[f] = lf.Library
			}
		}
	}

	var results []types.FileLocation

	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		pos := fset.Position(call.Pos())
		line := pos.Line
		expr := formatExpr(fset, call)

		// Check slog.Info(...), slog.Error(...), etc.
		if loc := checkSlogCall(call, slogFuncs, filePath, line, expr); loc != nil {
			results = append(results, *loc)
			return true
		}

		// Check log.Info(...), log.Error(...) for fiber
		if loc := checkFiberCall(call, fiberFuncs, filePath, line, expr); loc != nil {
			results = append(results, *loc)
			return true
		}

		// Check entry.Info(...), entry.Error(...) for logrus
		if loc := checkLogrusCall(call, logrusFuncs, filePath, line, expr); loc != nil {
			results = append(results, *loc)
			return true
		}

		return true
	})

	return results, nil
}

// checkSlogCall detects slog.Info/Warn/Error/Debug calls without Context.
// Pattern: slog.Info("msg") or slog.Info("msg", args...)
// Compliant: slog.InfoContext(ctx, "msg") — skip these.
func checkSlogCall(call *ast.CallExpr, slogFuncs map[string]string, file string, line int, expr string) *types.FileLocation {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}

	// Must be called on "slog" package
	ident, ok := sel.X.(*ast.Ident)
	if !ok || ident.Name != "slog" {
		return nil
	}

	funcName := sel.Sel.Name

	// Skip if already has Context suffix (e.g. InfoContext, ErrorContext)
	if strings.HasSuffix(funcName, "Context") {
		return nil
	}

	// Check if this is a known slog function
	if _, found := slogFuncs[funcName]; !found {
		return nil
	}

	// Also skip: slog.With(ctx).Info(...) — the With method handles context
	// This is detected by checking if the call is on a slog.Logger receiver
	// which we handle separately via the With pattern

	return &types.FileLocation{
		File:     file,
		Line:     line,
		FuncName: "slog." + funcName,
		LogExpr:  expr,
	}
}

// checkFiberCall detects fiber log.Info/Warn/Error/etc calls without WithContext.
// Pattern: log.Info("msg")
// Compliant: log.WithContext(c).Info("msg") — skip these.
func checkFiberCall(call *ast.CallExpr, fiberFuncs map[string]string, file string, line int, expr string) *types.FileLocation {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}

	ident, ok := sel.X.(*ast.Ident)
	if !ok {
		return nil
	}

	// Must be called on "log" (fiber's log package alias)
	if ident.Name != "log" {
		return nil
	}

	funcName := sel.Sel.Name
	if _, found := fiberFuncs[funcName]; !found {
		return nil
	}

	// Skip if called on a WithContext() result: log.WithContext(c).Info(...)
	// In this case sel.X would be a CallExpr, not an Ident.
	// Since we matched sel.X as *ast.Ident, this is a direct call — needs fixing.

	return &types.FileLocation{
		File:     file,
		Line:     line,
		FuncName: "log." + funcName,
		LogExpr:  expr,
	}
}

// checkLogrusCall detects logrus entry.Info/Warn/Error/etc calls without WithContext.
// Pattern: entry.Info("msg")
// Compliant: entry.WithContext(ctx).Info("msg") — skip these.
func checkLogrusCall(call *ast.CallExpr, logrusFuncs map[string]string, file string, line int, expr string) *types.FileLocation {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}

	// For logrus, the receiver is typically an *logrus.Entry variable
	// sel.X should be an Ident (the variable name like "entry", "log", "logger")
	_, ok = sel.X.(*ast.Ident)
	if !ok {
		// If sel.X is a CallExpr, it might be entry.WithContext(ctx).Info(...)
		// which is already compliant — skip
		if _, isCall := sel.X.(*ast.CallExpr); isCall {
			return nil
		}
		return nil
	}

	funcName := sel.Sel.Name
	if _, found := logrusFuncs[funcName]; !found {
		return nil
	}

	// We need to verify this is actually a logrus entry.
	// Heuristic: if the function name matches logrus functions and
	// the import contains "logrus", it's likely a logrus call.
	// For now, we match all *Ident.Func patterns that match logrus function names.
	// The analyzer phase will do deeper verification.

	return &types.FileLocation{
		File:     file,
		Line:     line,
		FuncName: funcName,
		LogExpr:  expr,
	}
}

// formatExpr formats a CallExpr into a readable string.
func formatExpr(fset *token.FileSet, call *ast.CallExpr) string {
	start := fset.Position(call.Pos())
	end := fset.Position(call.End())
	if start.Filename != end.Filename {
		return "<expr>"
	}
	// Read the source snippet
	data, err := os.ReadFile(start.Filename)
	if err != nil {
		return "<expr>"
	}
	lines := strings.Split(string(data), "\n")
	if start.Line == end.Line && start.Line <= len(lines) {
		line := lines[start.Line-1]
		startCol := start.Column - 1
		endCol := end.Column - 1
		if startCol < len(line) && endCol <= len(line) {
			return line[startCol:endCol]
		}
	}
	return "<expr>"
}

// isGoFile checks if a file path ends with .go and is not a test file.
func isGoFile(path string) bool {
	return strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go")
}
