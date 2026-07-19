package tools

import (
	"fmt"
	"path/filepath"
	"strings"
)

// PathValidator 路径安全校验器
// 确保所有文件操作都在指定的仓库目录内
type PathValidator struct {
	allowedRoots []string // 允许操作的根目录列表
	deniedPaths  []string // 禁止修改的路径模式
}

// NewPathValidator 创建路径校验器
func NewPathValidator(repoRoot string) *PathValidator {
	absRoot, _ := filepath.Abs(repoRoot)

	return &PathValidator{
		allowedRoots: []string{absRoot},
		deniedPaths: []string{
			".git",           // Git 内部文件
			"vendor",         // vendor 目录
			"go.mod",         // 模块文件
			"go.sum",         // 依赖校验文件
			".claude",        // Claude 配置
			".env",           // 环境变量
			"_test.go",       // 测试文件
			"testdata",       // 测试数据
			"migration",      // 数据库迁移
			"migrations",     // 数据库迁移
			"schema.go",      // GORM schema 定义
			"model.go",       // 数据模型定义（通常不需要改）
		},
	}
}

// ValidatePath 校验文件路径是否在允许范围内
func (pv *PathValidator) ValidatePath(filePath string) error {
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return fmt.Errorf("无法解析路径: %w", err)
	}

	// 检查是否在允许的根目录内
	allowed := false
	for _, root := range pv.allowedRoots {
		rel, err := filepath.Rel(root, absPath)
		if err == nil && !strings.HasPrefix(rel, "..") {
			allowed = true
			break
		}
	}
	if !allowed {
		return fmt.Errorf("路径 %s 不在允许的操作目录内", filePath)
	}

	// 检查是否匹配禁止模式
	for _, denied := range pv.deniedPaths {
		if pv.matchesDenied(absPath, denied) {
			return fmt.Errorf("路径 %s 匹配禁止模式 %s，不允许修改", filePath, denied)
		}
	}

	return nil
}

// ValidateReadPath 校验读取路径（限制较少，只检查根目录）
func (pv *PathValidator) ValidateReadPath(filePath string) error {
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return fmt.Errorf("无法解析路径: %w", err)
	}

	for _, root := range pv.allowedRoots {
		rel, err := filepath.Rel(root, absPath)
		if err == nil && !strings.HasPrefix(rel, "..") {
			return nil
		}
	}

	return fmt.Errorf("路径 %s 不在允许的读取目录内", filePath)
}

// matchesDenied 检查路径是否匹配禁止模式
func (pv *PathValidator) matchesDenied(absPath, pattern string) bool {
	// 检查路径中是否包含禁止的目录/文件名
	parts := strings.Split(absPath, string(filepath.Separator))
	for _, part := range parts {
		if part == pattern {
			return true
		}
	}
	// 检查文件名后缀
	if strings.HasSuffix(absPath, pattern) {
		return true
	}
	return false
}

// AddDeniedPath 添加禁止修改的路径模式
func (pv *PathValidator) AddDeniedPath(pattern string) {
	pv.deniedPaths = append(pv.deniedPaths, pattern)
}

// AddAllowedRoot 添加允许操作的根目录
func (pv *PathValidator) AddAllowedRoot(root string) {
	absRoot, _ := filepath.Abs(root)
	pv.allowedRoots = append(pv.allowedRoots, absRoot)
}
