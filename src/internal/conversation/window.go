package conversation

import (
	"context"
	"strconv"
	"strings"
)

// WindowResolver resolves the context window for a provider/model.
type WindowResolver struct {
	Store *ProjectStore
}

// Resolve returns the context window using local > provider > inferred > default.
func (r WindowResolver) Resolve(ctx context.Context, opts ContextOptions) (WindowInfo, error) {
	store := r.Store
	if store == nil {
		store = NewProjectStore(opts.ProjectRoot)
	}
	if cfg, ok, err := store.LoadLocalConfig(ctx); err != nil {
		return WindowInfo{}, err
	} else if ok && cfg.ContextWindow > 0 {
		return WindowInfo{Limit: cfg.ContextWindow, Source: WindowSourceLocal}, nil
	}

	if opts.ProviderWindow > 0 {
		return WindowInfo{Limit: opts.ProviderWindow, Source: WindowSourceProvider}, nil
	}
	if inferred, ok := InferWindow(opts.ProviderName, opts.ModelName); ok {
		return inferred, nil
	}
	return WindowInfo{Limit: defaultContextWindow, Source: WindowSourceDefault}, nil
}

// InferWindow returns a context window declared in the model name suffix.
func InferWindow(providerName, modelName string) (WindowInfo, bool) {
	if limit, ok := inferWindowFromSuffix(modelName); ok {
		return WindowInfo{Limit: limit, Source: WindowSourceInferred}, true
	}
	if limit, ok := inferWindowFromSuffix(providerName); ok {
		return WindowInfo{Limit: limit, Source: WindowSourceInferred}, true
	}
	return WindowInfo{}, false
}

func inferWindowFromSuffix(value string) (int, bool) {
	value = strings.TrimSpace(value)
	if !strings.HasSuffix(value, "]") {
		return 0, false
	}
	start := strings.LastIndex(value, "[")
	if start < 0 {
		return 0, false
	}
	marker := strings.TrimSpace(value[start+1 : len(value)-1])
	if marker == "" {
		return 0, false
	}
	return parseWindowMarker(marker)
}

func parseWindowMarker(marker string) (int, bool) {
	marker = strings.ToLower(strings.TrimSpace(marker))
	if marker == "" {
		return 0, false
	}
	multiplier := 1
	if suffix := marker[len(marker)-1:]; suffix == "k" || suffix == "m" {
		marker = strings.TrimSpace(marker[:len(marker)-1])
		if suffix == "k" {
			multiplier = 1000
		} else {
			multiplier = 1000000
		}
	}
	value, err := strconv.Atoi(marker)
	if err != nil || value <= 0 {
		return 0, false
	}
	return value * multiplier, true
}
