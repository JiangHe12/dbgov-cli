package sqlclass

import "strings"

func IsReadOnly(sql string) bool {
	keyword := firstKeyword(sql)
	switch keyword {
	case "select", "show", "describe", "desc", "explain":
		return true
	default:
		return false
	}
}

func firstKeyword(sql string) string {
	trimmed := strings.TrimSpace(sql)
	if trimmed == "" {
		return ""
	}
	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return ""
	}
	return strings.ToLower(fields[0])
}
