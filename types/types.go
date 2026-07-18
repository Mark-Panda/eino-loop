package types

// FileLocation represents a log call site that needs fixing.
type FileLocation struct {
	File     string
	Line     int
	FuncName string
	LogExpr  string
}

// AnalyzeResult contains the analysis of a log call site.
type AnalyzeResult struct {
	Location   FileLocation
	LogLib     string // "slog" / "fiber" / "logrus"
	FixType    string // "context_param" / "logger_receiver" / "skip"
	HasCtx     bool
	NearestCtx string
	RiskLevel  string // "low" / "medium" / "high"
}

// CompileError represents a Go compilation error.
type CompileError struct {
	File    string
	Line    int
	Message string
}

// FixResult contains the result of fixing a single repository.
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

// VerifyResult contains the verification result after fixing.
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

// AllPassed returns true if all verification levels passed.
func (v VerifyResult) AllPassed() bool {
	return v.CompileOK && v.AllIssuesFixed && v.RegressionFree
}

// CanRetry returns true if we can still retry fixing.
func (v VerifyResult) CanRetry() bool {
	return !v.NeedsHuman && v.RetryCount < v.MaxRetries
}

// RepoFixResult is the final result for a single repository.
type RepoFixResult struct {
	Repo         string
	Branch       string
	CommitHash   string
	VerifyResult VerifyResult
	FixResult    FixResult
	RetryRounds  int
}

// ReportSummary contains aggregate statistics.
type ReportSummary struct {
	TotalRepos   int
	ProblemRepos int
	FixedRepos   int
	FailedRepos  int
	TotalFiles   int
	TotalFixes   int
	TotalSkipped int
}
