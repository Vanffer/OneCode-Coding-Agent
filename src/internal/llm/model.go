package llm

import (
	"strconv"
	"strings"
)

func requestModelName(model string) string {
	model = strings.TrimSpace(model)
	start, ok := contextWindowSuffix(model)
	if !ok {
		return model
	}
	return strings.TrimSpace(model[:start])
}

func contextWindowSuffix(value string) (int, bool) {
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
	return start, isContextWindowMarker(marker)
}

func isContextWindowMarker(marker string) bool {
	marker = strings.ToLower(strings.TrimSpace(marker))
	if marker == "" {
		return false
	}
	if suffix := marker[len(marker)-1:]; suffix == "k" || suffix == "m" {
		marker = strings.TrimSpace(marker[:len(marker)-1])
	}
	value, err := strconv.Atoi(marker)
	return err == nil && value > 0
}
