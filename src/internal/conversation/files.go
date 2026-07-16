package conversation

import (
	"sort"
	"strings"
	"time"

	"onecode/internal/llm"
)

const (
	defaultFileIndexLimit       = 20
	defaultFileIndexPreviewSize = 800
)

// ObserveToolCall records files referenced by a tool call and its result.
func (idx *FileIndex) ObserveToolCall(call llm.ToolCall, result llm.ToolResult) {
	if idx == nil {
		return
	}
	switch call.Name {
	case "read_file":
		if path := stringArg(call.Input, "path"); path != "" {
			idx.addOrUpdate(path, previewText(result.Content, defaultFileIndexPreviewSize), "read")
		}
	case "write_file", "edit_file":
		if path := stringArg(call.Input, "path"); path != "" {
			idx.addOrUpdate(path, previewText(result.Content, defaultFileIndexPreviewSize), "edited")
		}
	}
	idx.trim(defaultFileIndexLimit)
}

// ObserveStoredToolResult records a stored tool result path.
func (idx *FileIndex) ObserveStoredToolResult(stored StoredToolResult) {
	if idx == nil || strings.TrimSpace(stored.Path) == "" {
		return
	}
	idx.addOrUpdate(stored.Path, previewText(stored.Preview, defaultFileIndexPreviewSize), "stored tool result")
	idx.trim(defaultFileIndexLimit)
}

// Recent returns most recently observed file entries.
func (idx *FileIndex) Recent(limit int) []FileIndexEntry {
	if idx == nil || limit == 0 {
		return nil
	}
	entries := append([]FileIndexEntry(nil), idx.Entries...)
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].LastSeenAt.After(entries[j].LastSeenAt)
	})
	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}
	return entries
}

func (idx *FileIndex) addOrUpdate(path, preview, reason string) {
	path = normalizeIndexedPath(path)
	if path == "" {
		return
	}
	now := time.Now()
	for i := range idx.Entries {
		if idx.Entries[i].Path != path {
			continue
		}
		if strings.TrimSpace(preview) != "" {
			idx.Entries[i].Preview = previewText(preview, defaultFileIndexPreviewSize)
		}
		if strings.TrimSpace(reason) != "" {
			idx.Entries[i].Reason = reason
		}
		idx.Entries[i].LastSeenAt = now
		return
	}
	idx.Entries = append(idx.Entries, FileIndexEntry{
		Path:       path,
		Preview:    previewText(preview, defaultFileIndexPreviewSize),
		Reason:     reason,
		LastSeenAt: now,
	})
}

func (idx *FileIndex) trim(limit int) {
	if limit <= 0 || len(idx.Entries) <= limit {
		return
	}
	recent := idx.Recent(limit)
	idx.Entries = recent
}

func stringArg(input map[string]interface{}, key string) string {
	value, ok := input[key]
	if !ok {
		return ""
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return ""
}

func normalizeIndexedPath(path string) string {
	path = strings.TrimSpace(strings.ReplaceAll(path, "\\", "/"))
	path = strings.Trim(path, "/")
	if path == "." || path == ".." || strings.HasPrefix(path, "../") {
		return ""
	}
	return path
}
