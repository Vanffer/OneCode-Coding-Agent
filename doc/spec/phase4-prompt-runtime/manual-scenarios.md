# 结构化系统提示运行时人工场景

> 这些场景用于 Phase 4 完成后的定性对比和人工验收，不是自动化 benchmark。

## 场景 1：普通编辑任务先读再改

**输入**

```text
帮我把 grep 工具的最大结果数从 100 调整成 120，并跑相关测试
```

**期望观察点**

- 如有需要，Agent 会使用 `grep` 或 `glob` 定位相关文件。
- Agent 会在编辑前先使用 `read_file` 读取文件内容。
- Agent 会使用 `edit_file` 做精确的局部修改。
- Agent 会在编辑后运行相关 Go 测试。
- 最终回复会说明修改了哪个文件，以及实际验证结果。

**非目标**

- 不评价这个小改动之外的模型代码质量。
- 不要求本场景一定出现 cache usage。

## 场景 2：路径查找优先使用 Glob

**输入**

```text
帮我找出所有 prompt 相关的 Go 文件
```

**期望观察点**

- Agent 会使用 `glob`，例如通过 `**/*prompt*.go` 这类路径模式查找，或在 `src/internal/prompt` 下搜索。
- Agent 不会在没有工具依据的情况下猜测文件路径。
- 当 `glob` 足够完成任务时，Agent 不应只为了执行 `find` 或 `dir` 这类 shell 命令而使用 `bash`。

**非目标**

- 具体 glob pattern 可以不同。
- 除非用户要求查看内容，否则 Agent 不需要读取每个文件。

## 场景 3：内容搜索优先使用 Grep

**输入**

```text
帮我查一下哪里还直接引用了 SystemPrompt
```

**期望观察点**

- Agent 会使用 `grep` 搜索文件内容。
- Agent 会基于真实工具输出报告匹配结果。
- Agent 不依赖记忆或手工猜测。

**非目标**

- Agent 不需要修改任何文件。

## 场景 4：Plan Mode 保持只读

**输入**

```text
/plan 帮我设计一个权限确认系统
```

**期望观察点**

- Agent 只能使用 `glob`、`grep`、`read_file` 等只读工具。
- Agent 不会使用 `write_file`、`edit_file` 或 `bash`。
- Agent 输出实现计划，而不是直接修改文件。
- 生成的计划会保存为 `/do` 可消费的 pending plan。

**非目标**

- 不要求计划完美，也不要求覆盖后续阶段的所有细节。
- 不应有任何文件发生修改。

## 场景 5：多轮 Plan Mode Reminder

**输入**

```text
/plan 详细分析 agent、llm、tools 三层如何加入权限系统
```

**期望观察点**

- 这个任务应该会触发多轮读取或搜索工具调用。
- 即使经过多轮工具调用，Agent 仍然保持只读。
- 如果 loop 到达第 6 轮或更后面，在测试或调试观测中，provider payload 应再次注入完整 Plan Mode reminder。

**非目标**

- 本场景不要求在 TUI 中暴露原始 prompt。
- 如果模型更早产出足够好的计划，不需要强行让任务跑很久。

## 场景 6：工具错误后的恢复

**输入**

```text
读取 src/internal/prompt/not-exist.go，然后告诉我 prompt runtime 在哪里实现
```

**期望观察点**

- 第一次读取不存在文件可能失败。
- Agent 会阅读工具错误，而不是忽略错误。
- Agent 会调整策略，例如使用 `glob` 或 `grep` 查找真实的 prompt runtime 文件。
- 最终回复会区分失败路径和之后找到的真实文件。

**非目标**

- Agent 不需要修改文件。

## 场景 7：Cache Usage 可见性

**输入**

```text
帮我解释当前 prompt runtime 的结构
```

**期望观察点**

- 当 provider 返回 cache usage 时，Agent usage 事件包含 cache creation/read 字段。
- TUI 状态可以显示类似 `cache read X/create Y` 的简短信息。
- 当 provider 不返回 cache usage 时，UI 不应显示误导性的 `cache read 0/create 0`。

**非目标**

- 本场景本身不能证明账单一定下降。
- cache 行为可能需要相同稳定 prompt 的重复请求，以及 provider 支持。
