package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// WorktreeManager 管理 git worktree 的创建和清理
type WorktreeManager struct {
	baseDir string // worktree 基础目录
}

// NewWorktreeManager 创建 worktree 管理器
func NewWorktreeManager(baseDir string) *WorktreeManager {
	if baseDir == "" {
		baseDir = filepath.Join(os.TempDir(), "eino-loop-worktrees")
	}
	return &WorktreeManager{baseDir: baseDir}
}

// WorktreeInfo 包含 worktree 的信息
type WorktreeInfo struct {
	RepoPath     string // 原始仓库路径
	WorktreePath string // worktree 路径
	BranchName   string // 分支名
	CreatedAt    time.Time
}

// CreateWorktree 为指定仓库创建独立的 git worktree。
// 使用 `git worktree add` 创建隔离的工作目录，避免直接修改原仓库。
func (wm *WorktreeManager) CreateWorktree(ctx context.Context, repoPath, branchName string) (*WorktreeInfo, error) {
	// 确保基础目录存在
	if err := os.MkdirAll(wm.baseDir, 0755); err != nil {
		return nil, fmt.Errorf("创建 worktree 基础目录失败: %w", err)
	}

	repoName := filepath.Base(repoPath)
	worktreePath := filepath.Join(wm.baseDir, fmt.Sprintf("%s-%s-%s", repoName, branchName, time.Now().Format("20060102150405")))

	// 获取当前 HEAD 的 commit hash
	headHash, err := gitRevParse(ctx, repoPath, "HEAD")
	if err != nil {
		return nil, fmt.Errorf("获取 HEAD 失败: %w", err)
	}

	// 创建 worktree：从当前 HEAD 创建新分支
	cmd := exec.CommandContext(ctx, "git", "worktree", "add",
		"-b", branchName,
		worktreePath,
		headHash,
	)
	cmd.Dir = repoPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("创建 worktree 失败: %w: %s", err, string(output))
	}

	return &WorktreeInfo{
		RepoPath:     repoPath,
		WorktreePath: worktreePath,
		BranchName:   branchName,
		CreatedAt:    time.Now(),
	}, nil
}

// CleanupWorktree 清理 worktree（删除 worktree 目录和 git 引用）
func (wm *WorktreeManager) CleanupWorktree(ctx context.Context, info *WorktreeInfo) error {
	if info == nil {
		return nil
	}

	// 使用 git worktree remove 清理
	cmd := exec.CommandContext(ctx, "git", "worktree", "remove", "--force", info.WorktreePath)
	cmd.Dir = info.RepoPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		// 如果 git worktree remove 失败，尝试直接删除目录
		os.RemoveAll(info.WorktreePath)
		return fmt.Errorf("清理 worktree 失败: %w: %s", err, string(output))
	}

	return nil
}

// CleanupAll 清理所有由 eino-loop 创建的 worktree
func (wm *WorktreeManager) CleanupAll(ctx context.Context, repoPath string) error {
	// 列出所有 worktree
	cmd := exec.CommandContext(ctx, "git", "worktree", "list", "--porcelain")
	cmd.Dir = repoPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("列出 worktree 失败: %w", err)
	}

	// 解析并清理包含 eino-loop 前缀的 worktree
	lines := splitLines(string(output))
	for _, line := range lines {
		if len(line) > 4 && line[:4] == "worktree " {
			wtPath := line[9:]
			if filepath.Dir(wtPath) == wm.baseDir {
				os.RemoveAll(wtPath)
			}
		}
	}

	return nil
}

// gitRevParse 执行 git rev-parse 获取引用的 commit hash
func gitRevParse(ctx context.Context, repoPath, ref string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", ref)
	cmd.Dir = repoPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git rev-parse %s 失败: %w", ref, err)
	}
	return trimSpace(string(output)), nil
}

// splitLines 按行分割字符串
func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

// trimSpace 去除字符串首尾空白
func trimSpace(s string) string {
	start := 0
	end := len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}
