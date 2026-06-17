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

func writeTempFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}
	return path
}
