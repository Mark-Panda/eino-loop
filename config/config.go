package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config 包含 eino-loop 系统的所有配置。
type Config struct {
	// 仓库设置
	RepoRoot     string // 包含 Go 仓库的根目录
	TargetBranch string // 要扫描的目标分支（默认：master）
	FixBranchTpl string // 修复分支名称模板

	// 扫描设置
	ScanInterval time.Duration
	MaxRepos     int
	MaxRetries   int // 修复-验证的最大重试轮数（默认：3）
	DryRun       bool // 仅扫描，不修复

	// 需要检测的日志函数
	LogFunctions []LogFunc

	// LLM 设置
	LLMBaseURL string // OpenAI 兼容端点
	LLMAPIKey  string // API 密钥
	LLMModel   string // 模型名称（如 gpt-4o, deepseek-chat）
	LLMMaxStep int    // Agent 最大推理步数（默认：20）

	// 飞书设置
	FeishuEnabled  bool
	FeishuChatID   string // 飞书群聊 ID
	FeishuCLIPath  string // lark-cli 二进制文件路径
	FeishuDocSpace string // 飞书文档空间 ID
}

// LogFunc 描述一个日志库及其需要检测的函数。
type LogFunc struct {
	Library   string   // "slog" / "fiber" / "logrus"
	Functions []string // ["Info", "Warn", "Error", ...]
	CtxForm   string   // WithContext 调用模式
}

// DefaultLogFunctions 根据 SKILL.md 返回标准的待检测日志函数。
func DefaultLogFunctions() []LogFunc {
	return []LogFunc{
		{
			Library:   "go-logger",
			Functions: []string{"Info", "Warn", "Error", "Debug", "Fatal", "Infof", "Warnf", "Errorf", "Debugf", "Fatalf", "Infow", "Warnw", "Errorw", "Debugw"},
			CtxForm:   "WithContext",
		},
		{
			Library:   "gorm",
			Functions: []string{"First", "Find", "Last", "Take", "Count", "Pluck", "Scan", "Create", "Save", "Update", "Updates", "Delete", "Exec"},
			CtxForm:   "WithContext",
		},
		{
			Library:   "seelog",
			Functions: []string{"Info", "Warn", "Error", "Debug", "Critical", "Infof", "Warnf", "Errorf", "Debugf", "Criticalf"},
			CtxForm:   "",
		},
		{
			Library:   "resty",
			Functions: []string{"Get", "Post", "Put", "Delete", "Patch", "Head", "Options"},
			CtxForm:   "SetContext",
		},
	}
}

// Load 从环境变量读取配置，并提供合理的默认值。
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

		// LLM 配置
		LLMBaseURL: envOrDefault("EINO_LOOP_LLM_BASE_URL", "https://api.openai.com/v1"),
		LLMAPIKey:  os.Getenv("EINO_LOOP_LLM_API_KEY"),
		LLMModel:   envOrDefault("EINO_LOOP_LLM_MODEL", "gpt-4o"),
		LLMMaxStep: intOrDefault("EINO_LOOP_LLM_MAX_STEP", 20),

		FeishuEnabled:  boolOrDefault("EINO_LOOP_FEISHU_ENABLED", false),
		FeishuChatID:   os.Getenv("EINO_LOOP_FEISHU_CHAT_ID"),
		FeishuCLIPath:  envOrDefault("EINO_LOOP_FEISHU_CLI_PATH", "lark-cli"),
		FeishuDocSpace: os.Getenv("EINO_LOOP_FEISHU_DOC_SPACE"),
	}
	return cfg
}

// FixBranchName 根据模板生成修复分支名称。
func (c *Config) FixBranchName() string {
	date := time.Now().Format("20060102")
	return strings.ReplaceAll(c.FixBranchTpl, "{date}", date)
}

// Validate 校验配置的完整性和合理性。
func (c *Config) Validate() error {
	if c.RepoRoot == "" {
		return fmt.Errorf("EINO_LOOP_REPO_ROOT 不能为空")
	}
	if _, err := os.Stat(c.RepoRoot); os.IsNotExist(err) {
		return fmt.Errorf("EINO_LOOP_REPO_ROOT 路径不存在: %s", c.RepoRoot)
	}
	if c.MaxRepos <= 0 {
		return fmt.Errorf("EINO_LOOP_MAX_REPOS 必须为正数，当前值: %d", c.MaxRepos)
	}
	if c.MaxRetries <= 0 {
		return fmt.Errorf("EINO_LOOP_MAX_RETRIES 必须为正数，当前值: %d", c.MaxRetries)
	}
	if c.ScanInterval <= 0 {
		return fmt.Errorf("EINO_LOOP_SCAN_INTERVAL 必须为正数")
	}
	if c.LLMAPIKey == "" {
		return fmt.Errorf("EINO_LOOP_LLM_API_KEY 不能为空")
	}
	if c.LLMModel == "" {
		return fmt.Errorf("EINO_LOOP_LLM_MODEL 不能为空")
	}
	if c.LLMMaxStep <= 0 {
		c.LLMMaxStep = 20 // 默认值
	}
	return nil
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
