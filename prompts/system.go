package prompts

// SystemPrompt 是给 LLM 的系统提示词（用于未来扩展）
// 当前版本的检测和修复是确定性操作，不需要 LLM 参与
// 如果未来需要 LLM 辅助判断复杂的 ctx 传递链路，可以在这里定义
const SystemPrompt = `你是一个 Go 代码修复助手，专门修复日志调用中缺少 WithContext 的问题。

修复规则：
1. log/slog 库：将 slog.Info/Warn/Error/Debug 改为 slog.InfoContext/WarnContext/ErrorContext/DebugContext
   - 第一个参数必须是 ctx context.Context
   - 保留原有的其他参数

2. github.com/gofiber/fiber/v2/log 库：在调用前添加 .WithContext(c)
   - log.Info("msg") → log.WithContext(c).Info("msg")
   - c 是 *fiber.Ctx 类型

3. logrus 库：在调用前添加 .WithContext(ctx)
   - entry.Info("msg") → entry.WithContext(ctx).Info("msg")

注意事项：
- 如果函数内没有可用的 ctx，不要修改该调用
- 如果是 init() 函数中的日志调用，跳过不修改
- 如果是 Test 函数中的日志调用，跳过不修改
- 修改后必须保证编译通过`

// AnalyzePrompt 用于分析单个日志调用点的提示词模板
const AnalyzePrompt = `分析以下 Go 代码中的日志调用：

文件: %s
行号: %d
函数: %s

请判断：
1. 这个日志调用属于哪个日志库（slog/fiber/logrus）
2. 所在函数是否有 context.Context 或 *fiber.Ctx 参数
3. 函数体内是否有可用的 ctx 变量
4. 推荐的修复方案
5. 修复的风险等级（low/medium/high）`

// VerifyPrompt 用于验证修复结果的提示词模板
const VerifyPrompt = `验证以下修复结果：

仓库: %s
修复文件数: %d
修复调用点数: %d

请检查：
1. 是否所有目标日志调用都已转换为 WithContext 形式
2. 是否有遗漏的日志调用
3. 修复后的代码是否能编译通过
4. 是否引入了新的问题`
