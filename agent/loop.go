package agent

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"time"

	"github.com/Mark-Panda/eino-loop/config"
	"github.com/Mark-Panda/eino-loop/feishu"
	"github.com/Mark-Panda/eino-loop/tools"
	"github.com/Mark-Panda/eino-loop/types"
)

// LoopInput 是整个循环管道的输入。
type LoopInput struct {
	RepoRoot string
}

// LoopRunner 是编译后的循环图的接口。
type LoopRunner interface {
	Invoke(ctx context.Context, input LoopInput) (string, error)
}

// BuildLoopGraph 构建并编译管道。
// 管道：Scanner → Puller → Detector → Analyzer → Fixer → Verifier → (重试) → Reporter → Feishu
func BuildLoopGraph(cfg *config.Config) (LoopRunner, error) {
	return &graphRunner{cfg: cfg}, nil
}

// graphRunner 将完整管道实现为有向图。
type graphRunner struct {
	cfg *config.Config
}

func (r *graphRunner) Invoke(ctx context.Context, input LoopInput) (string, error) {
	cfg := r.cfg
	repoRoot := input.RepoRoot
	if repoRoot == "" {
		repoRoot = cfg.RepoRoot
	}

	log.Printf("[Scanner] Scanning repositories in %s", repoRoot)

	repos, err := tools.ScanRepositories(ctx, repoRoot, cfg.MaxRepos)
	if err != nil {
		return "", fmt.Errorf("scan repositories: %w", err)
	}
	log.Printf("[Scanner] Found %d repositories", len(repos))

	if len(repos) == 0 {
		return "No repositories found", nil
	}

	var allResults []types.RepoFixResult

	for _, repoPath := range repos {
		repoName := filepath.Base(repoPath)
		log.Printf("[Pipeline] Processing repository: %s", repoName)

		result, err := r.processRepo(ctx, repoPath, repoName)
		if err != nil {
			log.Printf("[Pipeline] Error processing %s: %v", repoName, err)
			allResults = append(allResults, types.RepoFixResult{
				Repo: repoName,
				FixResult: types.FixResult{
					Errors: []string{err.Error()},
				},
			})
			continue
		}
		allResults = append(allResults, *result)
	}

	log.Printf("[Reporter] Generating report")
	report, summary := tools.GenerateReport(ctx, allResults)

	if cfg.FeishuEnabled {
		r.sendToFeishu(ctx, allResults, summary)
	}

	return report, nil
}

// processRepo 对单个仓库执行完整管道。
func (r *graphRunner) processRepo(ctx context.Context, repoPath, repoName string) (*types.RepoFixResult, error) {
	cfg := r.cfg

	// 拉取最新代码
	log.Printf("[Puller] Pulling latest %s for %s", cfg.TargetBranch, repoName)
	if err := tools.PullLatest(ctx, repoPath, cfg.TargetBranch); err != nil {
		return nil, fmt.Errorf("pull latest: %w", err)
	}

	// 检测日志问题
	log.Printf("[Detector] Scanning for log calls without context")
	locations, err := tools.FindLogsWithoutContext(ctx, repoPath, convertLogFuncs(cfg.LogFunctions))
	if err != nil {
		return nil, fmt.Errorf("find logs: %w", err)
	}
	log.Printf("[Detector] Found %d log calls without context in %s", len(locations), repoName)

	if len(locations) == 0 {
		return &types.RepoFixResult{
			Repo: repoName,
			VerifyResult: types.VerifyResult{
				CompileOK:      true,
				AllIssuesFixed: true,
				RegressionFree: true,
			},
		}, nil
	}

	if cfg.DryRun {
		log.Printf("[DRY-RUN] Would fix %d log calls in %s", len(locations), repoName)
		return &types.RepoFixResult{
			Repo: repoName,
			FixResult: types.FixResult{
				OriginalIssues: locations,
			},
		}, nil
	}

	// 创建修复分支
	log.Printf("[Fixer] Creating fix branch for %s", repoName)
	branchName, worktreePath, err := tools.CreateFixBranch(ctx, repoPath, cfg)
	if err != nil {
		return nil, fmt.Errorf("create fix branch: %w", err)
	}

	// 分析并修复
	fixResult := types.FixResult{
		Repo:           repoName,
		Branch:         branchName,
		WorktreePath:   worktreePath,
		OriginalIssues: locations,
	}

	fixCount, skipped, changedFiles, fixErrs := r.analyzeAndFix(ctx, worktreePath, locations)
	fixResult.FixesApplied = fixCount
	fixResult.Skipped = skipped
	fixResult.FilesChanged = changedFiles
	fixResult.Errors = fixErrs

	log.Printf("[Fixer] Applied %d fixes, skipped %d in %s", fixCount, skipped, repoName)

	if fixCount == 0 {
		return &types.RepoFixResult{
			Repo:      repoName,
			Branch:    branchName,
			FixResult: fixResult,
			VerifyResult: types.VerifyResult{
				CompileOK:      true,
				AllIssuesFixed: true,
				RegressionFree: true,
			},
		}, nil
	}

	// 验证与重试循环
	log.Printf("[Verifier] Starting verification for %s", repoName)
	verifyResult := tools.VerifyAndRetry(ctx, cfg, worktreePath, fixResult, func(ctx context.Context) (int, error) {
		remaining, _ := tools.FindLogsWithoutContext(ctx, worktreePath, convertLogFuncs(cfg.LogFunctions))
		count, _, _, errs := r.analyzeAndFix(ctx, worktreePath, remaining)
		return count, aggregateErrors(errs)
	})

	log.Printf("[Verifier] Result for %s: compile=%v allFixed=%v regression=%v retries=%d",
		repoName, verifyResult.CompileOK, verifyResult.AllIssuesFixed, verifyResult.RegressionFree, verifyResult.RetryCount)

	// 验证通过后提交
	var commitHash string
	if verifyResult.AllPassed() {
		commitMsg := fmt.Sprintf("fix: add WithContext to %d log calls\n\nAuto-fixed by eino-loop\nRepository: %s\nFixes: %d\nSkipped: %d",
			fixCount, repoName, fixCount, skipped)

		log.Printf("[Fixer] Committing changes for %s", repoName)
		hash, err := tools.CommitAndPush(ctx, worktreePath, commitMsg)
		if err != nil {
			log.Printf("[Fixer] Commit/push failed for %s: %v (changes preserved locally)", repoName, err)
			fixResult.Errors = append(fixResult.Errors, fmt.Sprintf("commit/push: %v", err))
		}
		commitHash = hash
	}

	return &types.RepoFixResult{
		Repo:         repoName,
		Branch:       branchName,
		CommitHash:   commitHash,
		FixResult:    fixResult,
		VerifyResult: verifyResult,
		RetryRounds:  verifyResult.RetryCount,
	}, nil
}

// analyzeAndFix 分析每个位置并应用修复。
func (r *graphRunner) analyzeAndFix(ctx context.Context, worktreePath string, locations []types.FileLocation) (fixed int, skipped int, changedFiles []string, errors []string) {
	changedSet := make(map[string]bool)

	for _, loc := range locations {
		if ctx.Err() != nil {
			errors = append(errors, "context cancelled")
			break
		}

		analysis, err := tools.AnalyzeLogCallsite(loc.File, loc.Line, loc.FuncName)
		if err != nil {
			errors = append(errors, fmt.Sprintf("analyze %s:%d: %v", loc.File, loc.Line, err))
			continue
		}

		if analysis.FixType == "skip" {
			skipped++
			log.Printf("[Analyzer] Skipping %s:%d (risk=%s, no ctx available)", loc.File, loc.Line, analysis.RiskLevel)
			continue
		}

		applied, _, err := tools.ApplyLogFix(ctx, worktreePath, *analysis)
		if err != nil {
			errors = append(errors, fmt.Sprintf("fix %s:%d: %v", loc.File, loc.Line, err))
			continue
		}

		if applied {
			fixed++
			if !changedSet[loc.File] {
				changedFiles = append(changedFiles, loc.File)
				changedSet[loc.File] = true
			}
		}
	}

	return fixed, skipped, changedFiles, errors
}

// sendToFeishu 将报告发送到飞书。
func (r *graphRunner) sendToFeishu(ctx context.Context, results []types.RepoFixResult, summary types.ReportSummary) {
	cfg := r.cfg

	if !feishu.IsAvailable(cfg.FeishuCLIPath) {
		log.Printf("[Feishu] lark-cli not available at %s, skipping", cfg.FeishuCLIPath)
		return
	}

	title := fmt.Sprintf("Log WithContext 修复报告 %s", time.Now().Format("2006-01-02"))
	content := tools.GenerateFeishuDocContent(results)

	docURL, _, err := feishu.CreateDoc(ctx, cfg.FeishuCLIPath, title, content, cfg.FeishuDocSpace)
	if err != nil {
		log.Printf("[Feishu] Failed to create document: %v", err)
		docURL = ""
	}

	if cfg.FeishuChatID != "" {
		cardJSON := feishu.BuildMessageCard(docURL, summary)
		_, err := feishu.SendCard(ctx, cfg.FeishuCLIPath, cfg.FeishuChatID, cardJSON)
		if err != nil {
			log.Printf("[Feishu] Failed to send message: %v", err)
		} else {
			log.Printf("[Feishu] Report sent to chat %s", cfg.FeishuChatID)
		}
	}
}

func convertLogFuncs(cfgFuncs []config.LogFunc) []tools.LogFunc {
	result := make([]tools.LogFunc, len(cfgFuncs))
	for i, f := range cfgFuncs {
		result[i] = tools.LogFunc{
			Library:   f.Library,
			Functions: f.Functions,
			CtxForm:   f.CtxForm,
		}
	}
	return result
}

func aggregateErrors(errs []string) error {
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("errors: %v", errs)
}
