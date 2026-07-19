package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// FeishuReporter 飞书报告生成器
type FeishuReporter struct {
	cliPath   string
	wikiSpace string
	chatID    string
}

// NewFeishuReporter 创建飞书报告生成器
func NewFeishuReporter(cliPath, wikiSpace, chatID string) *FeishuReporter {
	return &FeishuReporter{
		cliPath:   cliPath,
		wikiSpace: wikiSpace,
		chatID:    chatID,
	}
}

// CreateDocument 创建飞书文档，返回文档 URL
func (r *FeishuReporter) CreateDocument(ctx context.Context, title, markdownContent string) (string, error) {
	// 写入临时 Markdown 文件
	tmpFile, err := os.CreateTemp("", "eino-loop-report-*.md")
	if err != nil {
		return "", fmt.Errorf("创建临时文件失败: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(markdownContent); err != nil {
		tmpFile.Close()
		return "", fmt.Errorf("写入临时文件失败: %w", err)
	}
	tmpFile.Close()

	// 构建命令参数
	args := []string{"docs", "+create",
		"--title", title,
		"--markdown", fmt.Sprintf("@%s", tmpFile.Name()),
	}

	// 指定 wiki space（如果配置了）
	if r.wikiSpace != "" {
		args = append(args, "--wiki-space", r.wikiSpace)
	}

	// 执行命令
	cmd := exec.CommandContext(ctx, r.cliPath, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("创建飞书文档失败: %w: %s", err, string(output))
	}

	// 解析输出获取文档 URL
	docURL := parseDocURL(string(output))
	return docURL, nil
}

// SendCard 发送飞书消息卡片
func (r *FeishuReporter) SendCard(ctx context.Context, title, summary, docURL string) error {
	if r.chatID == "" {
		return fmt.Errorf("未配置飞书群聊 ID")
	}

	// 构建消息内容
	content := fmt.Sprintf("🔧 **%s**\n\n%s\n\n📄 [查看完整报告](%s)", title, summary, docURL)

	cmd := exec.CommandContext(ctx, r.cliPath, "message", "send",
		"--chat-id", r.chatID,
		"--type", "text",
		"--content", content,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("发送飞书消息失败: %w: %s", err, string(output))
	}

	return nil
}

// IsAvailable 检查 lark-cli 是否可用
func (r *FeishuReporter) IsAvailable() bool {
	_, err := exec.LookPath(r.cliPath)
	return err == nil
}

// GenerateLarkMarkdown 生成 Lark-flavored Markdown 格式的报告
func GenerateLarkMarkdown(report string, repoResults []RepoResultSummary) string {
	now := time.Now().Format("2006-01-02 15:04:05")

	md := fmt.Sprintf(`# 🔧 Log WithContext 修复报告

> 生成时间: %s

---

## 📊 扫描摘要

`, now)

	// 统计
	totalRepos := len(repoResults)
	successRepos := 0
	failedRepos := 0
	totalFixes := 0
	totalSkipped := 0

	for _, r := range repoResults {
		if r.Verified {
			successRepos++
		} else {
			failedRepos++
		}
		totalFixes += r.FixesApplied
		totalSkipped += r.Skipped
	}

	md += fmt.Sprintf(`| 指标 | 数量 |
|------|------|
| 扫描仓库数 | %d |
| 修复通过 | %d ✅ |
| 需人工审查 | %d ⚠️ |
| 修复调用点 | %d |
| 跳过（无 ctx） | %d |

---

## 📦 仓库详情

`, totalRepos, successRepos, failedRepos, totalFixes, totalSkipped)

	// 每个仓库的详情
	for _, r := range repoResults {
		status := "✅"
		if !r.Verified {
			status = "⚠️ 需人工审查"
		}

		md += fmt.Sprintf(`### %s %s

`, r.Repo, status)

		if r.Branch != "" {
			md += fmt.Sprintf("- **分支**: `%s`\n", r.Branch)
		}
		if r.CommitHash != "" {
			md += fmt.Sprintf("- **Commit**: `%s`\n", r.CommitHash[:7])
		}

		md += fmt.Sprintf("- **修复数**: %d\n", r.FixesApplied)
		md += fmt.Sprintf("- **跳过数**: %d\n", r.Skipped)

		if len(r.FilesChanged) > 0 {
			md += "- **修改文件**:\n"
			for _, f := range r.FilesChanged {
				md += fmt.Sprintf("  - `%s`\n", f)
			}
		}

		if len(r.RemainingIssues) > 0 {
			md += "- **遗留问题**:\n"
			for _, issue := range r.RemainingIssues {
				md += fmt.Sprintf("  - `%s:%d` - %s\n", issue.File, issue.Line, issue.FuncName)
			}
			md += "- **建议**: 手动为缺少 ctx 的函数添加 context 参数传递\n"
		}

		md += "\n---\n\n"
	}

	// 总结
	md += "## 📝 修复规则\n\n"
	md += "本次修复遵循 SKILL 规范：\n\n"
	md += "- **go-logger**: `ycLogger.Info(\"msg\")` → `ycLogger.WithContext(ctx).Info(\"msg\")`\n"
	md += "- **gorm**: `db.First(&u)` → `db.WithContext(ctx).First(&u)`\n"
	md += "- **seelog**: 标记为需迁移到 go-logger（seelog 不支持 WithContext）\n"
	md += "- **resty**: 标记为需添加 `SetContext(ctx)`\n\n"

	return md
}

// RepoResultSummary 仓库结果摘要
type RepoResultSummary struct {
	Repo            string
	Branch          string
	CommitHash      string
	FixesApplied    int
	Skipped         int
	Verified        bool
	FilesChanged    []string
	RemainingIssues []IssueSummary
}

// IssueSummary 问题摘要
type IssueSummary struct {
	File     string
	Line     int
	FuncName string
}

// parseDocURL 从 lark-cli 输出中解析文档 URL
func parseDocURL(output string) string {
	// lark-cli 输出格式通常是 JSON 或包含 URL 的文本
	// 简单提取 https:// 开头的 URL
	lines := splitLines(output)
	for _, line := range lines {
		if len(line) > 8 && (line[:8] == "https://" || line[:7] == "http://") {
			return line
		}
	}
	return ""
}
