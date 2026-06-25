package searchutil

import (
	"path/filepath"
	"strings"
)

func ValidateGlobPattern(pattern string) error {
	pattern = normalizeGlobPath(pattern)
	if pattern == "" {
		return nil
	}
	if !strings.Contains(pattern, "/") {
		_, err := filepath.Match(pattern, "")
		return err
	}
	for _, part := range splitGlobPath(pattern) {
		if part == "**" {
			continue
		}
		if _, err := filepath.Match(part, ""); err != nil {
			return err
		}
	}
	return nil
}

// MatchPattern 用相对路径匹配 glob，支持 ** 表示任意层级目录。
func MatchPattern(pattern, relPath string) (bool, error) {
	pattern = normalizeGlobPath(pattern)
	relPath = normalizeGlobPath(relPath)

	if pattern == "" {
		return false, nil
	}

	if !strings.Contains(pattern, "/") {
		return filepath.Match(pattern, filepath.Base(relPath))
	}

	patternParts := splitGlobPath(pattern)
	pathParts := splitGlobPath(relPath)
	return matchGlobParts(patternParts, pathParts)
}

func normalizeGlobPath(path string) string {
	path = filepath.ToSlash(strings.TrimSpace(path))
	path = strings.TrimPrefix(path, "./")
	path = strings.TrimPrefix(path, "/")
	return path
}

func splitGlobPath(path string) []string {
	if path == "" {
		return nil
	}
	parts := strings.Split(path, "/")
	result := parts[:0]
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		result = append(result, part)
	}
	return result
}

func matchGlobParts(patternParts, pathParts []string) (bool, error) {
	var walk func(patternIdx, pathIdx int) (bool, error)
	walk = func(patternIdx, pathIdx int) (bool, error) {
		if patternIdx == len(patternParts) {
			return pathIdx == len(pathParts), nil
		}

		patternPart := patternParts[patternIdx]
		if patternPart == "**" {
			if patternIdx == len(patternParts)-1 {
				return true, nil
			}
			for nextPathIdx := pathIdx; nextPathIdx <= len(pathParts); nextPathIdx++ {
				matched, err := walk(patternIdx+1, nextPathIdx)
				if err != nil || matched {
					return matched, err
				}
			}
			return false, nil
		}

		if pathIdx == len(pathParts) {
			return false, nil
		}

		matched, err := filepath.Match(patternPart, pathParts[pathIdx])
		if err != nil || !matched {
			return matched, err
		}
		return walk(patternIdx+1, pathIdx+1)
	}

	return walk(0, 0)
}
