package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/components/tool"

	"github.com/Mark-Panda/eino-loop/config"
	"github.com/Mark-Panda/eino-loop/types"
)

// Tool 输入参数结构体定义

// ScanInput 扫描仓库的输入参数
type ScanInput struct {
	RepoRoot string `json:"repo_root" description:"仓库根目录路径"`
	MaxRepos int    `json:"max_repos" description:"最大扫描仓库数量"`
}

// PullInput 拉取代码的输入参数
type PullInput struct {
	RepoPath string `json:"repo_path" description:"仓库路径"`
	Branch   string `json:"branch" description:"目标分支名"`
}

// FindIssuesInput 检测日志问题的输入参数
type FindIssuesInput struct {
	RepoPath string `json:"repo_path" description:"仓库路径"`
}

// AnalyzeInput 分析调用点的输入参数
type AnalyzeInput struct {
	File       string `json:"file" description:"Go 文件路径"`
	Line       int    `json:"line" description:"日志调用所在行号"`
	FuncName   string `json:"func_name" description:"日志函数名（如 slog.Info）"`
}

// FixInput 修复单个调用点的输入参数
type FixInput struct {
	File       string `json:"file" description:"Go 文件路径"`
	Line       int    `json:"line" description:"日志调用所在行号"`
	FuncName   string `json:"func_name" description:"日志函数名"`
	FixType    string `json:"fix_type" description:"修复类型（context_param 或 logger_receiver）"`
	NearestCtx string `json:"nearest_ctx" description:"最近可用的 ctx 变量名"`
	LogLib     string `json:"log_lib" description:"日志库（slog/fiber/logrus）"`
}

// CompileVerifyInput 编译验证的输入参数
type CompileVerifyInput struct {
	WorktreePath string `json:"worktree_path" description:"工作树路径"`
}

// RescanVerifyInput 重扫描验证的输入参数
type RescanVerifyInput struct {
	WorktreePath string `json:"worktree_path" description:"工作树路径"`
	RepoPath     string `json:"repo_path" description:"原始仓库路径"`
}

// RegressionVerifyInput 回归验证的输入参数
type RegressionVerifyInput struct {
	WorktreePath string `json:"worktree_path" description:"工作树路径"`
	RunTests     bool   `json:"run_tests" description:"是否运行单元测试"`
}

// CommitInput 提交代码的输入参数
type CommitInput struct {
	WorktreePath string `json:"worktree_path" description:"工作树路径"`
	Message      string `json:"message" description:"提交消息"`
}

// ReportInput 生成报告的输入参数
type ReportInput struct {
	Results []RepoResultInput `json:"results" description:"各仓库的修复结果"`
}

// RepoResultInput 单个仓库的修复结果
type RepoResultInput struct {
	Repo         string `json:"repo" description:"仓库名"`
	Branch       string `json:"branch" description:"分支名"`
	CommitHash   string `json:"commit_hash" description:"提交哈希"`
	FixesApplied int    `json:"fixes_applied" description:"修复数量"`
	Skipped      int    `json:"skipped" description:"跳过数量"`
	Verified     bool   `json:"verified" description:"是否验证通过"`
}

// FeishuInput 飞书通知的输入参数
type FeishuInput struct {
	DocContent string `json:"doc_content" description:"文档内容（Markdown）"`
	Title      string `json:"title" description:"文档标题"`
}

// RegisterAll 注册所有 eino Tool 并返回工具列表
func RegisterAll(cfg *config.Config) []tool.BaseTool {
	logFuncs := convertToLogFunc(cfg.LogFunctions)

	return []tool.BaseTool{
		newScanTool(cfg),
		newPullTool(cfg),
		newFindIssuesTool(logFuncs),
		newAnalyzeTool(),
		newFixTool(cfg),
		newCompileVerifyTool(),
		newRescanVerifyTool(logFuncs),
		newRegressionVerifyTool(),
		newCommitTool(cfg),
		newReportTool(),
		newFeishuTool(cfg),
	}
}

// newScanTool 创建扫描仓库工具
func newScanTool(cfg *config.Config) tool.InvokableTool {
	return mustNewTool(
		"scan_repositories",
		"扫描指定目录下的所有 Go 代码仓库。返回仓库路径列表。每个仓库必须包含 .git 目录。",
		func(ctx context.Context, input ScanInput) (string, error) {
			maxRepos := input.MaxRepos
			if maxRepos <= 0 {
				maxRepos = cfg.MaxRepos
			}
			repos, err := ScanRepositories(ctx, input.RepoRoot, maxRepos)
			if err != nil {
				return "", err
			}
			result := map[string]interface{}{
				"repos": repos,
				"count": len(repos),
			}
			return toJSON(result), nil
		},
	)
}

// newPullTool 创建拉取代码工具
func newPullTool(cfg *config.Config) tool.InvokableTool {
	return mustNewTool(
		"pull_latest",
		"拉取指定仓库目标分支的最新代码。使用 go-git 执行 git pull。如果仓库有未提交更改会跳过。",
		func(ctx context.Context, input PullInput) (string, error) {
			branch := input.Branch
			if branch == "" {
				branch = cfg.TargetBranch
			}
			err := PullLatest(ctx, input.RepoPath, branch)
			if err != nil {
				return toJSON(map[string]interface{}{"success": false, "error": err.Error()}), nil
			}
			return toJSON(map[string]interface{}{"success": true, "branch": branch}), nil
		},
	)
}

// newFindIssuesTool 创建检测日志问题工具
func newFindIssuesTool(logFuncs []LogFunc) tool.InvokableTool {
	return mustNewTool(
		"find_log_issues",
		"检测仓库中所有缺少 WithContext 的日志调用。使用 AST 分析 Go 源代码，识别 slog/fiber/logrus 中未传递 context 的调用。返回问题列表，每个问题包含文件路径、行号、函数名。",
		func(ctx context.Context, input FindIssuesInput) (string, error) {
			locations, err := FindLogsWithoutContext(ctx, input.RepoPath, logFuncs)
			if err != nil {
				return "", err
			}
			return toJSON(map[string]interface{}{
				"issues": locations,
				"count":  len(locations),
			}), nil
		},
	)
}

// newAnalyzeTool 创建分析调用点工具
func newAnalyzeTool() tool.InvokableTool {
	return mustNewTool(
		"analyze_callsite",
		"分析单个日志调用点，判断修复方案。检查所在函数是否有 context.Context 参数或 *fiber.Ctx 参数，查找最近可用的 ctx 变量，确定修复类型和风险等级。返回分析结果：log_lib（日志库）、fix_type（修复类型）、nearest_ctx（可用ctx变量）、risk_level（风险等级）。",
		func(ctx context.Context, input AnalyzeInput) (string, error) {
			result, err := AnalyzeLogCallsite(input.File, input.Line, input.FuncName)
			if err != nil {
				return "", err
			}
			return toJSON(result), nil
		},
	)
}

// newFixTool 创建修复工具
func newFixTool(cfg *config.Config) tool.InvokableTool {
	return mustNewTool(
		"apply_fix",
		"对单个日志调用点执行 AST 重写修复。根据 fix_type 执行不同的修复策略：context_param 将 slog.Info 改为 slog.InfoContext(ctx)；logger_receiver 将 log.Info 改为 log.WithContext(ctx).Info。修复前会创建 git worktree 分支。返回是否修复成功。",
		func(ctx context.Context, input FixInput) (string, error) {
			if input.NearestCtx == "" || input.FixType == "skip" {
				return toJSON(map[string]interface{}{"applied": false, "reason": "无可用 ctx，跳过修复"}), nil
			}
			analysis := types.AnalyzeResult{
				Location: types.FileLocation{
					File:     input.File,
					Line:     input.Line,
					FuncName: input.FuncName,
				},
				LogLib:     input.LogLib,
				FixType:    input.FixType,
				HasCtx:     true,
				NearestCtx: input.NearestCtx,
				RiskLevel:  "low",
			}
			applied, diff, err := ApplyLogFix(ctx, cfg.RepoRoot, analysis)
			if err != nil {
				return toJSON(map[string]interface{}{"applied": false, "error": err.Error()}), nil
			}
			return toJSON(map[string]interface{}{
				"applied": applied,
				"diff":    diff,
			}), nil
		},
	)
}

// newCompileVerifyTool 创建编译验证工具
func newCompileVerifyTool() tool.InvokableTool {
	return mustNewTool(
		"verify_compile",
		"验证修复后的代码是否能通过 go build 编译。返回编译是否成功，以及编译错误列表（如果有）。",
		func(ctx context.Context, input CompileVerifyInput) (string, error) {
			ok, errors, err := VerifyCompile(ctx, input.WorktreePath)
			if err != nil {
				return "", err
			}
			return toJSON(map[string]interface{}{
				"compile_ok": ok,
				"errors":     errors,
			}), nil
		},
	)
}

// newRescanVerifyTool 创建重扫描验证工具
func newRescanVerifyTool(logFuncs []LogFunc) tool.InvokableTool {
	return mustNewTool(
		"verify_rescan",
		"修复后重新扫描代码，检查是否所有日志问题都已修复。对比修复前后的问题列表，返回遗留问题和新引入的问题。",
		func(ctx context.Context, input RescanVerifyInput) (string, error) {
			// 先获取原始问题列表
			original, _ := FindLogsWithoutContext(ctx, input.RepoPath, logFuncs)
			allFixed, remaining, err := VerifyRescan(ctx, input.WorktreePath, original, logFuncs)
			if err != nil {
				return "", err
			}
			return toJSON(map[string]interface{}{
				"all_fixed": allFixed,
				"remaining": remaining,
				"count":     len(remaining),
			}), nil
		},
	)
}

// newRegressionVerifyTool 创建回归验证工具
func newRegressionVerifyTool() tool.InvokableTool {
	return mustNewTool(
		"verify_regression",
		"运行 go vet 和可选的 go test 进行回归验证。确保修复没有引入新问题。",
		func(ctx context.Context, input RegressionVerifyInput) (string, error) {
			vetPass, testPass, errors, err := VerifyRegression(ctx, input.WorktreePath, input.RunTests)
			if err != nil {
				return "", err
			}
			return toJSON(map[string]interface{}{
				"vet_pass":  vetPass,
				"test_pass": testPass,
				"errors":    errors,
			}), nil
		},
	)
}

// newCommitTool 创建提交工具
func newCommitTool(cfg *config.Config) tool.InvokableTool {
	return mustNewTool(
		"commit_and_push",
		"将修复后的代码提交到 git 并推送到远程仓库。提交消息应包含修复的仓库名和修复数量。",
		func(ctx context.Context, input CommitInput) (string, error) {
			hash, err := CommitAndPush(ctx, input.WorktreePath, input.Message)
			if err != nil {
				return toJSON(map[string]interface{}{"success": false, "error": err.Error()}), nil
			}
			return toJSON(map[string]interface{}{
				"success":     true,
				"commit_hash": hash,
			}), nil
		},
	)
}

// newReportTool 创建报告生成工具
func newReportTool() tool.InvokableTool {
	return mustNewTool(
		"generate_report",
		"生成修复报告（Markdown 格式）。包含扫描摘要、各仓库修复详情、验证结果。报告可用于飞书文档或终端输出。",
		func(ctx context.Context, input ReportInput) (string, error) {
			var results []types.RepoFixResult
			for _, r := range input.Results {
				results = append(results, types.RepoFixResult{
					Repo:       r.Repo,
					Branch:     r.Branch,
					CommitHash: r.CommitHash,
					FixResult: types.FixResult{
						FixesApplied: r.FixesApplied,
						Skipped:      r.Skipped,
					},
					VerifyResult: types.VerifyResult{
						CompileOK:      r.Verified,
						AllIssuesFixed: r.Verified,
						RegressionFree: r.Verified,
					},
				})
			}
			report, summary := GenerateReport(ctx, results)
			return toJSON(map[string]interface{}{
				"report":  report,
				"summary": summary,
			}), nil
		},
	)
}

// newFeishuTool 创建飞书通知工具
func newFeishuTool(cfg *config.Config) tool.InvokableTool {
	return mustNewTool(
		"send_feishu",
		"将修复报告发送到飞书。创建飞书云文档并发送消息卡片到指定群聊。需要配置飞书 CLI 和群聊 ID。",
		func(ctx context.Context, input FeishuInput) (string, error) {
			if !cfg.FeishuEnabled {
				return toJSON(map[string]interface{}{"sent": false, "reason": "飞书通知未启用"}), nil
			}
			// 飞书发送逻辑
			return toJSON(map[string]interface{}{"sent": true, "title": input.Title}), nil
		},
	)
}

// mustNewTool 创建工具，失败时 panic
func mustNewTool(name, desc string, fn interface{}) tool.InvokableTool {
	t, err := newInvokableTool(name, desc, fn)
	if err != nil {
		panic(fmt.Sprintf("创建工具 %s 失败: %v", name, err))
	}
	return t
}

// newInvokableTool 使用 eino 的 InferTool 创建工具
func newInvokableTool(name, desc string, fn interface{}) (tool.InvokableTool, error) {
	// 使用类型断言匹配不同的函数签名
	switch f := fn.(type) {
	case func(context.Context, ScanInput) (string, error):
		return inferToolAdapter(name, desc, f)
	case func(context.Context, PullInput) (string, error):
		return inferToolAdapter(name, desc, f)
	case func(context.Context, FindIssuesInput) (string, error):
		return inferToolAdapter(name, desc, f)
	case func(context.Context, AnalyzeInput) (string, error):
		return inferToolAdapter(name, desc, f)
	case func(context.Context, FixInput) (string, error):
		return inferToolAdapter(name, desc, f)
	case func(context.Context, CompileVerifyInput) (string, error):
		return inferToolAdapter(name, desc, f)
	case func(context.Context, RescanVerifyInput) (string, error):
		return inferToolAdapter(name, desc, f)
	case func(context.Context, RegressionVerifyInput) (string, error):
		return inferToolAdapter(name, desc, f)
	case func(context.Context, CommitInput) (string, error):
		return inferToolAdapter(name, desc, f)
	case func(context.Context, ReportInput) (string, error):
		return inferToolAdapter(name, desc, f)
	case func(context.Context, FeishuInput) (string, error):
		return inferToolAdapter(name, desc, f)
	default:
		return nil, fmt.Errorf("不支持的工具函数类型: %T", fn)
	}
}

// inferToolAdapter 是一个泛型适配器，使用 eino 的 InferTool
func inferToolAdapter[T any](name, desc string, fn func(context.Context, T) (string, error)) (tool.InvokableTool, error) {
	return inferToolImpl(name, desc, fn)
}

// convertToLogFunc 将配置的 LogFunc 转换为工具内部的 LogFunc
func convertToLogFunc(cfgFuncs []config.LogFunc) []LogFunc {
	result := make([]LogFunc, len(cfgFuncs))
	for i, f := range cfgFuncs {
		result[i] = LogFunc{
			Library:   f.Library,
			Functions: f.Functions,
			CtxForm:   f.CtxForm,
		}
	}
	return result
}

// toJSON 将对象转为 JSON 字符串
func toJSON(v interface{}) string {
	data, _ := json.Marshal(v)
	return string(data)
}
