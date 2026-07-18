package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Mark-Panda/eino-loop/types"
)

// GenerateReport generates a markdown report from all repo fix results.
func GenerateReport(ctx context.Context, results []types.RepoFixResult) (string, types.ReportSummary) {
	summary := types.ReportSummary{
		TotalRepos: len(results),
	}

	for _, r := range results {
		summary.TotalFiles += len(r.FixResult.FilesChanged)
		summary.TotalFixes += r.FixResult.FixesApplied
		summary.TotalSkipped += r.FixResult.Skipped

		if r.FixResult.FixesApplied > 0 || len(r.FixResult.Errors) > 0 {
			summary.ProblemRepos++
		}
		if r.VerifyResult.AllPassed() {
			summary.FixedRepos++
		} else if r.VerifyResult.NeedsHuman {
			summary.FailedRepos++
		}
	}

	var b strings.Builder

	b.WriteString("# Log WithContext 修复报告\n")
	b.WriteString(fmt.Sprintf("生成时间: %s\n\n", time.Now().Format("2006-01-02 15:04:05")))

	// Summary table
	b.WriteString("## 扫描摘要\n\n")
	b.WriteString("| 指标 | 数量 |\n")
	b.WriteString("|------|------|\n")
	b.WriteString(fmt.Sprintf("| 扫描仓库数 | %d |\n", summary.TotalRepos))
	b.WriteString(fmt.Sprintf("| 发现问题仓库 | %d |\n", summary.ProblemRepos))
	b.WriteString(fmt.Sprintf("| 修复文件数 | %d |\n", summary.TotalFiles))
	b.WriteString(fmt.Sprintf("| 修复调用点 | %d |\n", summary.TotalFixes))
	b.WriteString(fmt.Sprintf("| 跳过（无 ctx 可用）| %d |\n", summary.TotalSkipped))
	b.WriteString(fmt.Sprintf("| 验证通过 | %d ✅ |\n", summary.FixedRepos))
	b.WriteString(fmt.Sprintf("| 需人工审查 | %d ⚠️ |\n\n", summary.FailedRepos))

	// Repository details
	b.WriteString("## 仓库详情\n\n")
	for _, r := range results {
		b.WriteString(formatRepoResult(r))
		b.WriteString("\n")
	}

	return b.String(), summary
}

func formatRepoResult(r types.RepoFixResult) string {
	var b strings.Builder

	// Header with status icon
	status := "✅"
	if r.VerifyResult.NeedsHuman {
		status = "⚠️"
	} else if !r.VerifyResult.AllPassed() {
		status = "❌"
	}
	b.WriteString(fmt.Sprintf("### 📦 %s %s\n", r.Repo, status))

	// Branch and commit
	if r.Branch != "" {
		b.WriteString(fmt.Sprintf("- **分支**: %s\n", r.Branch))
	}
	if r.CommitHash != "" {
		b.WriteString(fmt.Sprintf("- **Commit**: %s\n", r.CommitHash[:7]))
	}

	// Verify result
	verifyStatus := formatVerifyStatus(r.VerifyResult)
	b.WriteString(fmt.Sprintf("- **验证**: %s\n", verifyStatus))

	// Retry rounds
	if r.RetryRounds > 0 {
		b.WriteString(fmt.Sprintf("- **修复轮次**: %d 轮\n", r.RetryRounds))
	}

	// Files changed
	if len(r.FixResult.FilesChanged) > 0 {
		b.WriteString("- **修复文件**:\n")
		b.WriteString("  | 文件 | 修复数 |\n")
		b.WriteString("  |------|--------|\n")
		for _, f := range r.FixResult.FilesChanged {
			b.WriteString(fmt.Sprintf("  | %s | - |\n", f))
		}
	}

	// Remaining issues
	if len(r.VerifyResult.Remaining) > 0 {
		b.WriteString("- **遗留问题**:\n")
		b.WriteString("  | 文件 | 行号 | 函数 |\n")
		b.WriteString("  |------|------|------|\n")
		for _, loc := range r.VerifyResult.Remaining {
			b.WriteString(fmt.Sprintf("  | %s | %d | %s |\n", loc.File, loc.Line, loc.FuncName))
		}
		b.WriteString("- **建议**: 手动为缺少 ctx 的函数添加 context 参数传递\n")
	}

	// Errors
	if len(r.FixResult.Errors) > 0 {
		b.WriteString("- **错误**:\n")
		for _, e := range r.FixResult.Errors {
			b.WriteString(fmt.Sprintf("  - %s\n", e))
		}
	}

	return b.String()
}

func formatVerifyStatus(v types.VerifyResult) string {
	var parts []string
	if v.CompileOK {
		parts = append(parts, "编译✅")
	} else {
		parts = append(parts, "编译❌")
	}
	if v.AllIssuesFixed {
		parts = append(parts, "重扫描✅")
	} else {
		parts = append(parts, "重扫描❌")
	}
	if v.RegressionFree {
		parts = append(parts, "回归✅")
	} else {
		parts = append(parts, "回归⚠️")
	}
	return strings.Join(parts, " ")
}

// GenerateFeishuDocContent generates markdown content suitable for Feishu document.
func GenerateFeishuDocContent(results []types.RepoFixResult) string {
	report, _ := GenerateReport(context.Background(), results)
	return report
}
