package conversation

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestWindowResolverLocalOverridesProvider(t *testing.T) {
	root := t.TempDir()
	store := NewProjectStore(root)
	if err := store.SaveLocalConfig(context.Background(), LocalConfig{ContextWindow: 200000}); err != nil {
		t.Fatalf("SaveLocalConfig returned error: %v", err)
	}

	got, err := (WindowResolver{Store: store}).Resolve(context.Background(), ContextOptions{
		ProjectRoot:    root,
		ProviderName:   "OpenAI",
		ModelName:      "gpt-4o",
		ProviderWindow: 64000,
	})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if got.Limit != 200000 || got.Source != WindowSourceLocal {
		t.Fatalf("expected local window, got %+v", got)
	}
}

func TestWindowResolverProviderOverridesInferred(t *testing.T) {
	got, err := (WindowResolver{}).Resolve(context.Background(), ContextOptions{
		ProjectRoot:    t.TempDir(),
		ProviderName:   "Claude",
		ModelName:      "claude-3-5-sonnet",
		ProviderWindow: 180000,
	})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if got.Limit != 180000 || got.Source != WindowSourceProvider {
		t.Fatalf("expected provider window, got %+v", got)
	}
}

func TestWindowResolverUsesDefaultForUnmarkedClaude(t *testing.T) {
	got, err := (WindowResolver{}).Resolve(context.Background(), ContextOptions{
		ProjectRoot:  t.TempDir(),
		ProviderName: "Anthropic",
		ModelName:    "claude-3-7-sonnet-latest",
	})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if got.Limit != defaultContextWindow || got.Source != WindowSourceDefault {
		t.Fatalf("expected default claude window, got %+v", got)
	}
}

func TestWindowResolverUsesDefaultForUnmarkedOpenAI(t *testing.T) {
	got, err := (WindowResolver{}).Resolve(context.Background(), ContextOptions{
		ProjectRoot:  t.TempDir(),
		ProviderName: "OpenAI",
		ModelName:    "gpt-4o",
	})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if got.Limit != defaultContextWindow || got.Source != WindowSourceDefault {
		t.Fatalf("expected default OpenAI window, got %+v", got)
	}
}

func TestWindowResolverUsesBracketSuffix(t *testing.T) {
	got, err := (WindowResolver{}).Resolve(context.Background(), ContextOptions{
		ProjectRoot:  t.TempDir(),
		ProviderName: "DeepSeek",
		ModelName:    "deepseek-chat[1M]",
	})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if got.Limit != 1000000 || got.Source != WindowSourceInferred {
		t.Fatalf("expected bracket suffix window, got %+v", got)
	}
}

func TestInferWindowFromBracketSuffix(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		model    string
		want     int
	}{
		{
			name:     "one million marker",
			provider: "openai",
			model:    "gpt-5.6-sol[1M]",
			want:     1000000,
		},
		{
			name:     "kilotoken marker",
			provider: "anthropic",
			model:    "claude-sonnet-5 [512K]",
			want:     512000,
		},
		{
			name:     "lowercase marker",
			provider: "deepseek",
			model:    "deepseek-chat[256k]",
			want:     256000,
		},
		{
			name:     "plain token count marker",
			provider: "zhipu",
			model:    "glm-5.2[200000]",
			want:     200000,
		},
		{
			name:     "provider marker fallback",
			provider: "custom-provider[128K]",
			model:    "corp-router-coder",
			want:     128000,
		},
		{
			name:     "model marker wins over provider marker",
			provider: "custom-provider[128K]",
			model:    "corp-router-coder[1M]",
			want:     1000000,
		},
		{
			name:     "unbracketed marker ignored",
			provider: "custom",
			model:    "corp-router-coder-128k",
			want:     0,
		},
		{
			name:     "invalid marker ignored",
			provider: "custom",
			model:    "corp-router-coder[large]",
			want:     0,
		},
		{
			name:     "non suffix marker ignored",
			provider: "custom",
			model:    "corp-router-coder[1M]-preview",
			want:     0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := InferWindow(tt.provider, tt.model)
			if tt.want == 0 {
				if ok {
					t.Fatalf("expected no inferred window, got %+v", got)
				}
				return
			}
			if !ok {
				t.Fatal("expected inferred window")
			}
			if got.Limit != tt.want || got.Source != WindowSourceInferred {
				t.Fatalf("expected inferred window %d, got %+v", tt.want, got)
			}
		})
	}
}

func TestWindowResolverDefault(t *testing.T) {
	got, err := (WindowResolver{}).Resolve(context.Background(), ContextOptions{
		ProjectRoot:  t.TempDir(),
		ProviderName: "Custom",
		ModelName:    "unknown-local-model",
	})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if got.Limit != defaultContextWindow || got.Source != WindowSourceDefault {
		t.Fatalf("expected default window, got %+v", got)
	}
}

func TestWindowResolverReturnsLocalConfigError(t *testing.T) {
	root := t.TempDir()
	contextDir := filepath.Join(root, contextDirName)
	if err := os.MkdirAll(contextDir, 0755); err != nil {
		t.Fatalf("failed to create context dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(contextDir, localConfigFileName), []byte("context_window: -1\n"), 0644); err != nil {
		t.Fatalf("failed to write local config: %v", err)
	}

	_, err := (WindowResolver{Store: NewProjectStore(root)}).Resolve(context.Background(), ContextOptions{
		ProjectRoot: root,
	})
	if err == nil {
		t.Fatal("expected local config validation error")
	}
}
