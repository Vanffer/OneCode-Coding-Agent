package permission

import (
	"os"
	"path/filepath"
	"testing"
)

func testProjectRoot(t *testing.T) string {
	t.Helper()
	root, err := os.MkdirTemp(".", "permission-test-*")
	if err != nil {
		t.Fatal(err)
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(abs); err != nil {
			t.Fatalf("cleanup test project root: %v", err)
		}
	})
	return abs
}
