package tools

import (
	"context"
	"time"
)

// Result 工具执行结果。
// 成功时 Content 是输出文本，失败时 Content 包含完整错误详情。
// IsError=true 时模型会看到错误并决定是否重试。
type Result struct {
	Content string // 成功：工具输出；失败：错误详情（含上下文）
	IsError bool   // true=失败，false=成功
}

// Tool 工具接口。每个核心工具实现它。
type Tool interface {
	// Name 返回工具名称，如 "read_file"、"bash"
	Name() string
	// Description 返回工具描述，告诉模型何时该用、怎么用
	Description() string
	// Schema 返回参数的 JSON Schema，模型据此构造合法参数
	Schema() map[string]interface{}
	// Timeout 返回该工具的超时时间（bash=5min，其他=30s）
	Timeout() time.Duration
	// Execute 执行工具，ctx 由 Registry.Execute 注入超时。
	// args 是 JSON 参数，内部 Unmarshal 到私有 struct；
	// 解析失败应返回 Result{IsError: true}，不要 panic。
	Execute(ctx context.Context, args map[string]interface{}) Result
}
