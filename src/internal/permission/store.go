package permission

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Store loads layered permission rules and persists local rules.
type Store interface {
	Load(ctx context.Context) ([]RuleSet, Mode, error)
	AppendLocalRule(ctx context.Context, rule Rule) error
}

// FileStore reads user/project/local YAML permission files.
type FileStore struct {
	UserPath    string
	ProjectPath string
	LocalPath   string
	ProjectRoot string
}

func NewFileStore(userPath, projectPath, localPath, projectRoot string) *FileStore {
	return &FileStore{
		UserPath:    userPath,
		ProjectPath: projectPath,
		LocalPath:   localPath,
		ProjectRoot: projectRoot,
	}
}

func DefaultFileStore(projectRoot string) *FileStore {
	userPath := ""
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		userPath = filepath.Join(home, ".onecode", "permissions.yaml")
	}
	return NewFileStore(
		userPath,
		filepath.Join(projectRoot, ".onecode", "permissions.yaml"),
		filepath.Join(projectRoot, ".onecode", "permissions.local.yaml"),
		projectRoot,
	)
}

func (s *FileStore) Load(ctx context.Context) ([]RuleSet, Mode, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	var sets []RuleSet
	mode := ModeDefault
	userMode := Mode("")
	localMode := Mode("")

	if set, cfgMode, ok, err := loadRuleFile(s.UserPath, ScopeUser); err != nil {
		return nil, "", err
	} else if ok {
		sets = append(sets, set)
		userMode = cfgMode
	}
	if set, _, ok, err := loadRuleFile(s.ProjectPath, ScopeProject); err != nil {
		return nil, "", err
	} else if ok {
		sets = append(sets, set)
	}
	if set, cfgMode, ok, err := loadRuleFile(s.LocalPath, ScopeLocal); err != nil {
		return nil, "", err
	} else if ok {
		sets = append(sets, set)
		localMode = cfgMode
	}

	if userMode != "" {
		mode = userMode
	}
	if localMode != "" {
		mode = localMode
	}

	return sets, mode, nil
}

func (s *FileStore) AppendLocalRule(ctx context.Context, rule Rule) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.validateLocalPath(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.LocalPath), 0755); err != nil {
		return fmt.Errorf("创建权限配置目录失败: %w", err)
	}

	cfg := Config{}
	if data, err := os.ReadFile(s.LocalPath); err == nil {
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return fmt.Errorf("本地权限配置格式错误: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("读取本地权限配置失败: %w", err)
	}

	rule.Scope = ScopeLocal
	cfg.Rules = append(cfg.Rules, RawRule(FormatRule(rule)))
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("序列化本地权限配置失败: %w", err)
	}
	if err := os.WriteFile(s.LocalPath, data, 0644); err != nil {
		return fmt.Errorf("写入本地权限配置失败: %w", err)
	}
	return nil
}

func (s *FileStore) validateLocalPath() error {
	if s.ProjectRoot == "" || s.LocalPath == "" {
		return fmt.Errorf("本地权限配置路径未设置")
	}
	root, err := filepath.Abs(filepath.Join(s.ProjectRoot, ".onecode"))
	if err != nil {
		return err
	}
	local, err := filepath.Abs(s.LocalPath)
	if err != nil {
		return err
	}
	if !pathWithinRoot(root, local) {
		return fmt.Errorf("拒绝写入项目 .onecode 目录外的权限配置: %s", s.LocalPath)
	}
	return nil
}

func loadRuleFile(path string, scope Scope) (RuleSet, Mode, bool, error) {
	if path == "" {
		return RuleSet{}, "", false, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return RuleSet{}, "", false, nil
		}
		return RuleSet{}, "", false, fmt.Errorf("读取权限配置失败 %s: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return RuleSet{}, "", false, fmt.Errorf("权限配置格式错误 %s: %w", path, err)
	}
	if cfg.Mode != "" && !validMode(cfg.Mode) {
		return RuleSet{}, "", false, fmt.Errorf("权限模式无效 %s: %s", path, cfg.Mode)
	}
	set := RuleSet{Scope: scope}
	for _, raw := range cfg.Rules {
		rule, err := ParseRule(string(raw), scope)
		if err != nil {
			return RuleSet{}, "", false, fmt.Errorf("权限规则错误 %s: %w", path, err)
		}
		set.Rules = append(set.Rules, rule)
	}
	return set, cfg.Mode, true, nil
}

func validMode(mode Mode) bool {
	switch mode {
	case ModeStrict, ModeDefault, ModePermissive, ModeBypass:
		return true
	default:
		return false
	}
}
