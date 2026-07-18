package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Mark-Panda/eino-loop/types"
)

// GenerateReport 根据所有仓库修复结果生成 markdown 报告。
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

	// 摘要表格
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

	// 仓库详情
	b.WriteString("## 仓库详情\n\n")
	for _, r := range results {
		b.WriteString(formatRepoResult(r))
		b.WriteString("\n")
	}

	return b.String(), summary
}

func formatRepoResult(r types.RepoFixResult) string {
	var b strings.Builder

	// 带状态图标的标题
	status := "✅"
	if r.VerifyResult.NeedsHuman {
		status = "⚠️"
	} else if !r.VerifyResult.AllPassed() {
		status = "❌"
	}
	b.WriteString(fmt.Sprintf("### 📦 %s %s\n", r.Repo, status))

	// 分支和提交
	if r.Branch != "" {
		b.WriteString(fmt.Sprintf("- **分支**: %s\n", r.Branch))
	}
	if r.CommitHash != "" {
		b.WriteString(fmt.Sprintf("- **Commit**: %s\n", r.CommitHash[:7]))
	}

	// 验证结果
	verifyStatus := formatVerifyStatus(r.VerifyResult)
	b.WriteString(fmt.Sprintf("- **验证**: %s\n", verifyStatus))

	// 修复轮次
	if r.RetryRounds > 0 {
		b.WriteString(fmt.Sprintf("- **修复轮次**: %d 轮\n", r.RetryRounds))
	}

	// 变更文件
	if len(r.FixResult.FilesChanged) > 0 {
		b.WriteString("- **修复文件**:\n")
		b.WriteString("  | 文件 | 修复数 |\n")
		b.WriteString("  |------|--------|\n")
		for _, f := range r.FixResult.FilesChanged {
			b.WriteString(fmt.Sprintf("  | %s | - |\n", f))
		}
	}

	// 遗留问题
	if len(r.VerifyResult.Remaining) > 0 {
		b.WriteString("- **遗留问题**:\n")
		b.WriteString("  | 文件 | 行号 | 函数 |\n")
		b.WriteString("  |------|------|------|\n")
		for _, loc := range r.VerifyResult.Remaining {
			b.WriteString(fmt.Sprintf("  | %s | %d | %s |\n", loc.File, loc.Line, loc.FuncName))
		}
		b.WriteString("- **建议**: 手动为缺少 ctx 的函数添加 context 参数传递\n")
	}

	// 错误
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
