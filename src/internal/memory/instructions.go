package memory

import (
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const maxInstructionIncludeDepth = 5

// LoadWarning describes a non-fatal instruction loading problem.
type LoadWarning struct {
	Path    string
	Line    int
	Message string
}

// InstructionSet is the ordered prompt content loaded for one session.
type InstructionSet struct {
	Content  string
	Sources  []string
	Warnings []LoadWarning
}

// InstructionLoader loads project-local, project-root, and user instructions.
type InstructionLoader struct {
	ProjectRoot string
	UserRoot    string
}

// Load reads the configured instruction entrypoints. Missing entrypoints are
// normal; invalid includes are returned as warnings while valid content remains.
func (l InstructionLoader) Load() InstructionSet {
	state := instructionLoadState{visited: make(map[string]struct{})}
	entries := []instructionEntry{
		{path: filepath.Join(l.ProjectRoot, ".onecode", "ONECODE.md"), allowedRoot: l.ProjectRoot},
		{path: filepath.Join(l.ProjectRoot, "ONECODE.md"), allowedRoot: l.ProjectRoot},
	}
	if strings.TrimSpace(l.UserRoot) != "" {
		entries = append(entries, instructionEntry{
			path:        filepath.Join(l.UserRoot, "ONECODE.md"),
			allowedRoot: l.UserRoot,
		})
	}

	var sections []string
	for _, entry := range entries {
		if _, err := os.Stat(entry.path); err != nil {
			if !os.IsNotExist(err) {
				state.warn(entry.path, 0, fmt.Sprintf("无法访问指令文件: %v", err))
			}
			continue
		}
		content, source, ok := state.loadFile(entry.path, entry.allowedRoot, 0)
		if !ok {
			continue
		}
		sections = append(sections, fmt.Sprintf(
			"<project-instructions source=\"%s\">\n%s\n</project-instructions>",
			html.EscapeString(filepath.ToSlash(source)),
			strings.TrimSpace(content),
		))
	}

	return InstructionSet{
		Content:  strings.Join(sections, "\n\n"),
		Sources:  state.sources,
		Warnings: state.warnings,
	}
}

type instructionEntry struct {
	path        string
	allowedRoot string
}

type instructionLoadState struct {
	visited  map[string]struct{}
	sources  []string
	warnings []LoadWarning
}

func (s *instructionLoadState) loadFile(path, allowedRoot string, depth int) (string, string, bool) {
	if depth > maxInstructionIncludeDepth {
		s.warn(path, 0, fmt.Sprintf("@include 超过最大深度 %d", maxInstructionIncludeDepth))
		return "", "", false
	}
	realPath, err := instructionPathWithinRoot(path, allowedRoot)
	if err != nil {
		s.warn(path, 0, err.Error())
		return "", "", false
	}
	key := filepath.Clean(realPath)
	if _, exists := s.visited[key]; exists {
		s.warn(path, 0, "跳过重复或循环引用")
		return "", realPath, false
	}
	s.visited[key] = struct{}{}

	data, err := os.ReadFile(realPath)
	if err != nil {
		s.warn(realPath, 0, fmt.Sprintf("无法读取指令文件: %v", err))
		return "", realPath, false
	}
	s.sources = append(s.sources, realPath)

	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	var out strings.Builder
	for i, line := range lines {
		includePath, isInclude, parseErr := parseIncludeDirective(line)
		switch {
		case parseErr != nil:
			s.warn(realPath, i+1, parseErr.Error())
		case isInclude:
			target := includePath
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(realPath), target)
			}
			warningStart := len(s.warnings)
			included, _, ok := s.loadFile(target, allowedRoot, depth+1)
			if ok {
				out.WriteString(included)
			} else if len(s.warnings) > warningStart {
				childWarning := s.warnings[warningStart]
				s.warnings = s.warnings[:warningStart]
				s.warn(realPath, i+1, fmt.Sprintf("@include %s: %s", includePath, childWarning.Message))
			}
		default:
			out.WriteString(line)
		}
		if i < len(lines)-1 {
			out.WriteByte('\n')
		}
	}
	return out.String(), realPath, true
}

func parseIncludeDirective(line string) (string, bool, error) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "@include" {
		return "", true, fmt.Errorf("@include 缺少路径")
	}
	if !strings.HasPrefix(trimmed, "@include ") && !strings.HasPrefix(trimmed, "@include\t") {
		return "", false, nil
	}
	value := strings.TrimSpace(strings.TrimPrefix(trimmed, "@include"))
	if value == "" {
		return "", true, fmt.Errorf("@include 缺少路径")
	}
	if strings.HasPrefix(value, "\"") {
		unquoted, err := strconv.Unquote(value)
		if err != nil {
			return "", true, fmt.Errorf("@include 引号路径无效: %v", err)
		}
		if strings.TrimSpace(unquoted) == "" {
			return "", true, fmt.Errorf("@include 缺少路径")
		}
		return unquoted, true, nil
	}
	return value, true, nil
}

func instructionPathWithinRoot(path, root string) (string, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("无法解析允许目录: %w", err)
	}
	rootReal, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return "", fmt.Errorf("无法解析允许目录符号链接: %w", err)
	}
	targetAbs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("无法解析引用路径: %w", err)
	}
	targetReal, err := filepath.EvalSymlinks(targetAbs)
	if err != nil {
		return "", fmt.Errorf("无法解析引用文件: %w", err)
	}
	rel, err := filepath.Rel(rootReal, targetReal)
	if err != nil {
		return "", fmt.Errorf("无法比较引用路径: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("引用路径超出允许目录: %s", path)
	}
	return targetReal, nil
}

func (s *instructionLoadState) warn(path string, line int, message string) {
	s.warnings = append(s.warnings, LoadWarning{Path: path, Line: line, Message: message})
}
