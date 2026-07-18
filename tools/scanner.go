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

// ScanRepositories 扫描仓库根目录，查找 Go 仓库。
// 返回仓库的绝对路径列表（包含 .git 的目录）。
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

// PullLatest 拉取目标分支的最新代码。
// 切换到目标分支并拉取最新变更。
func PullLatest(ctx context.Context, repoPath, branch string) error {
	repo, err := git.PlainOpen(repoPath)
	if err != nil {
		return fmt.Errorf("open repo %s: %w", repoPath, err)
	}

	w, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("get worktree: %w", err)
	}

	// 切换到目标分支
	branchRef := plumbing.NewBranchReferenceName(branch)
	err = w.Checkout(&git.CheckoutOptions{
		Branch: branchRef,
		Force:  true,
	})
	if err != nil {
		// 备选方案：尝试作为远程分支
		err = w.Checkout(&git.CheckoutOptions{
			Branch: plumbing.NewRemoteReferenceName("origin", branch),
			Create: false,
		})
		if err != nil {
			return fmt.Errorf("checkout branch %s: %w", branch, err)
		}
	}

	// 拉取最新代码
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

// FindLogsWithoutContext 扫描 Go 文件，查找缺少 WithContext 的日志调用。
// 根据 SKILL.md 规则检测 slog、fiber-log 和 logrus 模式。
func FindLogsWithoutContext(ctx context.Context, repoPath string, logFuncs []LogFunc) ([]types.FileLocation, error) {
	var results []types.FileLocation

	err := filepath.Walk(repoPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // 跳过无法访问的文件
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// 跳过 vendor、.git、测试文件
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
			return nil // 跳过解析出错的文件
		}
		results = append(results, locations...)
		return nil
	})

	if err != nil {
		return results, fmt.Errorf("walk repo %s: %w", repoPath, err)
	}
	return results, nil
}

// LogFunc 描述一个日志库及其需要检测的函数。
type LogFunc struct {
	Library   string
	Functions []string
	CtxForm   string
}

// scanFileForLogs 解析 Go 文件并查找缺少上下文的日志调用。
func scanFileForLogs(filePath string, logFuncs []LogFunc) ([]types.FileLocation, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filePath, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	// 为每个日志库构建查找映射
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

		// 检查 slog.Info(...)、slog.Error(...) 等调用
		if loc := checkSlogCall(call, slogFuncs, filePath, line, expr); loc != nil {
			results = append(results, *loc)
			return true
		}

		// 检查 fiber 的 log.Info(...)、log.Error(...) 调用
		if loc := checkFiberCall(call, fiberFuncs, filePath, line, expr); loc != nil {
			results = append(results, *loc)
			return true
		}

		// 检查 logrus 的 entry.Info(...)、entry.Error(...) 调用
		if loc := checkLogrusCall(call, logrusFuncs, filePath, line, expr); loc != nil {
			results = append(results, *loc)
			return true
		}

		return true
	})

	return results, nil
}

// checkSlogCall 检测缺少 Context 后缀的 slog.Info/Warn/Error/Debug 调用。
// 模式：slog.Info("msg") 或 slog.Info("msg", args...)
// 合规：slog.InfoContext(ctx, "msg") — 跳过这些。
func checkSlogCall(call *ast.CallExpr, slogFuncs map[string]string, file string, line int, expr string) *types.FileLocation {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}

	// 必须在 "slog" 包上调用
	ident, ok := sel.X.(*ast.Ident)
	if !ok || ident.Name != "slog" {
		return nil
	}

	funcName := sel.Sel.Name

	// 如果已经有 Context 后缀则跳过（如 InfoContext、ErrorContext）
	if strings.HasSuffix(funcName, "Context") {
		return nil
	}

	// 检查是否为已知的 slog 函数
	if _, found := slogFuncs[funcName]; !found {
		return nil
	}

	// 同样跳过：slog.With(ctx).Info(...) — With 方法已处理上下文
	// 通过检查调用是否在 slog.Logger 接收者上检测
	// 我们通过 With 模式单独处理

	return &types.FileLocation{
		File:     file,
		Line:     line,
		FuncName: "slog." + funcName,
		LogExpr:  expr,
	}
}

// checkFiberCall 检测缺少 WithContext 的 fiber log.Info/Warn/Error 等调用。
// 模式：log.Info("msg")
// 合规：log.WithContext(c).Info("msg") — 跳过这些。
func checkFiberCall(call *ast.CallExpr, fiberFuncs map[string]string, file string, line int, expr string) *types.FileLocation {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}

	ident, ok := sel.X.(*ast.Ident)
	if !ok {
		return nil
	}

	// 必须在 "log"（fiber 的日志包别名）上调用
	if ident.Name != "log" {
		return nil
	}

	funcName := sel.Sel.Name
	if _, found := fiberFuncs[funcName]; !found {
		return nil
	}

	// 如果在 WithContext() 结果上调用则跳过：log.WithContext(c).Info(...)
	// 此时 sel.X 应为 CallExpr 而非 Ident。
	// 由于我们匹配到 sel.X 为 *ast.Ident，这是直接调用 — 需要修复。

	return &types.FileLocation{
		File:     file,
		Line:     line,
		FuncName: "log." + funcName,
		LogExpr:  expr,
	}
}

// checkLogrusCall 检测缺少 WithContext 的 logrus entry.Info/Warn/Error 等调用。
// 模式：entry.Info("msg")
// 合规：entry.WithContext(ctx).Info("msg") — 跳过这些。
func checkLogrusCall(call *ast.CallExpr, logrusFuncs map[string]string, file string, line int, expr string) *types.FileLocation {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}

	// 对于 logrus，接收者通常是 *logrus.Entry 变量
	// sel.X 应为 Ident（变量名如 "entry"、"log"、"logger"）
	_, ok = sel.X.(*ast.Ident)
	if !ok {
		// 如果 sel.X 是 CallExpr，可能是 entry.WithContext(ctx).Info(...)
		// 已经合规 — 跳过
		if _, isCall := sel.X.(*ast.CallExpr); isCall {
			return nil
		}
		return nil
	}

	funcName := sel.Sel.Name
	if _, found := logrusFuncs[funcName]; !found {
		return nil
	}

	// 需要验证这是否确实是 logrus entry。
	// 启发式判断：如果函数名匹配 logrus 函数且
	// 导入包含 "logrus"，则很可能是 logrus 调用。
	// 目前，我们匹配所有符合 logrus 函数名的 *Ident.Func 模式。
	// 分析器阶段会做更深入的验证。

	return &types.FileLocation{
		File:     file,
		Line:     line,
		FuncName: funcName,
		LogExpr:  expr,
	}
}

// formatExpr 将 CallExpr 格式化为可读字符串。
func formatExpr(fset *token.FileSet, call *ast.CallExpr) string {
	start := fset.Position(call.Pos())
	end := fset.Position(call.End())
	if start.Filename != end.Filename {
		return "<expr>"
	}
	// 读取源代码片段
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

// isGoFile 检查文件路径是否以 .go 结尾且不是测试文件。
func isGoFile(path string) bool {
	return strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go")
}
