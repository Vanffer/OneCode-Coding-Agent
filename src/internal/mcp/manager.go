package mcp

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"onecode/internal/config"
	"onecode/internal/tools"
)

type TransportFactory func(name string, cfg config.MCPConfig) (Transport, error)

type Manager struct {
	configs          map[string]config.MCPConfig
	servers          map[string]*ServerSession
	transportFactory TransportFactory
}

type ServerSession struct {
	Name   string
	Config config.MCPConfig
	Client *Client
	Tools  []MCPTool
	Err    error
}

type DiscoverResult struct {
	Sessions []*ServerSession
	Errors   []ServerError
}

type ServerError struct {
	Server string
	Stage  string
	Err    error
}

func NewManager(cfg map[string]config.MCPConfig) *Manager {
	configs := make(map[string]config.MCPConfig, len(cfg))
	for name, server := range cfg {
		configs[name] = server
	}
	return &Manager{
		configs:          configs,
		servers:          map[string]*ServerSession{},
		transportFactory: NewTransport,
	}
}

func (m *Manager) SetTransportFactory(factory TransportFactory) {
	if factory == nil {
		factory = NewTransport
	}
	m.transportFactory = factory
}

func (m *Manager) Discover(ctx context.Context) DiscoverResult {
	result := DiscoverResult{}
	names := make([]string, 0, len(m.configs))
	for name := range m.configs {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		session, serverErr := m.discoverServer(ctx, name, m.configs[name])
		if serverErr.Err != nil {
			result.Errors = append(result.Errors, serverErr)
			continue
		}
		m.servers[name] = session
		result.Sessions = append(result.Sessions, session)
	}
	return result
}

func (m *Manager) RegisterTools(registry *tools.Registry, result *DiscoverResult) {
	if registry == nil || result == nil {
		return
	}
	for _, session := range result.Sessions {
		for _, remote := range session.Tools {
			name, err := RemoteToolName(session.Name, remote.Name)
			if err != nil {
				result.Errors = append(result.Errors, ServerError{Server: session.Name, Stage: StageRegister, Err: err})
				continue
			}
			if registry.Has(name) {
				result.Errors = append(result.Errors, ServerError{
					Server: session.Name,
					Stage:  StageRegister,
					Err:    fmt.Errorf("MCP 工具名冲突: %s", name),
				})
				continue
			}
			tool, err := NewRemoteTool(session.Name, remote, session.Client)
			if err != nil {
				result.Errors = append(result.Errors, ServerError{Server: session.Name, Stage: StageRegister, Err: err})
				continue
			}
			registry.RegisterWithSafetyAndCategory(tool, SafetyForMCPTool(session.Config, remote.Name), tools.CategoryMCP)
		}
	}
}

func (m *Manager) Close() error {
	var errs []error
	for _, session := range m.servers {
		if session.Client == nil {
			continue
		}
		if err := session.Client.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (m *Manager) discoverServer(ctx context.Context, name string, cfg config.MCPConfig) (*ServerSession, ServerError) {
	transport, err := m.transportFactory(name, cfg)
	if err != nil {
		return nil, ServerError{Server: name, Stage: StageConfig, Err: err}
	}
	client := NewClient(name, transport)
	session := &ServerSession{Name: name, Config: cfg, Client: client}

	if err := client.Start(ctx); err != nil {
		session.Err = err
		_ = client.Close()
		return nil, ServerError{Server: name, Stage: StageStart, Err: err}
	}
	if err := client.Initialize(ctx); err != nil {
		session.Err = err
		_ = client.Close()
		return nil, ServerError{Server: name, Stage: StageInitialize, Err: err}
	}
	remoteTools, err := client.ListTools(ctx)
	if err != nil {
		session.Err = err
		_ = client.Close()
		return nil, ServerError{Server: name, Stage: StageListTools, Err: err}
	}
	session.Tools = remoteTools
	return session, ServerError{}
}
