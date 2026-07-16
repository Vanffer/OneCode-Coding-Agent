package llm

import "testing"

func TestRequestModelNameStripsContextWindowSuffix(t *testing.T) {
	tests := []struct {
		name  string
		model string
		want  string
	}{
		{
			name:  "million suffix",
			model: "deepseek-chat[1M]",
			want:  "deepseek-chat",
		},
		{
			name:  "kilotoken suffix with space",
			model: "qwen3.7-max [512K]",
			want:  "qwen3.7-max",
		},
		{
			name:  "plain token suffix",
			model: "glm-5.2[200000]",
			want:  "glm-5.2",
		},
		{
			name:  "invalid suffix preserved",
			model: "model[preview]",
			want:  "model[preview]",
		},
		{
			name:  "non suffix marker preserved",
			model: "model[1M]-preview",
			want:  "model[1M]-preview",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := requestModelName(tt.model); got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}
