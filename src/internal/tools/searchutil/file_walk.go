package searchutil

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

func NormalizeSearchRoot(root string) string {
	if root == "" {
		return "."
	}
	return root
}

func ValidateSearchRoot(root string) error {
	info, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("路径不存在: %s", root)
	}
	if !info.IsDir() {
		return fmt.Errorf("路径不是目录: %s", root)
	}
	return nil
}

func WalkSearchFiles(ctx context.Context, root string, fn func(path, relPath string) error) (int, error) {
	skippedUnreadable := 0

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			skippedUnreadable++
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if d.IsDir() {
			if path != root && shouldSkipSearchDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}

		relPath, err := filepath.Rel(root, path)
		if err != nil {
			skippedUnreadable++
			return nil
		}

		return fn(path, filepath.ToSlash(relPath))
	})

	return skippedUnreadable, err
}

func shouldSkipSearchDir(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", ".idea", ".vscode":
		return true
	default:
		return false
	}
}
