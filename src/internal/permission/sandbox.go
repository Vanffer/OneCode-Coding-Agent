package permission

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Sandbox restricts explicit file targets to a single project root.
type Sandbox struct {
	Root string
}

// PathCheckOptions controls how missing paths are handled.
type PathCheckOptions struct {
	AllowMissingLeaf bool
}

func NewSandbox(root string) (Sandbox, error) {
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return Sandbox{}, fmt.Errorf("解析项目根失败: %w", err)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return Sandbox{}, fmt.Errorf("解析项目根符号链接失败: %w", err)
	}
	return Sandbox{Root: filepath.Clean(real)}, nil
}

// CheckPath returns the resolved absolute path if it remains inside the root.
func (s Sandbox) CheckPath(path string, opts PathCheckOptions) (string, error) {
	if strings.TrimSpace(path) == "" {
		path = "."
	}
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(s.Root, path)
	}
	abs = filepath.Clean(abs)

	resolved, err := resolvePathForSandbox(abs, opts.AllowMissingLeaf)
	if err != nil {
		return "", err
	}
	if !pathWithinRoot(s.Root, resolved) {
		return "", fmt.Errorf("路径超出项目沙箱: %s", path)
	}
	return resolved, nil
}

func resolvePathForSandbox(path string, allowMissingLeaf bool) (string, error) {
	if _, err := os.Lstat(path); err == nil {
		real, err := filepath.EvalSymlinks(path)
		if err != nil {
			return "", fmt.Errorf("解析路径符号链接失败: %w", err)
		}
		return filepath.Clean(real), nil
	}
	if !allowMissingLeaf {
		return "", fmt.Errorf("路径不存在: %s", path)
	}

	missingParts := []string{}
	current := filepath.Clean(path)
	for {
		if _, err := os.Lstat(current); err == nil {
			realParent, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", fmt.Errorf("解析父目录符号链接失败: %w", err)
			}
			for i := len(missingParts) - 1; i >= 0; i-- {
				realParent = filepath.Join(realParent, missingParts[i])
			}
			return filepath.Clean(realParent), nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("找不到已存在的父目录: %s", path)
		}
		missingParts = append(missingParts, filepath.Base(current))
		current = parent
	}
}

func pathWithinRoot(root, path string) bool {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" {
		root = strings.ToLower(root)
		path = strings.ToLower(path)
	}
	if path == root {
		return true
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
