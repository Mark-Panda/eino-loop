package tools

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/Mark-Panda/eino-loop/types"
	"github.com/Mark-Panda/eino-loop/config"
)

// CreateFixBranch 在 git 工作树中创建一个新分支用于应用修复。
func CreateFixBranch(ctx context.Context, repoPath string, cfg *config.Config) (branchName, worktreePath string, err error) {
	repo, err := git.PlainOpen(repoPath)
	if err != nil {
		return "", "", fmt.Errorf("open repo: %w", err)
	}

	branchName = cfg.FixBranchName()
	worktreePath = filepath.Join(os.TempDir(), "eino-loop-fix-"+filepath.Base(repoPath)+"-"+time.Now().Format("20060102150405"))

	// 获取 HEAD 引用
	headRef, err := repo.Head()
	if err != nil {
		return "", "", fmt.Errorf("get HEAD: %w", err)
	}

	// 获取工作树
	wt, err := repo.Worktree()
	if err != nil {
		return "", "", fmt.Errorf("get worktree: %w", err)
	}

	// 创建并切换到新分支
	branchRef := plumbing.NewBranchReferenceName(branchName)
	err = wt.Checkout(&git.CheckoutOptions{
		Hash:   headRef.Hash(),
		Branch: branchRef,
		Create: true,
	})
	if err != nil {
		return "", "", fmt.Errorf("create branch %s: %w", branchName, err)
	}

	// 使用仓库自身的工作树路径作为 worktreePath
	worktreePath = wt.Filesystem.Root()

	return branchName, worktreePath, nil
}

// ApplyLogFix 对单个日志调用位置应用 AST 重写修复。
// 如果修复成功应用则返回 true，以及 diff 内容。
func ApplyLogFix(ctx context.Context, worktreePath string, analysis types.AnalyzeResult) (bool, string, error) {
	if analysis.FixType == "skip" || analysis.NearestCtx == "" {
		return false, "", nil
	}

	filePath := analysis.Location.File
	line := analysis.Location.Line

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filePath, nil, parser.ParseComments)
	if err != nil {
		return false, "", fmt.Errorf("parse file: %w", err)
	}

	// 读取原始内容用于 diff
	originalContent, err := os.ReadFile(filePath)
	if err != nil {
		return false, "", fmt.Errorf("read original file: %w", err)
	}

	// 根据修复类型应用修复
	modified := false
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		callLine := fset.Position(call.Pos()).Line
		if callLine != line {
			return true
		}

		switch analysis.FixType {
		case "context_param":
			modified = fixSlogCall(call, analysis.NearestCtx)
		case "logger_receiver":
			modified = fixReceiverCall(call, analysis.LogLib, analysis.NearestCtx)
		}

		return false
	})

	if !modified {
		return false, "", nil
	}

	// 将修改后的 AST 写回文件
	outFile, err := os.Create(filePath)
	if err != nil {
		return false, "", fmt.Errorf("create output file: %w", err)
	}
	defer outFile.Close()

	if err := printer.Fprint(outFile, fset, file); err != nil {
		return false, "", fmt.Errorf("print AST: %w", err)
	}

	// 生成简单的 diff 表示
	newContent, _ := os.ReadFile(filePath)
	diff := generateDiff(filePath, string(originalContent), string(newContent))

	return true, diff, nil
}

// fixSlogCall 将 slog.Func("msg") 转换为 slog.FuncContext(ctx, "msg")。
func fixSlogCall(call *ast.CallExpr, ctxVar string) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}

	funcName := sel.Sel.Name
	if strings.HasSuffix(funcName, "Context") {
		return false // 已经有 Context
	}

	// 添加 Context 后缀：Info → InfoContext，Error → ErrorContext 等
	sel.Sel.Name = funcName + "Context"

	// 将 ctx 作为第一个参数前置
	ctxArg := &ast.Ident{Name: ctxVar}
	call.Args = append([]ast.Expr{ctxArg}, call.Args...)

	return true
}

// fixReceiverCall 将 log.Func("msg") 转换为 log.WithContext(ctx).Func("msg")。
// 用于 fiber-log 和 logrus。
func fixReceiverCall(call *ast.CallExpr, logLib, ctxVar string) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}

	ctxArg := &ast.Ident{Name: ctxVar}

	// 构建：receiver.WithContext(ctx)
	withContextCall := &ast.CallExpr{
		Fun: &ast.SelectorExpr{
			X:   sel.X,
			Sel: &ast.Ident{Name: "WithContext"},
		},
		Args: []ast.Expr{ctxArg},
	}

	// 替换：receiver.Func(args...) → receiver.WithContext(ctx).Func(args...)
	// sel.X 原本是接收者（如 "log" 或 "entry"）
	// 现在 sel.X 变为 WithContext 调用
	sel.X = withContextCall

	return true
}

// CommitAndPush 提交工作树中的所有更改并推送分支。
func CommitAndPush(ctx context.Context, worktreePath, message string) (string, error) {
	repo, err := git.PlainOpen(worktreePath)
	if err != nil {
		return "", fmt.Errorf("open repo at %s: %w", worktreePath, err)
	}

	wt, err := repo.Worktree()
	if err != nil {
		return "", fmt.Errorf("get worktree: %w", err)
	}

	// 暂存所有更改
	_, err = wt.Add(".")
	if err != nil {
		return "", fmt.Errorf("stage changes: %w", err)
	}

	// 提交
	hash, err := wt.Commit(message, &git.CommitOptions{
		Author: &object.Signature{
			Name:  "eino-loop",
			Email: "eino-loop@auto-fix",
			When:  time.Now(),
		},
	})
	if err != nil {
		return "", fmt.Errorf("commit: %w", err)
	}

	// 推送
	err = repo.PushContext(ctx, &git.PushOptions{
		RemoteName: "origin",
	})
	if err != nil && err != git.NoErrAlreadyUpToDate {
		// 推送失败不是致命错误 — 分支仍然在本地提交成功
		return hash.String(), fmt.Errorf("push (commit %s succeeded locally): %w", hash.String()[:7], err)
	}

	return hash.String(), nil
}

// generateDiff 在旧内容和新内容之间创建简单的统一 diff。
func generateDiff(filename, oldContent, newContent string) string {
	if oldContent == newContent {
		return ""
	}

	var diff strings.Builder
	oldLines := strings.Split(oldContent, "\n")
	newLines := strings.Split(newContent, "\n")

	diff.WriteString(fmt.Sprintf("--- a/%s\n", filename))
	diff.WriteString(fmt.Sprintf("+++ b/%s\n", filename))

	// 简单的逐行比较
	maxLines := len(oldLines)
	if len(newLines) > maxLines {
		maxLines = len(newLines)
	}

	for i := 0; i < maxLines; i++ {
		oldLine := ""
		newLine := ""
		if i < len(oldLines) {
			oldLine = oldLines[i]
		}
		if i < len(newLines) {
			newLine = newLines[i]
		}
		if oldLine != newLine {
			if i < len(oldLines) {
				diff.WriteString(fmt.Sprintf("-%s\n", oldLine))
			}
			if i < len(newLines) {
				diff.WriteString(fmt.Sprintf("+%s\n", newLine))
			}
		}
	}

	return diff.String()
}

// RunGoModTidy 运行 go mod tidy 以修复 AST 重写后的模块问题。
func RunGoModTidy(ctx context.Context, worktreePath string) error {
	cmd := exec.CommandContext(ctx, "go", "mod", "tidy")
	cmd.Dir = worktreePath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("go mod tidy: %w: %s", err, string(output))
	}
	return nil
}
