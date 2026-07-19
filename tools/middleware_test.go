package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckMiddleware_Kratos(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "http.go")

	content := `package server

import (
	"github.com/go-kratos/kratos/v2/middleware/recovery"
	"github.com/go-kratos/kratos/v2/transport/http"
	"gitlab.yc345.tv/backend/tracing"
	mdLogger "gitlab.yc345.tv/backend/go-logger/kratos"
)

func NewHTTPServer(c *conf.Server) *http.Server {
	var opts = []http.ServerOption{
		http.Middleware(
			tracing.Server(),
			mdLogger.Logger(config),
			recovery.Recovery(),
		),
	}
	return http.NewServer(opts...)
}
`
	os.WriteFile(filePath, []byte(content), 0644)

	result, err := CheckMiddleware(filePath)
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}

	if result.Framework != "Kratos" {
		t.Errorf("期望框架 Kratos，得到 %s", result.Framework)
	}
	if !result.HasTracingServer {
		t.Error("期望 HasTracingServer=true")
	}
	if !result.HasRecovery {
		t.Error("期望 HasRecovery=true")
	}
	if !result.HasLoggerMiddleware {
		t.Error("期望 HasLoggerMiddleware=true")
	}
	if len(result.Issues) != 0 {
		t.Errorf("期望无问题，得到: %v", result.Issues)
	}
}

func TestCheckMiddleware_Gin_MissingTracing(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "app.go")

	content := `package app

import (
	"github.com/gin-gonic/gin"
)

func NewRouter() *gin.Engine {
	r := gin.Default()
	// 缺少 tracing/gin.EnableTrace()
	// 缺少日志中间件
	return r
}
`
	os.WriteFile(filePath, []byte(content), 0644)

	result, err := CheckMiddleware(filePath)
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}

	if result.Framework != "Gin" {
		t.Errorf("期望框架 Gin，得到 %s", result.Framework)
	}
	if result.HasTracingServer {
		t.Error("期望 HasTracingServer=false（缺少 tracing）")
	}
	if len(result.Issues) == 0 {
		t.Error("期望有问题报告")
	}
}

func TestDetectFramework(t *testing.T) {
	tests := []struct {
		imports []importInfo
		want    string
	}{
		{[]importInfo{{path: "github.com/go-kratos/kratos/v2"}}, "Kratos"},
		{[]importInfo{{path: "github.com/gin-gonic/gin"}}, "Gin"},
		{[]importInfo{{path: "github.com/labstack/echo/v4"}}, "Echo"},
		{[]importInfo{{path: "fmt"}}, "Unknown"},
	}

	for _, tt := range tests {
		got := detectFramework(tt.imports)
		if got != tt.want {
			t.Errorf("detectFramework(%v) = %q，期望 %q", tt.imports, got, tt.want)
		}
	}
}
