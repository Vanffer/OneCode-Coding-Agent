package conversation

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"onecode/internal/llm"

	"gopkg.in/yaml.v3"
)

const (
	contextDirName      = ".onecode/context"
	toolResultsDirName  = "tool-results"
	localConfigFileName = "local.yaml"
	contextGitignore    = ".gitignore"
)

// LocalConfig stores project-local context management preferences.
type LocalConfig struct {
	ContextWindow int `yaml:"context_window"`
}

// NewProjectStore creates a project-local context artifact store.
func NewProjectStore(projectRoot string) *ProjectStore {
	if strings.TrimSpace(projectRoot) == "" {
		projectRoot = "."
	}
	return &ProjectStore{
		ProjectRoot: projectRoot,
		ContextDir:  filepath.Join(projectRoot, contextDirName),
	}
}

// Ensure creates the context artifact directories and local ignore rules.
func (s *ProjectStore) Ensure(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.normalize()
	if err := s.validateContextDir(); err != nil {
		return err
	}
	if err := os.MkdirAll(s.toolResultsDir(), 0755); err != nil {
		return fmt.Errorf("创建上下文产物目录失败: %w", err)
	}
	if err := s.ensureGitignore(); err != nil {
		return err
	}
	return nil
}

// StoreToolResult writes a full tool result into the project-local store.
func (s *ProjectStore) StoreToolResult(ctx context.Context, result llm.ToolResult, preview string) (StoredToolResult, error) {
	if err := ctx.Err(); err != nil {
		return StoredToolResult{}, err
	}
	if err := s.Ensure(ctx); err != nil {
		return StoredToolResult{}, err
	}

	name := storedToolResultFileName(result.ToolUseID)
	absPath := filepath.Join(s.toolResultsDir(), name)
	if err := s.validatePath(absPath); err != nil {
		return StoredToolResult{}, err
	}
	if err := os.WriteFile(absPath, []byte(result.Content), 0644); err != nil {
		return StoredToolResult{}, fmt.Errorf("保存工具结果失败: %w", err)
	}

	relPath, err := filepath.Rel(s.projectRoot(), absPath)
	if err != nil {
		relPath = absPath
	}
	if strings.TrimSpace(preview) == "" {
		preview = previewText(result.Content, 1200)
	}
	return StoredToolResult{
		ToolUseID: result.ToolUseID,
		Path:      filepath.ToSlash(relPath),
		Bytes:     len([]byte(result.Content)),
		Preview:   preview,
	}, nil
}

// LoadLocalConfig reads project-local context configuration.
func (s *ProjectStore) LoadLocalConfig(ctx context.Context) (LocalConfig, bool, error) {
	if err := ctx.Err(); err != nil {
		return LocalConfig{}, false, err
	}
	s.normalize()
	path := s.localConfigPath()
	if err := s.validatePath(path); err != nil {
		return LocalConfig{}, false, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return LocalConfig{}, false, nil
		}
		return LocalConfig{}, false, fmt.Errorf("读取上下文本地配置失败: %w", err)
	}
	var cfg LocalConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return LocalConfig{}, false, fmt.Errorf("上下文本地配置格式错误: %w", err)
	}
	if cfg.ContextWindow < 0 {
		return LocalConfig{}, false, fmt.Errorf("上下文本地配置 context_window 必须为正数")
	}
	return cfg, true, nil
}

// SaveLocalConfig writes project-local context configuration.
func (s *ProjectStore) SaveLocalConfig(ctx context.Context, cfg LocalConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if cfg.ContextWindow < 0 {
		return fmt.Errorf("上下文本地配置 context_window 必须为正数")
	}
	if err := s.Ensure(ctx); err != nil {
		return err
	}
	path := s.localConfigPath()
	if err := s.validatePath(path); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("序列化上下文本地配置失败: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("写入上下文本地配置失败: %w", err)
	}
	return nil
}

func (s *ProjectStore) normalize() {
	if strings.TrimSpace(s.ProjectRoot) == "" {
		s.ProjectRoot = "."
	}
	if strings.TrimSpace(s.ContextDir) == "" {
		s.ContextDir = filepath.Join(s.ProjectRoot, contextDirName)
	}
}

func (s *ProjectStore) projectRoot() string {
	if strings.TrimSpace(s.ProjectRoot) == "" {
		return "."
	}
	return s.ProjectRoot
}

func (s *ProjectStore) toolResultsDir() string {
	return filepath.Join(s.ContextDir, toolResultsDirName)
}

func (s *ProjectStore) localConfigPath() string {
	return filepath.Join(s.ContextDir, localConfigFileName)
}

func (s *ProjectStore) validateContextDir() error {
	expected := filepath.Join(s.projectRoot(), contextDirName)
	if !pathWithin(expected, s.ContextDir) {
		return fmt.Errorf("拒绝写入项目上下文目录外: %s", s.ContextDir)
	}
	return nil
}

func (s *ProjectStore) validatePath(path string) error {
	s.normalize()
	if err := s.validateContextDir(); err != nil {
		return err
	}
	if !pathWithin(s.ContextDir, path) {
		return fmt.Errorf("拒绝写入项目上下文目录外: %s", path)
	}
	return nil
}

func (s *ProjectStore) ensureGitignore() error {
	path := filepath.Join(s.ContextDir, contextGitignore)
	if err := s.validatePath(path); err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("读取上下文 .gitignore 失败: %w", err)
	}

	content := string(data)
	lines := map[string]bool{}
	for _, line := range strings.Split(content, "\n") {
		lines[strings.TrimSpace(line)] = true
	}

	required := []string{localConfigFileName, toolResultsDirName + "/"}
	var missing []string
	for _, line := range required {
		if !lines[line] {
			missing = append(missing, line)
		}
	}
	if len(missing) == 0 {
		return nil
	}

	var builder strings.Builder
	builder.WriteString(content)
	if content != "" && !strings.HasSuffix(content, "\n") {
		builder.WriteString("\n")
	}
	for _, line := range missing {
		builder.WriteString(line)
		builder.WriteString("\n")
	}
	if err := os.WriteFile(path, []byte(builder.String()), 0644); err != nil {
		return fmt.Errorf("写入上下文 .gitignore 失败: %w", err)
	}
	return nil
}

func storedToolResultFileName(toolUseID string) string {
	id := sanitizeFileName(toolUseID)
	if id == "" {
		id = "tool-result"
	}
	return time.Now().UTC().Format("20060102-150405") + "-" + id + ".txt"
}

func sanitizeFileName(value string) string {
	value = strings.TrimSpace(value)
	var builder strings.Builder
	for _, r := range value {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), r == '-', r == '_', r == '.':
			builder.WriteRune(r)
		default:
			builder.WriteByte('_')
		}
		if builder.Len() >= 80 {
			break
		}
	}
	return strings.Trim(builder.String(), ".")
}

func previewText(value string, maxRunes int) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\r\n", "\n"))
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return string(runes[:maxRunes]) + "\n... truncated"
}

func pathWithin(root, path string) bool {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	pathAbs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(rootAbs, pathAbs)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}
