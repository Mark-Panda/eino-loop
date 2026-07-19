package tools

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"

	"github.com/Mark-Panda/eino-loop/types"
)

// ========== 增量扫描 ==========

// FindModifiedFiles 使用 git diff 获取自上次扫描以来修改的文件。
// 用于增量扫描，只检查有变更的文件。
func FindModifiedFiles(ctx context.Context, repoPath, sinceRef string) ([]string, error) {
	// 获取自 sinceRef 以来修改的 .go 文件
	cmd := exec.CommandContext(ctx, "git", "diff", "--name-only", "--diff-filter=ACMR",
		sinceRef, "--", "*.go")
	cmd.Dir = repoPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git diff 失败: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	var goFiles []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" && strings.HasSuffix(line, ".go") && !strings.HasSuffix(line, "_test.go") {
			goFiles = append(goFiles, filepath.Join(repoPath, line))
		}
	}
	return goFiles, nil
}

// FindLogsWithoutContextIncremental 增量扫描：只检查自上次扫描以来修改的文件。
func FindLogsWithoutContextIncremental(ctx context.Context, repoPath, sinceRef string, logFuncs []LogFunc) ([]types.FileLocation, error) {
	modifiedFiles, err := FindModifiedFiles(ctx, repoPath, sinceRef)
	if err != nil {
		// 回退到全量扫描
		return FindLogsWithoutContext(ctx, repoPath, logFuncs)
	}

	if len(modifiedFiles) == 0 {
		return nil, nil
	}

	var results []types.FileLocation
	for _, filePath := range modifiedFiles {
		if ctx.Err() != nil {
			return results, ctx.Err()
		}
		locations, scanErr := scanFileForIssues(filePath, logFuncs)
		if scanErr != nil {
			continue
		}
		results = append(results, locations...)
	}

	return results, nil
}

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
// 扩展支持：结构体嵌入日志字段、seelog、resty SetContext
func scanFileForIssues(filePath string, logFuncs []LogFunc) ([]types.FileLocation, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filePath, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	// 收集导入信息
	imports := collectImports(file)

	// 收集结构体中的 logger 字段名
	loggerFields := collectLoggerFields(file)

	var results []types.FileLocation

	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		pos := fset.Position(call.Pos())
		line := pos.Line
		expr := formatExpr(fset, call)

		// 1. 检测 go-logger 包级调用缺少 WithContext
		if hasGoLoggerImport(imports) {
			if loc := checkGoLoggerCall(call, imports, filePath, line, expr); loc != nil {
				results = append(results, *loc)
			}
		}

		// 2. 检测结构体嵌入的 logger 字段调用缺少 WithContext
		//    模式：s.log.Info("msg"), uc.log.Error("msg"), u.log.Errorf("msg")
		if len(loggerFields) > 0 {
			if loc := checkStructLoggerCall(call, loggerFields, filePath, line, expr); loc != nil {
				results = append(results, *loc)
			}
		}

		// 3. 检测 gorm 数据库操作缺少 WithContext
		if hasGormImport(imports) {
			if loc := checkGormCall(call, imports, filePath, line, expr); loc != nil {
				results = append(results, *loc)
			}
		}

		// 4. 检测 seelog 调用（不支持 WithContext，需要迁移到 go-logger）
		if hasSeelogImport(imports) {
			if loc := checkSeelogCall(call, imports, filePath, line, expr); loc != nil {
				results = append(results, *loc)
			}
		}

		// 5. 检测 resty HTTP client 缺少 SetContext
		if hasRestyImport(imports) {
			if loc := checkRestyCall(call, imports, filePath, line, expr); loc != nil {
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

// ========== 结构体嵌入日志字段检测 ==========

// loggerFieldPatterns 匹配 go-logger 类型的字段名模式
var loggerFieldPatterns = []string{
	"log", "logger", "lg", "l",
}

// collectLoggerFields 收集文件中结构体的 logger 类型字段名。
// 查找类型为 *logger.Logger、*zap.Logger、*logrus.Entry 等的字段。
func collectLoggerFields(file *ast.File) map[string]bool {
	fields := make(map[string]bool)

	ast.Inspect(file, func(n ast.Node) bool {
		typeSpec, ok := n.(*ast.TypeSpec)
		if !ok {
			return true
		}

		structType, ok := typeSpec.Type.(*ast.StructType)
		if !ok {
			return true
		}

		for _, field := range structType.Fields.List {
			if len(field.Names) == 0 {
				continue
			}
			fieldName := field.Names[0].Name

			// 检查字段类型是否是 logger 相关
			if isLoggerType(field.Type) {
				fields[fieldName] = true
				continue
			}

			// 也匹配常见的 logger 字段名模式
			lowerName := strings.ToLower(fieldName)
			for _, pattern := range loggerFieldPatterns {
				if lowerName == pattern {
					fields[fieldName] = true
					break
				}
			}
		}

		return true
	})

	return fields
}

// isLoggerType 检查类型是否是 logger 相关类型。
func isLoggerType(expr ast.Expr) bool {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return isLoggerType(t.X)
	case *ast.SelectorExpr:
		typeName := t.Sel.Name
		// 匹配 *logger.Logger、*zap.Logger、*logrus.Entry、*slog.Logger 等
		if typeName == "Logger" || typeName == "Entry" || typeName == "SugaredLogger" {
			return true
		}
		// 匹配 go-logger 包的类型
		if ident, ok := t.X.(*ast.Ident); ok {
			if ident.Name == "logger" || ident.Name == "zap" || ident.Name == "logrus" || ident.Name == "slog" {
				return true
			}
		}
	case *ast.Ident:
		// 匹配同包内的 Logger 类型定义
		if t.Name == "Logger" {
			return true
		}
	}
	return false
}

// checkStructLoggerCall 检测结构体嵌入的 logger 字段调用是否缺少 WithContext。
// 模式：s.log.Info("msg")、uc.log.Error("msg")、u.log.Errorf("msg")
func checkStructLoggerCall(call *ast.CallExpr, loggerFields map[string]bool, file string, line int, expr string) *types.FileLocation {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}

	funcName := sel.Sel.Name

	// 检查是否是日志函数
	if !goLoggerLogFuncs[funcName] {
		return nil
	}

	// 检查调用链中是否有 WithContext
	if _, isCall := sel.X.(*ast.CallExpr); isCall {
		if innerSel, ok := sel.X.(*ast.CallExpr).Fun.(*ast.SelectorExpr); ok {
			if innerSel.Sel.Name == "WithContext" {
				return nil
			}
		}
	}

	// 检查接收者是否是结构体字段调用：receiver.log.Info()
	// sel.X 应该是 SelectorExpr：X=Ident(receiver), Sel=Ident(log)
	if innerSel, ok := sel.X.(*ast.SelectorExpr); ok {
		fieldName := innerSel.Sel.Name
		if loggerFields[fieldName] {
			return &types.FileLocation{
				File:     file,
				Line:     line,
				FuncName: fmt.Sprintf(".%s.%s", fieldName, funcName),
				LogExpr:  expr,
			}
		}
	}

	return nil
}

// ========== seelog 检测 ==========

// seelogLogFuncs seelog 中的日志函数
var seelogLogFuncs = map[string]bool{
	"Trace": true, "Debug": true, "Info": true, "Warn": true, "Error": true, "Critical": true,
	"Tracef": true, "Debugf": true, "Infof": true, "Warnf": true, "Errorf": true, "Criticalf": true,
}

// hasSeelogImport 检查是否导入了 seelog 包
func hasSeelogImport(imports []importInfo) bool {
	for _, imp := range imports {
		if strings.Contains(imp.path, "seelog") || strings.Contains(imp.path, "cihub/seelog") {
			return true
		}
	}
	return false
}

// checkSeelogCall 检测 seelog 调用。
// seelog 不支持 WithContext，需要迁移到 go-logger。
func checkSeelogCall(call *ast.CallExpr, imports []importInfo, file string, line int, expr string) *types.FileLocation {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}

	funcName := sel.Sel.Name
	if !seelogLogFuncs[funcName] {
		return nil
	}

	// 检查是否是 seelog 包调用
	if ident, ok := sel.X.(*ast.Ident); ok {
		if ident.Name == "seelog" {
			return &types.FileLocation{
				File:     file,
				Line:     line,
				FuncName: fmt.Sprintf("seelog.%s", funcName),
				LogExpr:  expr,
			}
		}
	}

	return nil
}

// ========== resty HTTP client 检测 ==========

// hasRestyImport 检查是否导入了 resty 包
func hasRestyImport(imports []importInfo) bool {
	for _, imp := range imports {
		if strings.Contains(imp.path, "resty") || strings.Contains(imp.path, "go-resty") {
			return true
		}
	}
	return false
}

// checkRestyCall 检测 resty HTTP client 调用缺少 SetContext。
// 模式：resty.New().R().Get(url) → 需要 .SetContext(ctx)
//       client.R().Get(url) → 需要 .SetContext(ctx)
func checkRestyCall(call *ast.CallExpr, imports []importInfo, file string, line int, expr string) *types.FileLocation {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}

	// 检查是否是 HTTP 方法调用（Get/Post/Put/Delete/Patch/Head/Options）
	httpMethods := map[string]bool{
		"Get": true, "Post": true, "Put": true, "Delete": true,
		"Patch": true, "Head": true, "Options": true,
	}

	funcName := sel.Sel.Name
	if !httpMethods[funcName] {
		return nil
	}

	// 向上遍历调用链，检查是否有 SetContext
	if hasSetContextInChain(call) {
		return nil
	}

	// 检查是否是 resty 请求调用（R().Get() 或 client.R().Get()）
	if isRestyCallChain(sel) {
		return &types.FileLocation{
			File:     file,
			Line:     line,
			FuncName: fmt.Sprintf("resty.%s", funcName),
			LogExpr:  expr,
		}
	}

	return nil
}

// hasSetContextInChain 向上遍历调用链，检查是否已有 SetContext。
func hasSetContextInChain(call *ast.CallExpr) bool {
	current := call.Fun
	for {
		sel, ok := current.(*ast.SelectorExpr)
		if !ok {
			break
		}
		if sel.Sel.Name == "SetContext" {
			return true
		}
		if innerCall, ok := sel.X.(*ast.CallExpr); ok {
			current = innerCall.Fun
			continue
		}
		break
	}
	return false
}

// isRestyCallChain 判断调用链是否来自 resty 请求。
func isRestyCallChain(sel *ast.SelectorExpr) bool {
	// 模式1: resty.New().R().Get() → sel.X 是 CallExpr(R())
	if callExpr, ok := sel.X.(*ast.CallExpr); ok {
		if innerSel, ok := callExpr.Fun.(*ast.SelectorExpr); ok {
			// R() 调用
			if innerSel.Sel.Name == "R" {
				return true
			}
			// SetResult().Get() 等链式调用
			if isRestyChainMethod(innerSel.Sel.Name) {
				return isRestyCallChain(innerSel)
			}
		}
	}

	// 模式2: client.R().Get() → sel.X 是 CallExpr(R())
	// 已在模式1 中覆盖

	return false
}

// isRestyChainMethod 检查是否是 resty 链式方法
func isRestyChainMethod(name string) bool {
	chainMethods := map[string]bool{
		"R": true, "SetResult": true, "SetBody": true, "SetHeader": true,
		"SetHeaders": true, "SetQueryParam": true, "SetQueryParams": true,
		"SetFormData": true, "SetPathParam": true, "SetPathParams": true,
		"SetBasicAuth": true, "SetAuthToken": true, "SetCookie": true,
		"SetCookies": true, "SetContentLength": true, "SetHostURL": true,
	}
	return chainMethods[name]
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
