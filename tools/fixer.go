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

// CreateFixBranch creates a new branch in a git worktree for applying fixes.
func CreateFixBranch(ctx context.Context, repoPath string, cfg *config.Config) (branchName, worktreePath string, err error) {
	repo, err := git.PlainOpen(repoPath)
	if err != nil {
		return "", "", fmt.Errorf("open repo: %w", err)
	}

	branchName = cfg.FixBranchName()
	worktreePath = filepath.Join(os.TempDir(), "eino-loop-fix-"+filepath.Base(repoPath)+"-"+time.Now().Format("20060102150405"))

	// Get HEAD reference
	headRef, err := repo.Head()
	if err != nil {
		return "", "", fmt.Errorf("get HEAD: %w", err)
	}

	// Create worktree
	wt, err := repo.Worktree()
	if err != nil {
		return "", "", fmt.Errorf("get worktree: %w", err)
	}

	// Create and checkout new branch
	branchRef := plumbing.NewBranchReferenceName(branchName)
	err = wt.Checkout(&git.CheckoutOptions{
		Hash:   headRef.Hash(),
		Branch: branchRef,
		Create: true,
	})
	if err != nil {
		return "", "", fmt.Errorf("create branch %s: %w", branchName, err)
	}

	// Use the repo's own worktree path as the worktreePath
	worktreePath = wt.Filesystem.Root()

	return branchName, worktreePath, nil
}

// ApplyLogFix applies the AST rewrite fix for a single log call site.
// Returns true if the fix was applied successfully, and the diff content.
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

	// Read original content for diff
	originalContent, err := os.ReadFile(filePath)
	if err != nil {
		return false, "", fmt.Errorf("read original file: %w", err)
	}

	// Apply the fix based on fix type
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

	// Write modified AST back to file
	outFile, err := os.Create(filePath)
	if err != nil {
		return false, "", fmt.Errorf("create output file: %w", err)
	}
	defer outFile.Close()

	if err := printer.Fprint(outFile, fset, file); err != nil {
		return false, "", fmt.Errorf("print AST: %w", err)
	}

	// Generate a simple diff representation
	newContent, _ := os.ReadFile(filePath)
	diff := generateDiff(filePath, string(originalContent), string(newContent))

	return true, diff, nil
}

// fixSlogCall transforms slog.Func("msg") → slog.FuncContext(ctx, "msg").
func fixSlogCall(call *ast.CallExpr, ctxVar string) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}

	funcName := sel.Sel.Name
	if strings.HasSuffix(funcName, "Context") {
		return false // Already has Context
	}

	// Add Context suffix: Info → InfoContext, Error → ErrorContext, etc.
	sel.Sel.Name = funcName + "Context"

	// Prepend ctx as first argument
	ctxArg := &ast.Ident{Name: ctxVar}
	call.Args = append([]ast.Expr{ctxArg}, call.Args...)

	return true
}

// fixReceiverCall transforms log.Func("msg") → log.WithContext(ctx).Func("msg").
// Used for fiber-log and logrus.
func fixReceiverCall(call *ast.CallExpr, logLib, ctxVar string) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}

	ctxArg := &ast.Ident{Name: ctxVar}

	// Build: receiver.WithContext(ctx)
	withContextCall := &ast.CallExpr{
		Fun: &ast.SelectorExpr{
			X:   sel.X,
			Sel: &ast.Ident{Name: "WithContext"},
		},
		Args: []ast.Expr{ctxArg},
	}

	// Replace: receiver.Func(args...) → receiver.WithContext(ctx).Func(args...)
	// sel.X was the receiver (e.g., "log" or "entry")
	// Now sel.X becomes the WithContext call
	sel.X = withContextCall

	return true
}

// CommitAndPush commits all changes in the worktree and pushes the branch.
func CommitAndPush(ctx context.Context, worktreePath, message string) (string, error) {
	repo, err := git.PlainOpen(worktreePath)
	if err != nil {
		return "", fmt.Errorf("open repo at %s: %w", worktreePath, err)
	}

	wt, err := repo.Worktree()
	if err != nil {
		return "", fmt.Errorf("get worktree: %w", err)
	}

	// Stage all changes
	_, err = wt.Add(".")
	if err != nil {
		return "", fmt.Errorf("stage changes: %w", err)
	}

	// Commit
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

	// Push
	err = repo.PushContext(ctx, &git.PushOptions{
		RemoteName: "origin",
	})
	if err != nil && err != git.NoErrAlreadyUpToDate {
		// Push failure is not fatal — branch is still committed locally
		return hash.String(), fmt.Errorf("push (commit %s succeeded locally): %w", hash.String()[:7], err)
	}

	return hash.String(), nil
}

// generateDiff creates a simple unified diff between old and new content.
func generateDiff(filename, oldContent, newContent string) string {
	if oldContent == newContent {
		return ""
	}

	var diff strings.Builder
	oldLines := strings.Split(oldContent, "\n")
	newLines := strings.Split(newContent, "\n")

	diff.WriteString(fmt.Sprintf("--- a/%s\n", filename))
	diff.WriteString(fmt.Sprintf("+++ b/%s\n", filename))

	// Simple line-by-line comparison
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

// RollbackGoMod runs go mod tidy to fix any module issues after AST rewrite.
func RunGoModTidy(ctx context.Context, worktreePath string) error {
	cmd := exec.CommandContext(ctx, "go", "mod", "tidy")
	cmd.Dir = worktreePath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("go mod tidy: %w: %s", err, string(output))
	}
	return nil
}
