package tools

import (
	"context"

	toolutils "github.com/cloudwego/eino/components/tool/utils"

	"github.com/cloudwego/eino/components/tool"
)

// inferToolImpl 使用 eino 的 InferTool 从类型化函数创建工具
func inferToolImpl[T any](name, desc string, fn func(context.Context, T) (string, error)) (tool.InvokableTool, error) {
	return toolutils.InferTool[T, string](name, desc, fn)
}
