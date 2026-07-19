package tools

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

// MiddlewareCheckResult 中间件检查结果
type MiddlewareCheckResult struct {
	Framework         string   // Kratos/Gin/Echo
	HasTracingServer  bool     // 是否有 tracing.Server()
	HasTracingClient  bool     // 是否有 tracing.Client()
	HasRecovery       bool     // 是否有 recovery.Recovery()
	HasLoggerMiddleware bool   // 是否有日志 middleware
	MiddlewareOrder    []string // middleware 顺序
	Issues            []string // 发现的问题
}

// CheckMiddleware 检查服务入口文件的 middleware 配置是否符合 SKILL 要求。
// 根据 SKILL 规则：
// - Kratos: 需要 tracing.Server()、日志 middleware、recovery.Recovery()
// - Gin: 需要 tracing/gin.EnableTrace()、go-logger GinMiddleware
// - Echo: 需要 tracing/echo.EnableTrace()、go-logger EchoMiddleware
func CheckMiddleware(filePath string) (*MiddlewareCheckResult, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filePath, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	// 收集导入信息
	imports := collectImports(file)

	// 检测框架类型
	framework := detectFramework(imports)

	result := &MiddlewareCheckResult{
		Framework: framework,
	}

	// 收集所有函数调用
	calls := collectCallExpressions(file)

	// 检查 tracing 相关
	result.HasTracingServer = hasCall(calls, "tracing", "Server")
	result.HasTracingClient = hasCall(calls, "tracing", "Client")

	// 检查 recovery
	result.HasRecovery = hasCall(calls, "recovery", "Recovery")

	// 检查日志 middleware
	switch framework {
	case "Kratos":
		result.HasLoggerMiddleware = hasCall(calls, "mdLogger", "Logger") || hasCall(calls, "logger", "KratosMiddleware")
	case "Gin":
		result.HasLoggerMiddleware = hasCall(calls, "loggerMiddleware", "GinMiddleware") || hasCall(calls, "gin", "Logger")
	case "Echo":
		result.HasLoggerMiddleware = hasCall(calls, "loggerMiddleware", "EchoMiddleware")
	}

	// 检查 EnableTrace
	switch framework {
	case "Gin":
		if !hasCall(calls, "gin", "EnableTrace") && !hasCall(calls, "tracing", "EnableTrace") {
			result.Issues = append(result.Issues, "缺少 tracing/gin.EnableTrace()")
		}
	case "Echo":
		if !hasCall(calls, "echo", "EnableTrace") && !hasCall(calls, "tracing", "EnableTrace") {
			result.Issues = append(result.Issues, "缺少 tracing/echo.EnableTrace()")
		}
	}

	// 通用检查
	if !result.HasTracingServer {
		result.Issues = append(result.Issues, "缺少 tracing.Server() 中间件")
	}
	if !result.HasRecovery {
		result.Issues = append(result.Issues, "缺少 recovery.Recovery() 中间件")
	}
	if !result.HasLoggerMiddleware {
		result.Issues = append(result.Issues, "缺少日志中间件")
	}

	return result, nil
}

// callInfo 存储函数调用信息
type callInfo struct {
	Receiver string // 接收者（包名或变量名）
	FuncName string // 函数名
}

// collectCallExpressions 收集文件中所有函数调用
func collectCallExpressions(file *ast.File) []callInfo {
	var calls []callInfo

	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		// 获取接收者名称
		receiver := ""
		if ident, ok := sel.X.(*ast.Ident); ok {
			receiver = ident.Name
		}

		calls = append(calls, callInfo{
			Receiver: receiver,
			FuncName: sel.Sel.Name,
		})

		return true
	})

	return calls
}

// hasCall 检查调用列表中是否有指定的函数调用
func hasCall(calls []callInfo, receiver, funcName string) bool {
	for _, call := range calls {
		if call.Receiver == receiver && call.FuncName == funcName {
			return true
		}
	}
	return false
}

// detectFramework 根据导入信息检测服务框架类型
func detectFramework(imports []importInfo) string {
	for _, imp := range imports {
		path := imp.path
		if strings.Contains(path, "go-kratos/kratos") {
			return "Kratos"
		}
		if strings.Contains(path, "gin-gonic/gin") {
			return "Gin"
		}
		if strings.Contains(path, "labstack/echo") {
			return "Echo"
		}
	}
	return "Unknown"
}
