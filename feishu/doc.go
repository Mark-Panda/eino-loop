package feishu

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/Mark-Panda/eino-loop/types"
)

// CreateDoc 从 markdown 内容创建飞书云文档。
// 使用 lark-cli 创建文档。
func CreateDoc(ctx context.Context, cliPath, title, content, spaceID string) (docURL, docID string, err error) {
	// 将内容写入临时文件
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

	// 构建命令参数
	args := []string{"doc", "create", "--title", title, "--content-file", tmpFile.Name()}
	if spaceID != "" {
		args = append(args, "--space-id", spaceID)
	}

	cmd := exec.CommandContext(ctx, cliPath, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("lark-cli doc create: %w: %s", err, string(output))
	}

	// 解析输出以获取文档 URL 和 ID
	// lark-cli 输出格式："Document created: <url> (id: <id>)"
	docURL, docID = parseDocOutput(string(output))
	return docURL, docID, nil
}

// SendCard 向飞书群聊发送交互式消息卡片。
func SendCard(ctx context.Context, cliPath, chatID, cardJSON string) (messageID string, err error) {
	// 将卡片 JSON 写入临时文件
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

// IsAvailable 检查 lark-cli 是否已安装且可访问。
func IsAvailable(cliPath string) bool {
	_, err := exec.LookPath(cliPath)
	return err == nil
}

// BuildMessageCard 构建飞书交互式消息卡片 JSON。
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

// parseDocOutput 从 lark-cli 输出中提取文档 URL 和 ID。
func parseDocOutput(output string) (url, id string) {
	// 简单解析 - 根据实际 lark-cli 输出格式调整
	// 预期格式："Document created: https://xxx.feishu.cn/docx/xxx (id: xxx)"
	fmt.Sscanf(output, "Document created: %s (id: %s)", &url, &id)
	return url, id
}

// parseMessageOutput 从 lark-cli 输出中提取消息 ID。
func parseMessageOutput(output string) string {
	var msgID string
	fmt.Sscanf(output, "Message sent: %s", &msgID)
	return msgID
}
