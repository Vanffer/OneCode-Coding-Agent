package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadValidConfig(t *testing.T) {
	content := `
providers:
  - name: Claude
    protocol: anthropic
    api_key: test-key
    model: claude-3-sonnet
  - name: GPT
    protocol: openai
    api_key: test-key
    model: gpt-4
`
	path := writeTempFile(t, content)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(cfg.Providers) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(cfg.Providers))
	}
	if cfg.Providers[0].Name != "Claude" {
		t.Errorf("expected first provider name 'Claude', got '%s'", cfg.Providers[0].Name)
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load("/nonexistent/config.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "无法读取配置文件") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	content := `invalid yaml: [[[`
	path := writeTempFile(t, content)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
	if !strings.Contains(err.Error(), "配置文件格式错误") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestLoadEmptyProviders(t *testing.T) {
	content := `providers: []`
	path := writeTempFile(t, content)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for empty providers")
	}
	if !strings.Contains(err.Error(), "providers 列表为空") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestLoadMissingFields(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr string
	}{
		{
			name: "missing name",
			content: `
providers:
  - protocol: anthropic
    api_key: test
    model: test
`,
			wantErr: "name 为空",
		},
		{
			name: "missing protocol",
			content: `
providers:
  - name: Test
    api_key: test
    model: test
`,
			wantErr: "protocol 为空",
		},
		{
			name: "invalid protocol",
			content: `
providers:
  - name: Test
    protocol: invalid
    api_key: test
    model: test
`,
			wantErr: "protocol 无效",
		},
		{
			name: "missing api_key",
			content: `
providers:
  - name: Test
    protocol: anthropic
    model: test
`,
			wantErr: "api_key 为空",
		},
		{
			name: "missing model",
			content: `
providers:
  - name: Test
    protocol: anthropic
    api_key: test
`,
			wantErr: "model 为空",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTempFile(t, tt.content)
			_, err := Load(path)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("expected error containing '%s', got '%v'", tt.wantErr, err)
			}
		})
	}
}

func TestLoadProviderContextWindow(t *testing.T) {
	path := writeTempFile(t, `
providers:
  - name: Claude
    protocol: anthropic
    api_key: test-key
    model: claude-3-sonnet
    context_window: 200000
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if got := cfg.Providers[0].ContextWindow; got != 200000 {
		t.Fatalf("expected context window 200000, got %d", got)
	}
}

func TestLoadProviderContextWindowOptional(t *testing.T) {
	path := writeTempFile(t, validProviderConfig())

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if got := cfg.Providers[0].ContextWindow; got != 0 {
		t.Fatalf("expected unset context window to be 0, got %d", got)
	}
}

func TestLoadProviderContextWindowInvalid(t *testing.T) {
	path := writeTempFile(t, `
providers:
  - name: Claude
    protocol: anthropic
    api_key: test-key
    model: claude-3-sonnet
    context_window: -1
`)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected invalid context_window error")
	}
	if !strings.Contains(err.Error(), "context_window") {
		t.Fatalf("expected context_window in error, got %v", err)
	}
}

func TestLoadMCPConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr string
	}{
		{
			name: "invalid type",
			content: validProviderConfig() + `
mcp_servers:
  bad:
    type: websocket
`,
			wantErr: "type 无效",
		},
		{
			name: "stdio missing command",
			content: validProviderConfig() + `
mcp_servers:
  local:
    type: stdio
`,
			wantErr: "command 为空",
		},
		{
			name: "http missing url",
			content: validProviderConfig() + `
mcp_servers:
  remote:
    type: http
`,
			wantErr: "url 为空",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTempFile(t, tt.content)
			_, err := Load(path)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestLoadMerged(t *testing.T) {
	dir := t.TempDir()
	userPath := filepath.Join(dir, "user.yaml")
	projectPath := filepath.Join(dir, "project.yaml")
	writeFile(t, userPath, `
mcp_servers:
  docs:
    type: http
    url: https://user.example/mcp
`)
	writeFile(t, projectPath, validProviderConfig()+`
mcp_servers:
  repo:
    type: stdio
    command: onecode-mcp
`)

	cfg, err := LoadMerged(userPath, projectPath)
	if err != nil {
		t.Fatalf("LoadMerged returned error: %v", err)
	}
	if len(cfg.Providers) != 1 {
		t.Fatalf("expected project providers, got %d", len(cfg.Providers))
	}
	if _, ok := cfg.MCPServers["docs"]; !ok {
		t.Fatal("expected user MCP server to be merged")
	}
	if _, ok := cfg.MCPServers["repo"]; !ok {
		t.Fatal("expected project MCP server to be merged")
	}
}

func TestLoadMergedMissingUserConfig(t *testing.T) {
	dir := t.TempDir()
	projectPath := filepath.Join(dir, "project.yaml")
	writeFile(t, projectPath, validProviderConfig())

	cfg, err := LoadMerged(filepath.Join(dir, "missing.yaml"), projectPath)
	if err != nil {
		t.Fatalf("LoadMerged returned error: %v", err)
	}
	if len(cfg.Providers) != 1 {
		t.Fatalf("expected project providers, got %d", len(cfg.Providers))
	}
}

func TestLoadMergedBadUserConfig(t *testing.T) {
	dir := t.TempDir()
	userPath := filepath.Join(dir, "user.yaml")
	projectPath := filepath.Join(dir, "project.yaml")
	writeFile(t, userPath, `bad: [[[`)
	writeFile(t, projectPath, validProviderConfig())

	_, err := LoadMerged(userPath, projectPath)
	if err == nil {
		t.Fatal("expected bad user config error")
	}
	if !strings.Contains(err.Error(), "配置文件格式错误") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadMergedMCPServerOverride(t *testing.T) {
	dir := t.TempDir()
	userPath := filepath.Join(dir, "user.yaml")
	projectPath := filepath.Join(dir, "project.yaml")
	writeFile(t, userPath, `
mcp_servers:
  github:
    type: http
    url: https://user.example/mcp
`)
	writeFile(t, projectPath, validProviderConfig()+`
mcp_servers:
  github:
    type: stdio
    command: project-github-mcp
`)

	cfg, err := LoadMerged(userPath, projectPath)
	if err != nil {
		t.Fatalf("LoadMerged returned error: %v", err)
	}
	got := cfg.MCPServers["github"]
	if got.Type != "stdio" || got.Command != "project-github-mcp" || got.URL != "" {
		t.Fatalf("expected project server to override user server, got %+v", got)
	}
}

func TestLoadMergedMCPServerUnion(t *testing.T) {
	dir := t.TempDir()
	userPath := filepath.Join(dir, "user.yaml")
	projectPath := filepath.Join(dir, "project.yaml")
	writeFile(t, userPath, `
mcp_servers:
  user_docs:
    type: http
    url: https://docs.example/mcp
`)
	writeFile(t, projectPath, validProviderConfig()+`
mcp_servers:
  project_repo:
    type: stdio
    command: repo-mcp
`)

	cfg, err := LoadMerged(userPath, projectPath)
	if err != nil {
		t.Fatalf("LoadMerged returned error: %v", err)
	}
	if len(cfg.MCPServers) != 2 {
		t.Fatalf("expected 2 MCP servers, got %d: %+v", len(cfg.MCPServers), cfg.MCPServers)
	}
}

func TestLoadMergedProvidersFromProject(t *testing.T) {
	dir := t.TempDir()
	userPath := filepath.Join(dir, "user.yaml")
	projectPath := filepath.Join(dir, "project.yaml")
	writeFile(t, userPath, `
providers:
  - name: User
    protocol: openai
    api_key: user-key
    model: user-model
`)
	writeFile(t, projectPath, `
providers:
  - name: Project
    protocol: anthropic
    api_key: project-key
    model: project-model
`)

	cfg, err := LoadMerged(userPath, projectPath)
	if err != nil {
		t.Fatalf("LoadMerged returned error: %v", err)
	}
	if len(cfg.Providers) != 1 || cfg.Providers[0].Name != "Project" {
		t.Fatalf("expected project providers only, got %+v", cfg.Providers)
	}
}

func TestMemoryEnabledDefaultsToTrue(t *testing.T) {
	cfg, err := Load(writeTempFile(t, validProviderConfig()))
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if !cfg.Memory.Enabled {
		t.Fatal("expected memory to be enabled by default")
	}
}

func TestLoadMemoryDisabled(t *testing.T) {
	cfg, err := Load(writeTempFile(t, validProviderConfig()+`
memory:
  enabled: false
`))
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Memory.Enabled {
		t.Fatal("expected memory to be disabled")
	}
}

func TestLoadMergedMemoryPrecedence(t *testing.T) {
	tests := []struct {
		name       string
		userMemory string
		project    string
		want       bool
	}{
		{name: "default", want: true},
		{name: "user disables", userMemory: "memory:\n  enabled: false\n", want: false},
		{name: "project overrides user", userMemory: "memory:\n  enabled: false\n", project: "memory:\n  enabled: true\n", want: true},
		{name: "project disables", userMemory: "memory:\n  enabled: true\n", project: "memory:\n  enabled: false\n", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			userPath := filepath.Join(dir, "user.yaml")
			projectPath := filepath.Join(dir, "project.yaml")
			writeFile(t, userPath, tt.userMemory)
			writeFile(t, projectPath, validProviderConfig()+tt.project)

			cfg, err := LoadMerged(userPath, projectPath)
			if err != nil {
				t.Fatalf("LoadMerged returned error: %v", err)
			}
			if cfg.Memory.Enabled != tt.want {
				t.Fatalf("expected memory enabled=%t, got %t", tt.want, cfg.Memory.Enabled)
			}
		})
	}
}

func TestExpandMCPEnvAndHeaders(t *testing.T) {
	t.Setenv("ONECODE_MCP_TOKEN", "secret-token")
	t.Setenv("ONECODE_MCP_PATH", "C:/mcp")
	path := writeTempFile(t, validProviderConfig()+`
mcp_servers:
  local:
    type: stdio
    command: local-mcp
    env:
      MCP_PATH: "${ONECODE_MCP_PATH}/bin"
  remote:
    type: http
    url: https://remote.example/mcp
    headers:
      Authorization: "Bearer ${ONECODE_MCP_TOKEN}"
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if got := cfg.MCPServers["local"].Env["MCP_PATH"]; got != "C:/mcp/bin" {
		t.Fatalf("expected env expansion, got %q", got)
	}
	if got := cfg.MCPServers["remote"].Headers["Authorization"]; got != "Bearer secret-token" {
		t.Fatalf("expected header expansion, got %q", got)
	}
}

func TestExpandMCPEnvMissingVar(t *testing.T) {
	path := writeTempFile(t, validProviderConfig()+`
mcp_servers:
  remote:
    type: http
    url: https://remote.example/mcp
    headers:
      Authorization: "Bearer ${ONECODE_MISSING_TOKEN}"
`)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected missing environment variable error")
	}
	if !strings.Contains(err.Error(), "ONECODE_MISSING_TOKEN") {
		t.Fatalf("expected missing variable name in error, got %v", err)
	}
	if !strings.Contains(err.Error(), "mcp_servers.remote.headers.Authorization") {
		t.Fatalf("expected field path in error, got %v", err)
	}
}

func TestExpandMCPEnvDoesNotLeakSecret(t *testing.T) {
	path := writeTempFile(t, validProviderConfig()+`
mcp_servers:
  remote:
    type: http
    url: https://remote.example/mcp
    headers:
      Authorization: "Bearer real-secret-token-${ONECODE_MISSING_TOKEN}"
`)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected missing environment variable error")
	}
	if strings.Contains(err.Error(), "real-secret-token") || strings.Contains(err.Error(), "Bearer") {
		t.Fatalf("error leaked header value: %v", err)
	}
}

func writeTempFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	writeFile(t, path, content)
	return path
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}
}

func validProviderConfig() string {
	return `
providers:
  - name: Claude
    protocol: anthropic
    api_key: test-key
    model: claude-3-sonnet
`
}
