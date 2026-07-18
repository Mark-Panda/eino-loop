package types

// FileLocation 表示一个需要修复的日志调用位置。
type FileLocation struct {
	File     string
	Line     int
	FuncName string
	LogExpr  string
}

// AnalyzeResult 包含对日志调用位置的分析结果。
type AnalyzeResult struct {
	Location   FileLocation
	LogLib     string // "slog" / "fiber" / "logrus"
	FixType    string // "context_param" / "logger_receiver" / "skip"
	HasCtx     bool
	NearestCtx string
	RiskLevel  string // "low" / "medium" / "high"
}

// CompileError 表示一个 Go 编译错误。
type CompileError struct {
	File    string
	Line    int
	Message string
}

// FixResult 包含单个仓库的修复结果。
type FixResult struct {
	Repo           string
	Branch         string
	WorktreePath   string
	CommitHash     string
	FilesChanged   []string
	FixesApplied   int
	Skipped        int
	OriginalIssues []FileLocation
	Errors         []string
}

// VerifyResult 包含修复后的验证结果。
type VerifyResult struct {
	CompileOK      bool
	AllIssuesFixed bool
	RegressionFree bool
	Remaining      []FileLocation
	CompileErrors  []CompileError
	RetryCount     int
	MaxRetries     int
	NeedsHuman     bool
}

// AllPassed 返回所有验证级别是否全部通过。
func (v VerifyResult) AllPassed() bool {
	return v.CompileOK && v.AllIssuesFixed && v.RegressionFree
}

// CanRetry 返回是否还可以继续重试修复。
func (v VerifyResult) CanRetry() bool {
	return !v.NeedsHuman && v.RetryCount < v.MaxRetries
}

// RepoFixResult 是单个仓库的最终结果。
type RepoFixResult struct {
	Repo         string
	Branch       string
	CommitHash   string
	VerifyResult VerifyResult
	FixResult    FixResult
	RetryRounds  int
}

// ReportSummary 包含汇总统计信息。
type ReportSummary struct {
	TotalRepos   int
	ProblemRepos int
	FixedRepos   int
	FailedRepos  int
	TotalFiles   int
	TotalFixes   int
	TotalSkipped int
}
