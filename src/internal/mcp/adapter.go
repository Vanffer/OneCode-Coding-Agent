package mcp

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	"onecode/internal/config"
	"onecode/internal/tools"
)

const remoteToolTimeout = 30 * time.Second

type ToolCaller interface {
	CallTool(ctx context.Context, name string, args map[string]interface{}) (CallToolResult, error)
}

type RemoteTool struct {
	name        string
	serverName  string
	remoteName  string
	description string
	schema      map[string]interface{}
	caller      ToolCaller
	timeout     time.Duration
}

func NewRemoteTool(serverName string, tool MCPTool, caller ToolCaller) (*RemoteTool, error) {
	name, err := RemoteToolName(serverName, tool.Name)
	if err != nil {
		return nil, err
	}
	schema, err := normalizeInputSchema(tool.InputSchema)
	if err != nil {
		return nil, fmt.Errorf("MCP tool %s schema 无效: %w", name, err)
	}
	description := strings.TrimSpace(tool.Description)
	if description == "" {
		description = fmt.Sprintf("MCP tool %s from server %s", tool.Name, serverName)
	}
	return &RemoteTool{
		name:        name,
		serverName:  serverName,
		remoteName:  tool.Name,
		description: description,
		schema:      schema,
		caller:      caller,
		timeout:     remoteToolTimeout,
	}, nil
}

func RemoteToolName(serverName, toolName string) (string, error) {
	server := sanitizeToolPart(serverName)
	tool := sanitizeToolPart(toolName)
	if server == "" || tool == "" {
		return "", fmt.Errorf("MCP 工具名无效: server=%q tool=%q", serverName, toolName)
	}
	return server + "." + tool, nil
}

func (t *RemoteTool) Name() string {
	return t.name
}

func (t *RemoteTool) Description() string {
	return t.description
}

func (t *RemoteTool) Schema() map[string]interface{} {
	return t.schema
}

func (t *RemoteTool) Timeout() time.Duration {
	return t.timeout
}

func (t *RemoteTool) Execute(ctx context.Context, args map[string]interface{}) tools.Result {
	result, err := t.caller.CallTool(ctx, t.remoteName, args)
	if err != nil {
		return tools.Result{Content: "MCP 工具调用失败: " + err.Error(), IsError: true}
	}
	content := convertMCPContent(result.Content)
	if content == "" && result.IsError {
		content = "MCP 工具返回错误"
	}
	return tools.Result{Content: content, IsError: result.IsError}
}

func SafetyForMCPTool(serverCfg config.MCPConfig, remoteToolName string) tools.Safety {
	readOnly := serverCfg.ReadOnly
	if toolCfg, ok := serverCfg.Tools[remoteToolName]; ok && toolCfg.ReadOnly != nil {
		readOnly = *toolCfg.ReadOnly
	}
	if readOnly {
		return tools.SafetyReadOnly
	}
	return tools.SafetySideEffect
}

func sanitizeToolPart(value string) string {
	value = strings.TrimSpace(value)
	var b strings.Builder
	for _, r := range value {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			b.WriteRune(r)
		case r == '_' || r == '-' || r == '.':
			b.WriteRune(r)
		case unicode.IsSpace(r):
			b.WriteRune('_')
		default:
			b.WriteRune('_')
		}
	}
	return strings.Trim(b.String(), "_.-")
}

func normalizeInputSchema(schema map[string]interface{}) (map[string]interface{}, error) {
	if len(schema) == 0 {
		return map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		}, nil
	}
	out := make(map[string]interface{}, len(schema)+1)
	for key, value := range schema {
		out[key] = value
	}
	if schemaType, ok := out["type"]; ok {
		if schemaType != "object" {
			return nil, fmt.Errorf("schema type must be object, got %v", schemaType)
		}
	} else {
		out["type"] = "object"
	}
	return out, nil
}

func convertMCPContent(contents []MCPContent) string {
	parts := make([]string, 0, len(contents))
	for _, content := range contents {
		if content.Type == "text" {
			parts = append(parts, content.Text)
			continue
		}
		if content.Type == "" {
			parts = append(parts, "[MCP 非文本内容]")
			continue
		}
		parts = append(parts, fmt.Sprintf("[MCP 非文本内容: %s]", content.Type))
	}
	return strings.Join(parts, "\n")
}
