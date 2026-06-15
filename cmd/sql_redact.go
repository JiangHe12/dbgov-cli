package cmd

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/JiangHe12/opskit-core/redact"
)

const (
	redactedSQLLiteral = "'[REDACTED]'"
	sqlQuotedLiteral   = `(?:'(?:''|[^'])*'|"(?:""|[^"])*")`
)

type sqlSecretLiteralPattern struct {
	re          *regexp.Regexp
	replacement string
}

var sqlSecretLiteralPatterns = []sqlSecretLiteralPattern{
	{
		re:          regexp.MustCompile(`(?i)(\bSET\s+PASSWORD\b(?:\s+FOR\s+[^=]+)?\s*=\s*PASSWORD\s*\(\s*)` + sqlQuotedLiteral + `(\s*\))`),
		replacement: `${1}` + redactedSQLLiteral + `${2}`,
	},
	{
		re:          regexp.MustCompile(`(?i)(\bIDENTIFIED\s+BY\s+(?:PASSWORD\s+)?)` + sqlQuotedLiteral),
		replacement: `${1}` + redactedSQLLiteral,
	},
	{
		re:          regexp.MustCompile(`(?i)(\bIDENTIFIED\s+WITH\s+[A-Za-z0-9_.$-]+\s+(?:BY|AS)\s+)` + sqlQuotedLiteral),
		replacement: `${1}` + redactedSQLLiteral,
	},
	{
		re:          regexp.MustCompile(`(?i)(\bPASSWORD\s*\(\s*)` + sqlQuotedLiteral + `(\s*\))`),
		replacement: `${1}` + redactedSQLLiteral + `${2}`,
	},
	{
		re:          regexp.MustCompile(`(?i)(\bSET\s+PASSWORD\b(?:\s+FOR\s+[^=]+)?\s*=\s*)` + sqlQuotedLiteral),
		replacement: `${1}` + redactedSQLLiteral,
	},
	{
		re:          regexp.MustCompile(`(?i)(\bPASSWORD\s+)` + sqlQuotedLiteral),
		replacement: `${1}` + redactedSQLLiteral,
	},
}

var harmlessSQLAssignments = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bSET\s+password_changed\s*=\s*NOW\s*\(\s*\)`),
}

func redactSQL(statement string) string {
	protected := make([]string, 0)
	for _, pattern := range harmlessSQLAssignments {
		statement = pattern.ReplaceAllStringFunc(statement, func(match string) string {
			protected = append(protected, match)
			return sqlRedactionPlaceholder(len(protected) - 1)
		})
	}
	for _, pattern := range sqlSecretLiteralPatterns {
		statement = protectSQLMatches(statement, pattern, &protected)
	}
	statement = redact.String(statement)
	for index, value := range protected {
		statement = strings.ReplaceAll(statement, sqlRedactionPlaceholder(index), value)
	}
	return statement
}

func protectSQLMatches(statement string, pattern sqlSecretLiteralPattern, protected *[]string) string {
	matches := pattern.re.FindAllStringIndex(statement, -1)
	for index := len(matches) - 1; index >= 0; index-- {
		match := matches[index]
		value := pattern.re.ReplaceAllString(statement[match[0]:match[1]], pattern.replacement)
		*protected = append(*protected, value)
		placeholder := sqlRedactionPlaceholder(len(*protected) - 1)
		statement = statement[:match[0]] + placeholder + statement[match[1]:]
	}
	return statement
}

func sqlRedactionPlaceholder(index int) string {
	return fmt.Sprintf("\x00DBGOV_SQL_REDACT_%d\x00", index)
}
