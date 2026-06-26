package tools

import (
	"context"
	"fmt"
	"time"

	"onecode/internal/llm"
)

// Registry 工具注册中心。
// 集中登记所有工具，按名查找。
// order 保持注册顺序，保证导出的工具列表顺序稳定。
type Registry struct {
	order []string
	tools map[string]ToolInfo
}

// NewRegistry 创建注册中心
func NewRegistry() *Registry {
	return &Registry{
		order: make([]string, 0),
		tools: make(map[string]ToolInfo),
	}
}

// Register 注册工具，并按内置工具名推断安全分类。
func (r *Registry) Register(t Tool) {
	r.RegisterWithSafety(t, defaultSafety(t.Name()))
}

// RegisterWithSafety 注册工具，并显式指定安全分类。
func (r *Registry) RegisterWithSafety(t Tool, safety Safety) {
	name := t.Name()
	if _, exists := r.tools[name]; exists {
		return // 已注册，跳过
	}
	r.order = append(r.order, name)
	r.tools[name] = ToolInfo{
		Tool:   t,
		Safety: safety,
	}
}

// Get 按名查找工具
func (r *Registry) Get(name string) (Tool, bool) {
	info, ok := r.tools[name]
	if !ok {
		return nil, false
	}
	return info.Tool, true
}

// Safety 返回工具安全分类。
func (r *Registry) Safety(name string) (Safety, bool) {
	info, ok := r.tools[name]
	if !ok {
		return SafetySideEffect, false
	}
	return info.Safety, true
}

// List 按注册顺序返回所有工具
func (r *Registry) List() []Tool {
	result := make([]Tool, 0, len(r.order))
	for _, name := range r.order {
		result = append(result, r.tools[name].Tool)
	}
	return result
}

// ToToolDefinitions 转成 LLM API 工具定义列表，顺序与 List 一致
func (r *Registry) ToToolDefinitions() []llm.ToolDefinition {
	result := make([]llm.ToolDefinition, 0, len(r.order))
	for _, name := range r.order {
		t := r.tools[name].Tool
		result = append(result, llm.ToolDefinition{
			Name:        t.Name(),
			Description: t.Description(),
			Schema:      t.Schema(),
		})
	}
	return result
}

// ToToolDefinitionsBySafety 转成指定安全分类范围内的工具定义列表。
func (r *Registry) ToToolDefinitionsBySafety(allowed map[Safety]bool) []llm.ToolDefinition {
	result := make([]llm.ToolDefinition, 0, len(r.order))
	for _, name := range r.order {
		info := r.tools[name]
		if !allowed[info.Safety] {
			continue
		}
		t := info.Tool
		result = append(result, llm.ToolDefinition{
			Name:        t.Name(),
			Description: t.Description(),
			Schema:      t.Schema(),
		})
	}
	return result
}

// Execute 查找工具并执行。内部：
// 1. 按名查找，找不到返回 Result{IsError: true}
// 2. 用 tool.Timeout() 创建带超时的 ctx
// 3. 调用 tool.Execute，捕获 panic 转为 Result
func (r *Registry) Execute(ctx context.Context, name string, args map[string]interface{}) Result {
	// 查找工具
	info, ok := r.tools[name]
	if !ok {
		return Result{
			Content: fmt.Sprintf("工具不存在: %s", name),
			IsError: true,
		}
	}
	t := info.Tool

	// 创建带超时的 ctx
	timeout := t.Timeout()
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// 执行工具，捕获 panic
	var result Result
	func() {
		defer func() {
			if r := recover(); r != nil {
				result = Result{
					Content: fmt.Sprintf("工具执行 panic: %v", r),
					IsError: true,
				}
			}
		}()
		result = t.Execute(ctx, args)
	}()

	// 检查是否超时
	if ctx.Err() == context.DeadlineExceeded && !result.IsError {
		return Result{
			Content: fmt.Sprintf("工具执行超时 (%v)", timeout),
			IsError: true,
		}
	}

	return result
}

// defaultSafety 为内置工具提供固定安全分类。未知工具保守视为有副作用。
func defaultSafety(name string) Safety {
	switch name {
	case "read_file", "glob", "grep":
		return SafetyReadOnly
	default:
		return SafetySideEffect
	}
}
