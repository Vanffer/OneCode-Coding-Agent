package mcp

import (
	"context"
	"fmt"

	"onecode/internal/config"
)

type Transport interface {
	Start(ctx context.Context) error
	Request(ctx context.Context, method string, params interface{}, result interface{}) error
	Close() error
}

func NewTransport(name string, cfg config.MCPConfig) (Transport, error) {
	switch cfg.Type {
	case "stdio":
		return NewStdioTransport(name, cfg.Command, cfg.Args, cfg.Env), nil
	case "http":
		return NewHTTPTransport(name, cfg.URL, cfg.Headers, nil), nil
	default:
		return nil, fmt.Errorf("MCP server %s type 无效: %s", name, cfg.Type)
	}
}
