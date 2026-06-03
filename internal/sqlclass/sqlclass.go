package sqlclass

import (
	"regexp"
	"strings"
)

type Kind string

const (
	KindInsert Kind = "insert"
	KindUpdate Kind = "update"
	KindDelete Kind = "delete"
)

var whereRE = regexp.MustCompile(`(?i)\bwhere\b`)

func IsReadOnly(sql string) bool {
	keyword := firstKeyword(sql)
	switch keyword {
	case "select", "show", "describe", "desc", "explain":
		return true
	default:
		return false
	}
}

func ClassifyDML(sql string) (kind Kind, hasWhere bool, ok bool) {
	switch firstKeyword(sql) {
	case "insert":
		return KindInsert, false, true
	case "update":
		return KindUpdate, hasWhereToken(sql), true
	case "delete":
		return KindDelete, hasWhereToken(sql), true
	default:
		return "", false, false
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

func hasWhereToken(sql string) bool {
	return whereRE.MatchString(sql)
}
