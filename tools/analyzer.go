package tools

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"

	"github.com/Mark-Panda/eino-loop/types"
)

// AnalyzeLogCallsite 分析日志调用位置，确定修复策略。
// 检查包含该调用的函数是否具有 ctx 参数，并查找最近的 ctx 变量。
func AnalyzeLogCallsite(file string, line int, logFuncName string) (*types.AnalyzeResult, error) {
	fset := token.NewFileSet()
	astFile, err := parser.ParseFile(fset, file, nil, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse file %s: %w", file, err)
	}

	// 查找包含目标行的函数
	targetFunc := findEnclosingFunc(astFile, fset, line)
	if targetFunc == nil {
		return &types.AnalyzeResult{
			Location: types.FileLocation{
				File:     file,
				Line:     line,
				FuncName: logFuncName,
			},
			FixType:   "skip",
			RiskLevel: "high",
		}, nil
	}

	// 根据函数名检测日志库
	logLib := detectLogLib(logFuncName)

	// 检查函数是否有 ctx 参数
	ctxParamName, hasCtx := findCtxParam(targetFunc)

	if hasCtx {
		return &types.AnalyzeResult{
			Location: types.FileLocation{
				File:     file,
				Line:     line,
				FuncName: logFuncName,
			},
			LogLib:     logLib,
			FixType:    determineFixType(logLib),
			HasCtx:     true,
			NearestCtx: ctxParamName,
			RiskLevel:  "low",
		}, nil
	}

	// 函数签名中没有 ctx — 在函数体中搜索 ctx
	localCtx := findLocalCtxVar(targetFunc, fset, line)
	if localCtx != "" {
		return &types.AnalyzeResult{
			Location: types.FileLocation{
				File:     file,
				Line:     line,
				FuncName: logFuncName,
			},
			LogLib:     logLib,
			FixType:    determineFixType(logLib),
			HasCtx:     false,
			NearestCtx: localCtx,
			RiskLevel:  "medium",
		}, nil
	}

	// 完全没有可用的 ctx
	return &types.AnalyzeResult{
		Location: types.FileLocation{
			File:     file,
			Line:     line,
			FuncName: logFuncName,
		},
		LogLib:    logLib,
		FixType:   "skip",
		RiskLevel: "high",
	}, nil
}

// findEnclosingFunc 查找包含给定行的函数声明或函数字面量。
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
			return false // 找到了，停止向下遍历
		}
		return true
	})

	return result
}

// findCtxParam 检查函数是否有 context.Context 类型的参数。
// 如果找到，返回参数名和 true。
func findCtxParam(fn *ast.FuncDecl) (string, bool) {
	if fn == nil || fn.Type.Params == nil {
		return "", false
	}

	for _, param := range fn.Type.Params.List {
		if isContextType(param.Type) {
			// 返回第一个参数名
			if len(param.Names) > 0 {
				return param.Names[0].Name, true
			}
			return "ctx", true
		}
	}
	return "", false
}

// isContextType 检查 AST 类型表达式是否为 context.Context 或 fiber.Ctx。
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

// findLocalCtxVar 在函数体中搜索在目标行之前赋值的 context.Context 类型的局部变量。
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
			return false // 不要在目标行之后继续查找
		}

		// 检查：ctx := something.Context() 或 ctx := context.Background()
		// 或：ctx, cancel := context.WithCancel(parent)
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

// isCtxCreationExpr 检查表达式是否创建了 context（context.Background()、context.TODO() 等）。
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

// isContextWithExpr 检查表达式是否为 context.With*（WithCancel、WithTimeout 等）。
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

// detectLogLib 根据函数名判断日志库。
func detectLogLib(funcName string) string {
	if strings.HasPrefix(funcName, "slog.") {
		return "slog"
	}
	if strings.HasPrefix(funcName, "log.") {
		return "fiber"
	}
	// logrus 函数名没有前缀
	return "logrus"
}

// determineFixType 根据日志库返回修复策略。
func determineFixType(logLib string) string {
	switch logLib {
	case "slog":
		return "context_param" // slog.Info → slog.InfoContext
	case "fiber":
		return "logger_receiver" // log.Info → log.WithContext(c).Info
	case "logrus":
		return "logger_receiver" // entry.Info → entry.WithContext(ctx).Info
	default:
		return "skip"
	}
}
