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

// ========== 仓库扫描 ==========

// ScanRepositories 扫描仓库根目录下的所有 Go 代码仓库。
// 返回包含 .git 目录的仓库路径列表。
func ScanRepositories(ctx context.Context, repoRoot string, maxRepos int) ([]string, error) {
	entries, err := os.ReadDir(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("读取仓库根目录 %s 失败: %w", repoRoot, err)
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

// PullLatest 拉取指定仓库目标分支的最新代码。
func PullLatest(ctx context.Context, repoPath, branch string) error {
	repo, err := git.PlainOpen(repoPath)
	if err != nil {
		return fmt.Errorf("打开仓库 %s 失败: %w", repoPath, err)
	}

	w, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("获取工作树失败: %w", err)
	}

	// 切换到目标分支
	branchRef := plumbing.NewBranchReferenceName(branch)
	err = w.Checkout(&git.CheckoutOptions{
		Branch: branchRef,
		Force:  true,
	})
	if err != nil {
		// 回退：尝试远程分支
		err = w.Checkout(&git.CheckoutOptions{
			Branch: plumbing.NewRemoteReferenceName("origin", branch),
			Create: false,
		})
		if err != nil {
			return fmt.Errorf("切换分支 %s 失败: %w", branch, err)
		}
	}

	// 拉取最新
	err = w.PullContext(ctx, &git.PullOptions{
		RemoteName:    "origin",
		ReferenceName: branchRef,
		Force:         true,
	})
	if err != nil && err != git.NoErrAlreadyUpToDate {
		return fmt.Errorf("拉取分支 %s 失败: %w", branch, err)
	}

	return nil
}

// ========== 日志问题检测 ==========

// FindLogsWithoutContext 根据 SKILL 规则检测日志中缺少 WithContext 的调用。
// 重点检测：
// 1. gitlab.yc345.tv/backend/go-logger 包的日志调用
// 2. gorm.io/gorm 数据库 CRUD 操作缺少 WithContext(ctx)
func FindLogsWithoutContext(ctx context.Context, repoPath string, logFuncs []LogFunc) ([]types.FileLocation, error) {
	var results []types.FileLocation

	err := filepath.Walk(repoPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// 跳过 vendor、.git、testdata、_test.go
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

		locations, scanErr := scanFileForIssues(path, logFuncs)
		if scanErr != nil {
			return nil
		}
		results = append(results, locations...)
		return nil
	})

	if err != nil {
		return results, fmt.Errorf("遍历仓库 %s 失败: %w", repoPath, err)
	}
	return results, nil
}

// LogFunc 描述一个日志库及其需要检测的函数。
type LogFunc struct {
	Library   string   // "go-logger" / "gorm"
	Functions []string // 需要检测的函数名
	CtxForm   string   // WithContext 调用模式
}

// scanFileForIssues 扫描单个 Go 文件中的日志问题。
func scanFileForIssues(filePath string, logFuncs []LogFunc) ([]types.FileLocation, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filePath, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	// 收集导入信息，判断是否使用了 go-logger 或 gorm
	imports := collectImports(file)

	var results []types.FileLocation

	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		pos := fset.Position(call.Pos())
		line := pos.Line
		expr := formatExpr(fset, call)

		// 检测 go-logger 日志调用缺少 WithContext
		if hasGoLoggerImport(imports) {
			if loc := checkGoLoggerCall(call, imports, filePath, line, expr); loc != nil {
				results = append(results, *loc)
			}
		}

		// 检测 gorm 数据库操作缺少 WithContext
		if hasGormImport(imports) {
			if loc := checkGormCall(call, imports, filePath, line, expr); loc != nil {
				results = append(results, *loc)
			}
		}

		return true
	})

	return results, nil
}

// ========== 导入分析 ==========

// importInfo 存储导入的包信息
type importInfo struct {
	name    string // 导入别名（如无别名则为包的最后一段）
	path    string // 完整导入路径
	isAlias bool   // 是否有显式别名
}

// collectImports 收集文件中所有导入的包信息
func collectImports(file *ast.File) []importInfo {
	var imports []importInfo
	for _, imp := range file.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		info := importInfo{path: path}

		// 获取包名（路径最后一段）
		parts := strings.Split(path, "/")
		info.name = parts[len(parts)-1]

		if imp.Name != nil {
			info.name = imp.Name.Name
			info.isAlias = true
		}

		imports = append(imports, info)
	}
	return imports
}

// hasGoLoggerImport 检查是否导入了 go-logger 包
func hasGoLoggerImport(imports []importInfo) bool {
	for _, imp := range imports {
		if strings.Contains(imp.path, "gitlab.yc345.tv/backend/go-logger") {
			return true
		}
	}
	return false
}

// hasGormImport 检查是否导入了 gorm 包
func hasGormImport(imports []importInfo) bool {
	for _, imp := range imports {
		if imp.path == "gorm.io/gorm" || strings.HasPrefix(imp.path, "gorm.io/gorm/") {
			return true
		}
		// 也检查 gen 生成的代码
		if strings.Contains(imp.path, "gorm.io/gen") {
			return true
		}
	}
	return false
}

// getGoLoggerAlias 获取 go-logger 包的别名
func getGoLoggerAlias(imports []importInfo) string {
	for _, imp := range imports {
		if strings.Contains(imp.path, "gitlab.yc345.tv/backend/go-logger") {
			return imp.name
		}
	}
	return ""
}

// getGormAlias 获取 gorm 包的别名
func getGormAlias(imports []importInfo) string {
	for _, imp := range imports {
		if imp.path == "gorm.io/gorm" {
			return imp.name
		}
	}
	return "db" // 常见默认别名
}

// ========== go-logger 检测 ==========

// goLoggerLogFuncs go-logger 中需要 WithContext 的日志函数
var goLoggerLogFuncs = map[string]bool{
	"Info":  true,
	"Warn":  true,
	"Error": true,
	"Debug": true,
	"Fatal": true,
	"Panic": true,
	// 带格式化后缀的版本
	"Infof":  true,
	"Warnf":  true,
	"Errorf":  true,
	"Debugf":  true,
	"Fatalf":  true,
	"Panicf":  true,
	// 带结构化字段的版本
	"Infow":  true,
	"Warnw":  true,
	"Errorw":  true,
	"Debugw":  true,
}

// checkGoLoggerCall 检测 go-logger 日志调用是否缺少 WithContext。
//
// 根据 SKILL 规则：
// - ycLogger.Info("msg") → ycLogger.WithContext(ctx).Info("msg")
// - ycLogger.GetLogger().Info("msg") → ycLogger.WithContext(ctx).Info("msg")
//
// 已合规的调用会跳过：
// - ycLogger.WithContext(ctx).Info("msg") ✅
func checkGoLoggerCall(call *ast.CallExpr, imports []importInfo, file string, line int, expr string) *types.FileLocation {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}

	funcName := sel.Sel.Name

	// 检查是否是需要检测的日志函数
	if !goLoggerLogFuncs[funcName] {
		return nil
	}

	// 检查调用链：如果有 WithContext 在前面，说明已经合规
	// 形式1: ycLogger.WithContext(ctx).Info("msg")  → sel.X 是 CallExpr
	if _, isCall := sel.X.(*ast.CallExpr); isCall {
		// 检查是否是 WithContext 调用
		if innerSel, ok := sel.X.(*ast.CallExpr).Fun.(*ast.SelectorExpr); ok {
			if innerSel.Sel.Name == "WithContext" {
				return nil // 已有 WithContext，跳过
			}
		}
	}

	// 形式2: logger.WithContext(ctx).Info("msg")  → sel.X 是 CallExpr(WithContext)
	// 已在上面处理

	// 检查是否是 go-logger 包的调用
	alias := getGoLoggerAlias(imports)

	// 形式3: ycLogger.Info("msg") → sel.X 是 Ident，且名称匹配导入别名
	if ident, ok := sel.X.(*ast.Ident); ok {
		if ident.Name == alias || ident.Name == "logger" || ident.Name == "log" {
			return &types.FileLocation{
				File:     file,
				Line:     line,
				FuncName: fmt.Sprintf("%s.%s", ident.Name, funcName),
				LogExpr:  expr,
			}
		}
	}

	// 形式4: ycLogger.GetLogger().Info("msg") → sel.X 是 CallExpr
	if callExpr, ok := sel.X.(*ast.CallExpr); ok {
		if innerSel, ok := callExpr.Fun.(*ast.SelectorExpr); ok {
			if ident, ok := innerSel.X.(*ast.Ident); ok {
				if (ident.Name == alias || ident.Name == "logger") && innerSel.Sel.Name == "GetLogger" {
					return &types.FileLocation{
						File:     file,
						Line:     line,
						FuncName: fmt.Sprintf("%s.GetLogger().%s", ident.Name, funcName),
						LogExpr:  expr,
					}
				}
			}
		}
	}

	return nil
}

// ========== gorm 检测 ==========

// gormCrudFuncs gorm 中需要 WithContext 的终端执行函数（不包括链式构建器如 Model/Where/Select）
var gormCrudFuncs = map[string]bool{
	// 查询执行
	"First":       true,
	"Find":        true,
	"Last":        true,
	"Take":        true,
	"Count":       true,
	"Pluck":       true,
	"Scan":        true,
	"Rows":        true,
	"Row":         true,
	"ScanRows":    true,
	// 创建执行
	"Create":          true,
	"CreateInBatches": true,
	"Save":            true,
	// 更新执行
	"Update":        true,
	"Updates":       true,
	"UpdateColumn":  true,
	"UpdateColumns": true,
	// 删除执行
	"Delete": true,
	// 原始 SQL 执行
	"Exec": true,
	"Raw":  true,
}

// gormChainBuilders gorm 链式构建器（不需要单独检查 WithContext，由终端函数统一检查）
var gormChainBuilders = map[string]bool{
	"Where": true, "Or": true, "Not": true, "Joins": true,
	"Preload": true, "Select": true, "Group": true, "Having": true,
	"Order": true, "Limit": true, "Offset": true, "Distinct": true,
	"Table": true, "Model": true, "Unscoped": true,
	"Association": true, "Related": true,
}

// checkGormCall 检测 gorm 数据库操作是否缺少 WithContext。
//
// 根据 SKILL 规则：
// - db.Model(&User{}).Where("id = ?", id).First(&user) → db.WithContext(ctx).Model(&User{}).Where("id = ?", id).First(&user)
// - query.User.Where(q.ID.Eq(id)).First() → query.User.WithContext(ctx).Where(q.ID.Eq(id)).First()
//
// 已合规的调用会跳过：
// - db.WithContext(ctx).Model(&User{}).First(&user) ✅
func checkGormCall(call *ast.CallExpr, imports []importInfo, file string, line int, expr string) *types.FileLocation {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}

	funcName := sel.Sel.Name

	// 检查是否是需要检测的 CRUD 函数
	if !gormCrudFuncs[funcName] {
		return nil
	}

	// 向上遍历调用链，检查是否有 WithContext
	if hasWithContextInChain(call) {
		return nil // 已有 WithContext，跳过
	}

	// 检查是否是 gorm 相关的调用（通过变量名或调用链判断）
	if isGormCallChain(sel, imports) {
		return &types.FileLocation{
			File:     file,
			Line:     line,
			FuncName: fmt.Sprintf("gorm.%s", funcName),
			LogExpr:  expr,
		}
	}

	return nil
}

// hasWithContextInChain 向上遍历调用链，检查是否已有 WithContext。
func hasWithContextInChain(call *ast.CallExpr) bool {
	// 检查当前调用的接收者链
	current := call.Fun
	for {
		sel, ok := current.(*ast.SelectorExpr)
		if !ok {
			break
		}

		// 检查是否是 WithContext 调用
		if sel.Sel.Name == "WithContext" {
			return true
		}

		// 继续向上：如果接收者是另一个方法调用
		if innerCall, ok := sel.X.(*ast.CallExpr); ok {
			current = innerCall.Fun
			continue
		}

		break
	}
	return false
}

// isGormCallChain 判断调用链是否来自 gorm 相关变量。
// 检测模式：
// - db.Xxx() 或 dbVar.Xxx()
// - r.data.db.Xxx()
// - query.Xxx()
// - d.db.Xxx() (结构体字段)
func isGormCallChain(sel *ast.SelectorExpr, imports []importInfo) bool {
	// 向上遍历整个调用链，查找根变量名
	current := sel.X
	for {
		// 如果是标识符（变量名），检查是否匹配 gorm 模式
		if ident, ok := current.(*ast.Ident); ok {
			name := strings.ToLower(ident.Name)
			if name == "db" || strings.HasSuffix(name, "db") || strings.HasSuffix(name, "dao") || strings.HasSuffix(name, "repo") || name == "query" || name == "q" {
				return true
			}
			break
		}

		// 如果是选择器（如 r.data.db），继续向上
		if innerSel, ok := current.(*ast.SelectorExpr); ok {
			// 检查当前字段名是否是 gorm 相关
			fieldName := strings.ToLower(innerSel.Sel.Name)
			if fieldName == "db" || strings.HasSuffix(fieldName, "db") || strings.HasSuffix(fieldName, "dao") || strings.HasSuffix(fieldName, "repo") {
				return true
			}
			current = innerSel.X
			continue
		}

		// 如果是函数调用（如 GetDB()），检查函数名
		if callExpr, ok := current.(*ast.CallExpr); ok {
			if innerSel, ok := callExpr.Fun.(*ast.SelectorExpr); ok {
				fieldName := strings.ToLower(innerSel.Sel.Name)
				if strings.Contains(fieldName, "db") || strings.Contains(fieldName, "gorm") || fieldName == "getdb" {
					return true
				}
				current = innerSel.X
				continue
			}
			break
		}

		break
	}
	return false
}

// ========== 辅助函数 ==========

// isGoFile 检查文件是否是 Go 源文件（排除测试文件）
func isGoFile(path string) bool {
	return strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go")
}

// formatExpr 将 CallExpr 格式化为可读字符串
func formatExpr(fset *token.FileSet, call *ast.CallExpr) string {
	start := fset.Position(call.Pos())
	end := fset.Position(call.End())
	if start.Filename != end.Filename {
		return "<expr>"
	}
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
