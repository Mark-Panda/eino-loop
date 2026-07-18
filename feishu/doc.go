package feishu

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/Mark-Panda/eino-loop/types"
)

// CreateDoc creates a Feishu cloud document from markdown content.
// Uses lark-cli to create the document.
func CreateDoc(ctx context.Context, cliPath, title, content, spaceID string) (docURL, docID string, err error) {
	// Write content to temp file
	tmpFile, err := os.CreateTemp("", "eino-loop-report-*.md")
	if err != nil {
		return "", "", fmt.Errorf("create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(content); err != nil {
		tmpFile.Close()
		return "", "", fmt.Errorf("write temp file: %w", err)
	}
	tmpFile.Close()

	// Build command args
	args := []string{"doc", "create", "--title", title, "--content-file", tmpFile.Name()}
	if spaceID != "" {
		args = append(args, "--space-id", spaceID)
	}

	cmd := exec.CommandContext(ctx, cliPath, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("lark-cli doc create: %w: %s", err, string(output))
	}

	// Parse output to get doc URL and ID
	// lark-cli output format: "Document created: <url> (id: <id>)"
	docURL, docID = parseDocOutput(string(output))
	return docURL, docID, nil
}

// SendCard sends an interactive message card to a Feishu chat.
func SendCard(ctx context.Context, cliPath, chatID, cardJSON string) (messageID string, err error) {
	// Write card JSON to temp file
	tmpFile, err := os.CreateTemp("", "eino-loop-card-*.json")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(cardJSON); err != nil {
		tmpFile.Close()
		return "", fmt.Errorf("write temp file: %w", err)
	}
	tmpFile.Close()

	cmd := exec.CommandContext(ctx, cliPath, "message", "send",
		"--chat-id", chatID,
		"--type", "interactive",
		"--card", tmpFile.Name(),
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("lark-cli message send: %w: %s", err, string(output))
	}

	return parseMessageOutput(string(output)), nil
}

// IsAvailable checks if lark-cli is installed and accessible.
func IsAvailable(cliPath string) bool {
	_, err := exec.LookPath(cliPath)
	return err == nil
}

// BuildMessageCard builds a Feishu interactive message card JSON.
func BuildMessageCard(docURL string, summary types.ReportSummary) string {
	status := "✅ 全部修复通过"
	if summary.FailedRepos > 0 {
		status = fmt.Sprintf("⚠️ %d 个仓库需人工审查", summary.FailedRepos)
	}

	return fmt.Sprintf(`{
  "config": {"wide_screen_mode": true},
  "header": {
    "title": {"tag": "plain_text", "content": "🔧 Log WithContext 自动修复报告"},
    "template": "blue"
  },
  "elements": [
    {
      "tag": "div",
      "text": {
        "tag": "lark_md",
        "content": "**状态**: %s\n**扫描仓库**: %d\n**发现问题**: %d\n**修复通过**: %d\n**修复调用点**: %d"
      }
    },
    {
      "tag": "action",
      "actions": [
        {
          "tag": "button",
          "text": {"tag": "plain_text", "content": "📄 查看完整报告"},
          "url": "%s",
          "type": "primary"
        }
      ]
    }
  ]
}`, status, summary.TotalRepos, summary.ProblemRepos, summary.FixedRepos, summary.TotalFixes, docURL)
}

// parseDocOutput extracts doc URL and ID from lark-cli output.
func parseDocOutput(output string) (url, id string) {
	// Simple parsing - adjust based on actual lark-cli output format
	// Expected: "Document created: https://xxx.feishu.cn/docx/xxx (id: xxx)"
	fmt.Sscanf(output, "Document created: %s (id: %s)", &url, &id)
	return url, id
}

// parseMessageOutput extracts message ID from lark-cli output.
func parseMessageOutput(output string) string {
	var msgID string
	fmt.Sscanf(output, "Message sent: %s", &msgID)
	return msgID
}
