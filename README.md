# eino-loop

基于 [CloudWeGo Eino](https://github.com/cloudwego/eino) 框架的 Loop Engineering 系统。

## 功能

自动扫描 Go 代码仓库中的日志调用，检测缺少 `WithContext` 的问题并自动修复：

1. **定期扫描** — 拉取指定目录下所有仓库 master 分支最新代码
2. **智能检测** — AST 分析识别 slog / fiber-log / logrus 中缺少 WithContext 的日志调用
3. **自动修复** — 创建 git worktree 分支，执行 AST 重写修复
4. **三级验证** — 编译验证 → 重扫描验证 → 回归验证，不通过则循环重修
5. **飞书通知** — 生成飞书云文档报告，发送飞书消息卡片通知

## 架构

```
Scanner → Analyzer → Fixer → Verifier → (循环重修) → Reporter → 飞书文档 + 飞书消息
```

基于 Eino `compose.Graph` DAG 编排，支持验证闭环自动重试。

## 文档

- [实现规划](PLAN.md)
- [Skill: 日志修复规范](skill-fixing-trace-id-logs/SKILL.md)
