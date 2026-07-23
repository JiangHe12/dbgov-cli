package backend

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/JiangHe12/opskit-core/v2/apperrors"

	"github.com/JiangHe12/dbgov-cli/internal/schema"
)

type Backend interface {
	Close() error
	Ping(ctx context.Context) error
	IntrospectSchema(ctx context.Context) (schema.Schema, error)
	Query(ctx context.Context, sql string) (QueryResult, error)
	Explain(ctx context.Context, sql string) (ExplainResult, error)
	TableDDL(ctx context.Context, table string) (string, error)
	RenderDDL(changes []schema.Change) ([]string, error)
	ExecDDL(ctx context.Context, statements []string) (executed int, err error)
	ExecDML(ctx context.Context, sql string) (affected int64, err error)
	ExecDMLBound(ctx context.Context, sql string, binding DMLPlanBinding) (affected int64, err error)
}

type QueryResult struct {
	Columns []string   `json:"columns"`
	Rows    [][]string `json:"rows"`
	Nulls   [][]bool   `json:"-"`
}

type ExplainResult struct {
	Columns         []string   `json:"columns"`
	Rows            [][]string `json:"rows"`
	Nulls           [][]bool   `json:"-"`
	EstimatedRows   int64      `json:"estimatedRows"`
	PlanFingerprint string     `json:"planFingerprint"`
}

// MarshalJSON preserves SQL NULL as JSON null instead of conflating it with
// an empty string. Rows remains [][]string for compatibility with table output
// and callers that consume the existing Go API.
func (r QueryResult) MarshalJSON() ([]byte, error) {
	type queryResultJSON struct {
		Columns []string `json:"columns"`
		Rows    [][]any  `json:"rows"`
	}
	return json.Marshal(queryResultJSON{Columns: r.Columns, Rows: typedRows(r.Rows, r.Nulls)})
}

// MarshalJSON preserves SQL NULL in explain output as well as query output.
func (r ExplainResult) MarshalJSON() ([]byte, error) {
	type explainResultJSON struct {
		Columns         []string `json:"columns"`
		Rows            [][]any  `json:"rows"`
		EstimatedRows   int64    `json:"estimatedRows"`
		PlanFingerprint string   `json:"planFingerprint"`
	}
	return json.Marshal(explainResultJSON{
		Columns:         r.Columns,
		Rows:            typedRows(r.Rows, r.Nulls),
		EstimatedRows:   r.EstimatedRows,
		PlanFingerprint: r.PlanFingerprint,
	})
}

// DisplayRows returns a table-safe copy where SQL NULL is visibly distinct
// from an empty string.
func (r QueryResult) DisplayRows() [][]string {
	return displayRows(r.Rows, r.Nulls)
}

// DisplayRows returns a table-safe copy where SQL NULL is visibly distinct
// from an empty string.
func (r ExplainResult) DisplayRows() [][]string {
	return displayRows(r.Rows, r.Nulls)
}

func typedRows(rows [][]string, nulls [][]bool) [][]any {
	if rows == nil {
		return nil
	}
	result := make([][]any, len(rows))
	for rowIndex, row := range rows {
		result[rowIndex] = make([]any, len(row))
		for columnIndex, value := range row {
			if isNullCell(nulls, rowIndex, columnIndex) {
				result[rowIndex][columnIndex] = nil
				continue
			}
			result[rowIndex][columnIndex] = value
		}
	}
	return result
}

func displayRows(rows [][]string, nulls [][]bool) [][]string {
	if rows == nil {
		return nil
	}
	result := make([][]string, len(rows))
	for rowIndex, row := range rows {
		result[rowIndex] = append([]string(nil), row...)
		for columnIndex := range row {
			if isNullCell(nulls, rowIndex, columnIndex) {
				result[rowIndex][columnIndex] = "NULL"
			}
		}
	}
	return result
}

func isNullCell(nulls [][]bool, rowIndex, columnIndex int) bool {
	return rowIndex < len(nulls) && columnIndex < len(nulls[rowIndex]) && nulls[rowIndex][columnIndex]
}

// DMLPlanBinding binds a governed execution to the exact EXPLAIN result that
// was risk-classified before authorization.
type DMLPlanBinding struct {
	PlanFingerprint string
	EstimatedRows   int64
}

// CommitIndeterminateError reports that the database returned an error while
// committing a transaction, so the caller cannot safely claim either success
// or rollback.
type CommitIndeterminateError struct {
	cause error
}

func (e *CommitIndeterminateError) Error() string {
	if e == nil || e.cause == nil {
		return "transaction commit outcome is indeterminate"
	}
	return e.cause.Error()
}

func (e *CommitIndeterminateError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// NewCommitIndeterminateError preserves a commit error's application error
// classification while making the uncertain transaction outcome detectable.
func NewCommitIndeterminateError(cause error) error {
	if cause == nil {
		return nil
	}
	appErr := apperrors.New(
		apperrors.CodePartialFailure,
		"transaction commit outcome is indeterminate; do not retry automatically",
		cause,
	).WithSuggestion("inspect the target database and audit record before deciding whether to retry")
	return &CommitIndeterminateError{cause: appErr}
}

// IsCommitIndeterminate reports whether err represents an unknown commit
// outcome, including when another error wraps it.
func IsCommitIndeterminate(err error) bool {
	var target *CommitIndeterminateError
	return errors.As(err, &target)
}

// PlanFingerprint returns a deterministic, domain-separated fingerprint for
// the database-provided EXPLAIN columns and rows.
func PlanFingerprint(result QueryResult) string {
	data, _ := json.Marshal(result)
	sum := sha256.Sum256(append([]byte("dbgov-cli.io/dml-plan/v1\x00"), data...))
	return "sha256:" + hex.EncodeToString(sum[:])
}
