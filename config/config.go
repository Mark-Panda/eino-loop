package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all configuration for the eino-loop system.
type Config struct {
	// Repo settings
	RepoRoot     string // Root directory containing Go repositories
	TargetBranch string // Target branch to scan (default: master)
	FixBranchTpl string // Fix branch name template

	// Scan settings
	ScanInterval time.Duration
	MaxRepos     int
	MaxRetries   int // Max fix-verify retry rounds (default: 3)
	DryRun       bool // Only scan, don't fix

	// Log functions to detect
	LogFunctions []LogFunc

	// Feishu settings
	FeishuEnabled  bool
	FeishuChatID   string // Feishu group chat ID
	FeishuCLIPath  string // Path to lark-cli binary
	FeishuDocSpace string // Feishu document space ID
}

// LogFunc describes a log library and its functions to detect.
type LogFunc struct {
	Library   string   // "slog" / "fiber" / "logrus"
	Functions []string // ["Info", "Warn", "Error", ...]
	CtxForm   string   // WithContext call pattern
}

// DefaultLogFunctions returns the standard log functions to detect per SKILL.md.
func DefaultLogFunctions() []LogFunc {
	return []LogFunc{
		{
			Library:   "slog",
			Functions: []string{"Info", "Debug", "Warn", "Error"},
			CtxForm:   "Context", // slog.Info → slog.InfoContext
		},
		{
			Library:   "fiber",
			Functions: []string{"Info", "Debug", "Warn", "Error", "Fatal", "Panic"},
			CtxForm:   "WithContext", // log.Info → log.WithContext(c).Info
		},
		{
			Library:   "logrus",
			Functions: []string{"Info", "Debug", "Warn", "Error", "Fatal", "Panic", "Trace", "Print"},
			CtxForm:   "WithContext", // entry.Info → entry.WithContext(ctx).Info
		},
	}
}

// Load reads configuration from environment variables with sensible defaults.
func Load() *Config {
	cfg := &Config{
		RepoRoot:     envOrDefault("EINO_LOOP_REPO_ROOT", "."),
		TargetBranch: envOrDefault("EINO_LOOP_TARGET_BRANCH", "master"),
		FixBranchTpl: envOrDefault("EINO_LOOP_FIX_BRANCH_TPL", "fix/slog-withcontext-{date}"),
		ScanInterval: durationOrDefault("EINO_LOOP_SCAN_INTERVAL", 1*time.Hour),
		MaxRepos:     intOrDefault("EINO_LOOP_MAX_REPOS", 50),
		MaxRetries:   intOrDefault("EINO_LOOP_MAX_RETRIES", 3),
		DryRun:       boolOrDefault("EINO_LOOP_DRY_RUN", false),
		LogFunctions: DefaultLogFunctions(),

		FeishuEnabled:  boolOrDefault("EINO_LOOP_FEISHU_ENABLED", false),
		FeishuChatID:   os.Getenv("EINO_LOOP_FEISHU_CHAT_ID"),
		FeishuCLIPath:  envOrDefault("EINO_LOOP_FEISHU_CLI_PATH", "lark-cli"),
		FeishuDocSpace: os.Getenv("EINO_LOOP_FEISHU_DOC_SPACE"),
	}
	return cfg
}

// FixBranchName generates the fix branch name from the template.
func (c *Config) FixBranchName() string {
	date := time.Now().Format("20060102")
	return strings.ReplaceAll(c.FixBranchTpl, "{date}", date)
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func durationOrDefault(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}

func intOrDefault(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func boolOrDefault(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return fallback
}
