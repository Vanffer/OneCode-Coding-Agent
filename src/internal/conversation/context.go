package conversation

import (
	"context"
	"fmt"
	"time"

	"onecode/internal/llm"
)

const (
	defaultContextWindow           = 256000
	defaultToolResultMaxTokens     = 8000
	defaultToolResultBatchMaxToken = 16000
	defaultRecentTokens            = 10000
	defaultRecentMinMessages       = 5
	defaultSummaryReserveTokens    = 20000
	defaultAutoSafetyMarginTokens  = 13000
	defaultManualSafetyMarginToken = 3000
	defaultForceSafetyMarginTokens = 3000
	defaultMaxCompactFailures      = 3
)

// Option customizes a Conversation.
type Option func(*Conversation)

// WithContextOptions initializes the conversation context with explicit options.
func WithContextOptions(opts ContextOptions) Option {
	return func(c *Conversation) {
		c.context = newContextState(normalizeContextOptions(opts))
	}
}

// ContextState stores context-management runtime state for one conversation.
type ContextState struct {
	ProjectRoot string
	Window      WindowInfo
	Usage       UsageEstimate
	Fuse        CompactFuse
	Store       *ProjectStore
	Files       *FileIndex
}

// WindowInfo describes the context window limit and where it came from.
type WindowInfo struct {
	Limit  int
	Source WindowSource
}

// WindowSource records how the context window was resolved.
type WindowSource int

const (
	WindowSourceLocal WindowSource = iota
	WindowSourceProvider
	WindowSourceInferred
	WindowSourceDefault
)

// UsageEstimate describes the current estimated context usage.
type UsageEstimate struct {
	Used      int
	Limit     int
	Percent   int
	Estimated bool
	Anchor    UsageAnchor
	UpdatedAt time.Time
}

// UsageAnchor stores the last provider-reported usage and message count.
type UsageAnchor struct {
	MessageCount int
	Usage        llm.Usage
}

// CompactFuse prevents repeated automatic compaction failures.
type CompactFuse struct {
	ConsecutiveFailures int
	Tripped             bool
}

// ProjectStore manages project-local context artifacts.
type ProjectStore struct {
	ProjectRoot string
	ContextDir  string
}

// FileIndex records recently relevant files.
type FileIndex struct {
	Entries []FileIndexEntry
}

// FileIndexEntry is one navigational hint for future compaction summaries.
type FileIndexEntry struct {
	Path       string
	Preview    string
	Reason     string
	LastSeenAt time.Time
}

// ContextOptions configures context management.
type ContextOptions struct {
	ProjectRoot              string
	ProviderName             string
	ModelName                string
	ProviderWindow           int
	ToolResultMaxTokens      int
	ToolResultBatchMaxTokens int
	RecentTokens             int
	RecentMinMessages        int
	SummaryReserveTokens     int
	AutoSafetyMarginTokens   int
	ManualSafetyMarginTokens int
	ForceSafetyMarginTokens  int
	MaxCompactFailures       int
}

// PreflightResult describes changes made before a normal model request.
type PreflightResult struct {
	Usage              UsageEstimate
	BoundedToolResults []StoredToolResult
	Compacted          bool
	CompactMode        CompactMode
	Statuses           []ContextStatus
}

// StoredToolResult describes a full tool result stored on disk.
type StoredToolResult struct {
	ToolUseID string
	Path      string
	Bytes     int
	Preview   string
}

// CompactMode identifies why compaction is running.
type CompactMode int

const (
	CompactModeAuto CompactMode = iota
	CompactModeManual
	CompactModeEmergency
	CompactModeForce
)

// ContextStatus is a structured status update from context management.
type ContextStatus struct {
	Kind    ContextStatusKind
	Message string
	Usage   UsageEstimate
}

// ContextStatusKind identifies context-management state transitions.
type ContextStatusKind int

const (
	ContextStatusUsageUpdated ContextStatusKind = iota
	ContextStatusToolResultStored
	ContextStatusCompactStarted
	ContextStatusCompactCompleted
	ContextStatusCompactFailed
	ContextStatusCompactFuseTripped
)

func newContextState(opts ContextOptions) *ContextState {
	opts = normalizeContextOptions(opts)
	window := WindowInfo{Limit: opts.ProviderWindow, Source: WindowSourceProvider}
	if window.Limit <= 0 {
		window = WindowInfo{Limit: defaultContextWindow, Source: WindowSourceDefault}
	}
	return &ContextState{
		ProjectRoot: opts.ProjectRoot,
		Window:      window,
		Usage: UsageEstimate{
			Limit:     window.Limit,
			Estimated: true,
			UpdatedAt: time.Now(),
		},
		Store: NewProjectStore(opts.ProjectRoot),
		Files: &FileIndex{},
	}
}

func normalizeContextOptions(opts ContextOptions) ContextOptions {
	if opts.ToolResultMaxTokens <= 0 {
		opts.ToolResultMaxTokens = defaultToolResultMaxTokens
	}
	if opts.ToolResultBatchMaxTokens <= 0 {
		opts.ToolResultBatchMaxTokens = defaultToolResultBatchMaxToken
	}
	if opts.RecentTokens <= 0 {
		opts.RecentTokens = defaultRecentTokens
	}
	if opts.RecentMinMessages <= 0 {
		opts.RecentMinMessages = defaultRecentMinMessages
	}
	if opts.SummaryReserveTokens <= 0 {
		opts.SummaryReserveTokens = defaultSummaryReserveTokens
	}
	if opts.AutoSafetyMarginTokens <= 0 {
		opts.AutoSafetyMarginTokens = defaultAutoSafetyMarginTokens
	}
	if opts.ManualSafetyMarginTokens <= 0 {
		opts.ManualSafetyMarginTokens = defaultManualSafetyMarginToken
	}
	if opts.ForceSafetyMarginTokens <= 0 {
		opts.ForceSafetyMarginTokens = defaultForceSafetyMarginTokens
	}
	if opts.MaxCompactFailures <= 0 {
		opts.MaxCompactFailures = defaultMaxCompactFailures
	}
	return opts
}

// UpdateUsage stores the latest provider-reported usage as the estimation anchor.
func (c *Conversation) UpdateUsage(usage llm.Usage) {
	if !usage.Available {
		return
	}
	state := c.ensureContext()
	total := usageTotal(usage)
	limit := state.Window.Limit
	if limit <= 0 {
		limit = defaultContextWindow
	}
	percent := 0
	if limit > 0 {
		percent = total * 100 / limit
	}
	state.Usage = UsageEstimate{
		Used:      total,
		Limit:     limit,
		Percent:   percent,
		Estimated: false,
		Anchor: UsageAnchor{
			MessageCount: len(c.messages),
			Usage:        usage,
		},
		UpdatedAt: time.Now(),
	}
}

// Preflight prepares the conversation before a normal model request.
func (c *Conversation) Preflight(ctx context.Context, compressor Compressor, opts ContextOptions) (PreflightResult, error) {
	return c.maintainContext(ctx, compressor, opts)
}

// PostToolResults maintains the conversation after tool results are appended.
func (c *Conversation) PostToolResults(ctx context.Context, compressor Compressor, opts ContextOptions) (PreflightResult, error) {
	return c.maintainContext(ctx, compressor, opts)
}

func (c *Conversation) maintainContext(ctx context.Context, compressor Compressor, opts ContextOptions) (PreflightResult, error) {
	state := c.ensureContext()
	opts = c.prepareOptions(opts)
	if err := c.prepareStoreAndWindow(ctx, opts); err != nil {
		return PreflightResult{}, err
	}

	result := PreflightResult{}
	bounder := ToolResultBounder{Store: state.Store}
	bound, err := bounder.Bound(c.messages, BoundOptions{
		SingleMaxTokens: opts.ToolResultMaxTokens,
		BatchMaxTokens:  opts.ToolResultBatchMaxTokens,
	})
	if err != nil {
		return result, err
	}
	if bound.Changed {
		c.messages = bound.Messages
		result.BoundedToolResults = bound.Stored
		for _, stored := range bound.Stored {
			state.Files.ObserveStoredToolResult(stored)
			result.Statuses = append(result.Statuses, ContextStatus{
				Kind:    ContextStatusToolResultStored,
				Message: fmt.Sprintf("工具结果已保存: %s", stored.Path),
				Usage:   state.Usage,
			})
		}
	}

	estimator := TokenEstimator{}
	usage := estimator.Estimate(c.messages, state.Window, state.Usage.Anchor)
	state.Usage = usage
	result.Usage = usage
	result.Statuses = append(result.Statuses, ContextStatus{
		Kind:    ContextStatusUsageUpdated,
		Message: "上下文用量已更新",
		Usage:   usage,
	})

	compactor := Compactor{Estimator: estimator}
	mode := CompactModeAuto
	shouldCompact := false
	if compactor.ShouldCompact(usage, state.Window, CompactModeForce, opts, state.Fuse) {
		mode = CompactModeForce
		shouldCompact = true
	} else if compactor.ShouldCompact(usage, state.Window, CompactModeAuto, opts, state.Fuse) {
		mode = CompactModeAuto
		shouldCompact = true
	}
	if !shouldCompact {
		return result, nil
	}

	compactResult, err := compactor.Compact(ctx, c.messages, *state, compressor, mode, opts)
	result.Statuses = append(result.Statuses, compactResult.Statuses...)
	if err != nil {
		if mode == CompactModeAuto {
			c.recordAutoCompactFailure(opts, &result)
			return result, nil
		}
		return result, err
	}
	c.messages = compactResult.Messages
	state.Usage = compactResult.Usage
	result.Usage = compactResult.Usage
	result.Compacted = true
	result.CompactMode = mode
	if mode == CompactModeAuto {
		state.Fuse = CompactFuse{}
	}
	return result, nil
}

// Compact runs a manual, emergency, or force compaction immediately.
func (c *Conversation) Compact(ctx context.Context, compressor Compressor, mode CompactMode, opts ContextOptions) (CompactResult, error) {
	state := c.ensureContext()
	opts = c.prepareOptions(opts)
	if err := c.prepareStoreAndWindow(ctx, opts); err != nil {
		return CompactResult{}, err
	}
	result, err := (Compactor{}).Compact(ctx, c.messages, *state, compressor, mode, opts)
	if err != nil {
		return result, err
	}
	c.messages = result.Messages
	state.Usage = result.Usage
	if mode == CompactModeAuto {
		state.Fuse = CompactFuse{}
	}
	return result, nil
}

// SetContextWindow stores a project-local context window override.
func (c *Conversation) SetContextWindow(ctx context.Context, limit int) (WindowInfo, error) {
	if limit <= 0 {
		return WindowInfo{}, fmt.Errorf("context window must be positive")
	}
	state := c.ensureContext()
	if state.Store == nil {
		state.Store = NewProjectStore(state.ProjectRoot)
	}
	if err := state.Store.SaveLocalConfig(ctx, LocalConfig{ContextWindow: limit}); err != nil {
		return WindowInfo{}, err
	}
	window, err := (WindowResolver{Store: state.Store}).Resolve(ctx, ContextOptions{ProjectRoot: state.ProjectRoot})
	if err != nil {
		return WindowInfo{}, err
	}
	state.Window = window
	state.Usage = TokenEstimator{}.Estimate(c.messages, window, state.Usage.Anchor)
	return window, nil
}

func (c *Conversation) ensureContext() *ContextState {
	if c.context == nil {
		c.context = newContextState(ContextOptions{})
	}
	if c.context.Store == nil {
		c.context.Store = NewProjectStore(c.context.ProjectRoot)
	}
	if c.context.Files == nil {
		c.context.Files = &FileIndex{}
	}
	return c.context
}

func (c *Conversation) prepareOptions(opts ContextOptions) ContextOptions {
	state := c.ensureContext()
	if opts.ProjectRoot == "" {
		opts.ProjectRoot = state.ProjectRoot
	}
	if opts.ProjectRoot != "" {
		state.ProjectRoot = opts.ProjectRoot
	}
	return normalizeContextOptions(opts)
}

func (c *Conversation) prepareStoreAndWindow(ctx context.Context, opts ContextOptions) error {
	state := c.ensureContext()
	if state.Store == nil || state.Store.ProjectRoot != opts.ProjectRoot {
		state.Store = NewProjectStore(opts.ProjectRoot)
	}
	window, err := (WindowResolver{Store: state.Store}).Resolve(ctx, opts)
	if err != nil {
		return err
	}
	state.Window = window
	return nil
}

func (c *Conversation) recordAutoCompactFailure(opts ContextOptions, result *PreflightResult) {
	state := c.ensureContext()
	state.Fuse.ConsecutiveFailures++
	if state.Fuse.ConsecutiveFailures >= opts.MaxCompactFailures {
		state.Fuse.Tripped = true
		result.Statuses = append(result.Statuses, ContextStatus{
			Kind:    ContextStatusCompactFuseTripped,
			Message: "自动上下文压缩已熔断",
			Usage:   state.Usage,
		})
	}
}
