package tools

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"

	"github.com/Mark-Panda/eino-loop/types"
)

// AnalyzeLogCallsite analyzes a log call site to determine the fix strategy.
// Checks if the enclosing function has a ctx parameter and finds the nearest ctx variable.
func AnalyzeLogCallsite(file string, line int, logFuncName string) (*types.AnalyzeResult, error) {
	fset := token.NewFileSet()
	astFile, err := parser.ParseFile(fset, file, nil, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse file %s: %w", file, err)
	}

	// Find the function containing the target line
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

	// Detect log library from function name
	logLib := detectLogLib(logFuncName)

	// Check if function has ctx parameter
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

	// No ctx in function signature — search for ctx in function body
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

	// No ctx available at all
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

// findEnclosingFunc finds the function declaration or function literal containing the given line.
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
			return false // Found it, stop descending
		}
		return true
	})

	return result
}

// findCtxParam checks if the function has a parameter of type context.Context.
// Returns the parameter name and true if found.
func findCtxParam(fn *ast.FuncDecl) (string, bool) {
	if fn == nil || fn.Type.Params == nil {
		return "", false
	}

	for _, param := range fn.Type.Params.List {
		if isContextType(param.Type) {
			// Return the first parameter name
			if len(param.Names) > 0 {
				return param.Names[0].Name, true
			}
			return "ctx", true
		}
	}
	return "", false
}

// isContextType checks if an AST type expression is context.Context or fiber.Ctx.
func isContextType(expr ast.Expr) bool {
	// Check for context.Context
	if sel, ok := expr.(*ast.SelectorExpr); ok {
		if ident, ok := sel.X.(*ast.Ident); ok {
			if ident.Name == "context" && sel.Sel.Name == "Context" {
				return true
			}
			// Check for fiber.Ctx
			if sel.Sel.Name == "Ctx" {
				return true
			}
		}
	}

	// Check for *fiber.Ctx (pointer type)
	if star, ok := expr.(*ast.StarExpr); ok {
		return isContextType(star.X)
	}

	return false
}

// findLocalCtxVar searches the function body for local variables of type context.Context
// that are assigned before the target line.
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
			return false // Don't look past the target line
		}

		// Check for: ctx := something.Context() or ctx := context.Background()
		// or: ctx, cancel := context.WithCancel(parent)
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

// isCtxCreationExpr checks if an expression creates a context (context.Background(), context.TODO(), etc.)
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

// isContextWithExpr checks if an expression is context.With* (WithCancel, WithTimeout, etc.)
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

// detectLogLib determines the log library from the function name.
func detectLogLib(funcName string) string {
	if strings.HasPrefix(funcName, "slog.") {
		return "slog"
	}
	if strings.HasPrefix(funcName, "log.") {
		return "fiber"
	}
	// logrus functions are just the function name without prefix
	return "logrus"
}

// determineFixType returns the fix strategy based on the log library.
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
