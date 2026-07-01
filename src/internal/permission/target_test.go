package permission

import "testing"

func TestExtractTarget(t *testing.T) {
	tests := []struct {
		name string
		req  Request
		want Target
	}{
		{
			name: "bash",
			req:  Request{Tool: "bash", Args: map[string]interface{}{"command": "git status"}},
			want: Target{Kind: TargetCommand, Value: "git status", Command: "git status"},
		},
		{
			name: "read file",
			req:  Request{Tool: "read_file", Args: map[string]interface{}{"path": "src/main.go"}},
			want: Target{Kind: TargetPath, Value: "src/main.go", Path: "src/main.go"},
		},
		{
			name: "glob default root",
			req:  Request{Tool: "glob", Args: map[string]interface{}{"pattern": "**/*.go"}},
			want: Target{Kind: TargetSearch, Value: ".", SearchRoot: ".", Glob: "**/*.go"},
		},
		{
			name: "grep with glob",
			req:  Request{Tool: "grep", Args: map[string]interface{}{"path": "src", "glob": "**/*.go"}},
			want: Target{Kind: TargetSearch, Value: "src", SearchRoot: "src", Glob: "**/*.go"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractTarget(tt.req)
			if got.Kind != tt.want.Kind || got.Value != tt.want.Value || got.Command != tt.want.Command ||
				got.Path != tt.want.Path || got.SearchRoot != tt.want.SearchRoot || got.Glob != tt.want.Glob {
				t.Fatalf("expected %+v, got %+v", tt.want, got)
			}
		})
	}
}
