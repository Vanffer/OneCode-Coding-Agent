package mcp

import (
	"context"
)

type Client struct {
	name      string
	transport Transport
	tools     []MCPTool
}

func NewClient(name string, transport Transport) *Client {
	return &Client{name: name, transport: transport}
}

func (c *Client) Start(ctx context.Context) error {
	return c.transport.Start(ctx)
}

func (c *Client) Initialize(ctx context.Context) error {
	params := InitializeParams{
		ProtocolVersion: ProtocolVersion,
		Capabilities: map[string]interface{}{
			"tools": map[string]interface{}{},
		},
		ClientInfo: ClientInfo{
			Name:    "onecode",
			Version: "0.1.0",
		},
	}
	var result InitializeResult
	return c.transport.Request(ctx, "initialize", params, &result)
}

func (c *Client) ListTools(ctx context.Context) ([]MCPTool, error) {
	var result ListToolsResult
	if err := c.transport.Request(ctx, "tools/list", nil, &result); err != nil {
		return nil, err
	}
	c.tools = result.Tools
	return result.Tools, nil
}

func (c *Client) CallTool(ctx context.Context, name string, args map[string]interface{}) (CallToolResult, error) {
	var result CallToolResult
	err := c.transport.Request(ctx, "tools/call", CallToolParams{
		Name:      name,
		Arguments: args,
	}, &result)
	return result, err
}

func (c *Client) Close() error {
	return c.transport.Close()
}
