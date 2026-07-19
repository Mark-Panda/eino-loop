package tools

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"

	"github.com/Mark-Panda/eino-loop/types"
)

// AnalyzeLogCallsite 分析日志调用位置，确定修复方案。
// 根据 SKILL 规则：
// - 检查函数是否有 context.Context 或 *fiber.Ctx 参数
// - 查找最近可用的 ctx 变量
// - 确定修复类型（go-logger 的 WithContext 或 gorm 的 WithContext）
func AnalyzeLogCallsite(file string, line int, funcName string) (*types.AnalyzeResult, error) {
	fset := token.NewFileSet()
	astFile, err := parser.ParseFile(fset, file, nil, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("解析文件 %s 失败: %w", file, err)
	}

	// 查找包含目标行的函数
	targetFunc := findEnclosingFunc(astFile, fset, line)
	if targetFunc == nil {
		return &types.AnalyzeResult{
			Location: types.FileLocation{
				File:     file,
				Line:     line,
				FuncName: funcName,
			},
			FixType:   "skip",
			RiskLevel: "high",
		}, nil
	}

	// 检测日志库类型
	logLib := detectLogLib(funcName)

	// 检查函数是否有 ctx 参数
	ctxParamName, hasCtx := findCtxParam(targetFunc)

	if hasCtx {
		return &types.AnalyzeResult{
			Location: types.FileLocation{
				File:     file,
				Line:     line,
				FuncName: funcName,
			},
			LogLib:     logLib,
			FixType:    determineFixType(logLib),
			HasCtx:     true,
			NearestCtx: ctxParamName,
			RiskLevel:  "low",
		}, nil
	}

	// 函数没有 ctx 参数，搜索函数体内的 ctx 变量
	localCtx := findLocalCtxVar(targetFunc, fset, line)
	if localCtx != "" {
		return &types.AnalyzeResult{
			Location: types.FileLocation{
				File:     file,
				Line:     line,
				FuncName: funcName,
			},
			LogLib:     logLib,
			FixType:    determineFixType(logLib),
			HasCtx:     false,
			NearestCtx: localCtx,
			RiskLevel:  "medium",
		}, nil
	}

	// 没有可用的 ctx
	return &types.AnalyzeResult{
		Location: types.FileLocation{
			File:     file,
			Line:     line,
			FuncName: funcName,
		},
		LogLib:    logLib,
		FixType:   "skip",
		RiskLevel: "high",
	}, nil
}

// findEnclosingFunc 查找包含目标行的函数声明。
func findEnclosingFunc(file *ast.File, fset *token.FileSet, targetLine int) *ast.FuncDecl {
	var result *ast.FuncDecl

	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok {
			return true
		}
		start := fset.Position(fn.Pos()).Line
		end := fset.Position(fn.End()).Line
		if targetLine >= start && targetLine <= end {
			result = fn
			return false
		}
		return true
	})

	return result
}

// findCtxParam 检查函数是否有 context.Context 或类似上下文参数。
func findCtxParam(fn *ast.FuncDecl) (string, bool) {
	if fn == nil || fn.Type.Params == nil {
		return "", false
	}

	for _, param := range fn.Type.Params.List {
		if isContextType(param.Type) {
			if len(param.Names) > 0 {
				return param.Names[0].Name, true
			}
			return "ctx", true
		}
	}
	return "", false
}

// isContextType 检查 AST 类型表达式是否是 context.Context 或 *fiber.Ctx。
func isContextType(expr ast.Expr) bool {
	// 检查 context.Context
	if sel, ok := expr.(*ast.SelectorExpr); ok {
		if ident, ok := sel.X.(*ast.Ident); ok {
			if ident.Name == "context" && sel.Sel.Name == "Context" {
				return true
			}
			// 检查 fiber.Ctx
			if sel.Sel.Name == "Ctx" {
				return true
			}
		}
	}

	// 检查 *fiber.Ctx（指针类型）
	if star, ok := expr.(*ast.StarExpr); ok {
		return isContextType(star.X)
	}

	return false
}

// findLocalCtxVar 搜索函数体中在目标行之前赋值的 ctx 变量。
func findLocalCtxVar(fn *ast.FuncDecl, fset *token.FileSet, targetLine int) string {
	if fn == nil || fn.Body == nil {
		return ""
	}

	var ctxVar string

	ast.Inspect(fn.Body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}

		line := fset.Position(assign.Pos()).Line
		if line >= targetLine {
			return false
		}

		// 检查 ctx 赋值模式
		for i, rhs := range assign.Rhs {
			if isCtxCreationExpr(rhs) || isContextWithExpr(rhs) {
				if i < len(assign.Lhs) {
					if ident, ok := assign.Lhs[i].(*ast.Ident); ok {
						name := strings.ToLower(ident.Name)
						if name == "ctx" || strings.HasSuffix(name, "ctx") || strings.HasSuffix(name, "Ctx") {
							ctxVar = ident.Name
						}
					}
				}
			}
		}

		return true
	})

	return ctxVar
}

// isCtxCreationExpr 检查表达式是否创建 context（context.Background()、context.TODO()）。
func isCtxCreationExpr(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	if ident.Name != "context" {
		return false
	}
	name := sel.Sel.Name
	return name == "Background" || name == "TODO"
}

// isContextWithExpr 检查表达式是否是 context.With*（WithCancel、WithTimeout 等）。
func isContextWithExpr(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return ident.Name == "context" && strings.HasPrefix(sel.Sel.Name, "With")
}

// detectLogLib 从函数名检测日志库类型。
func detectLogLib(funcName string) string {
	if strings.HasPrefix(funcName, "slog.") {
		return "slog"
	}
	if strings.HasPrefix(funcName, "log.") {
		return "fiber"
	}
	if strings.HasPrefix(funcName, "gorm.") {
		return "gorm"
	}
	if strings.HasPrefix(funcName, "ycLogger.") || strings.HasPrefix(funcName, "logger.") {
		return "go-logger"
	}
	return "logrus"
}

// determineFixType 根据日志库返回修复策略。
func determineFixType(logLib string) string {
	switch logLib {
	case "slog":
		return "context_param" // slog.Info → slog.InfoContext
	case "fiber":
		return "logger_receiver" // log.Info → log.WithContext(c).Info
	case "go-logger":
		return "logger_receiver" // ycLogger.Info → ycLogger.WithContext(ctx).Info
	case "gorm":
		return "logger_receiver" // db.First → db.WithContext(ctx).First
	case "logrus":
		return "logger_receiver" // entry.Info → entry.WithContext(ctx).Info
	default:
		return "skip"
	}
}
