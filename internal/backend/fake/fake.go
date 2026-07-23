package fake

import (
	"context"
	"fmt"

	"github.com/JiangHe12/opskit-core/v2/apperrors"

	dbbackend "github.com/JiangHe12/dbgov-cli/internal/backend"
	"github.com/JiangHe12/dbgov-cli/internal/schema"
)

type Backend struct {
	Schema            schema.Schema
	IntrospectSchemas []schema.Schema
	IntrospectCalls   int
	Executed          []string
	FailAt            int
	DDLCommitErr      error
	ExplainRows       int64
	ExplainErr        error
	BoundExplainRows  *int64
	BoundExplainErr   error
	BoundCommitErr    error
	DMLAffected       int64
	DMLErr            error
	ExecutedDML       []string
	DDLs              map[string]string
	TableDDLErr       error
	CloseErr          error
	CloseCalls        int
}

func New() *Backend {
	return &Backend{Schema: schema.Schema{Tables: map[string]schema.Table{
		"users": {
			Name: "users",
			Columns: []schema.Column{
				{Name: "id", Type: "BIGINT"},
				{Name: "legacy", Type: "TEXT"},
			},
			Indexes:     []schema.Index{{Name: "PRIMARY", Columns: []string{"id"}, Unique: true}},
			ForeignKeys: []schema.ForeignKey{{Name: "fk_users_org", Columns: []string{"org_id"}, RefTable: "orgs", RefColumns: []string{"id"}}},
		},
	}}}
}

func (b *Backend) Ping(context.Context) error { return nil }

func (b *Backend) Close() error {
	b.CloseCalls++
	return b.CloseErr
}

func (b *Backend) IntrospectSchema(context.Context) (schema.Schema, error) {
	call := b.IntrospectCalls
	b.IntrospectCalls++
	if len(b.IntrospectSchemas) > 0 {
		if call >= len(b.IntrospectSchemas) {
			call = len(b.IntrospectSchemas) - 1
		}
		return b.IntrospectSchemas[call], nil
	}
	return b.Schema, nil
}

func (b *Backend) Query(context.Context, string) (dbbackend.QueryResult, error) {
	return dbbackend.QueryResult{
		Columns: []string{"id", "name"},
		Rows:    [][]string{{"1", "alice"}, {"2", "bob"}},
	}, nil
}

func (b *Backend) Explain(context.Context, string) (dbbackend.ExplainResult, error) {
	if b.ExplainErr != nil {
		return dbbackend.ExplainResult{}, b.ExplainErr
	}
	rows := b.ExplainRows
	if rows == 0 {
		rows = 2
	}
	return fakeExplainResult(rows), nil
}

func (b *Backend) TableDDL(ctx context.Context, table string) (string, error) {
	if b.TableDDLErr != nil {
		return "", b.TableDDLErr
	}
	if b.DDLs != nil {
		if ddl, ok := b.DDLs[table]; ok {
			return ddl, nil
		}
	}
	return "CREATE TABLE `users` (`id` BIGINT, `legacy` TEXT);", nil
}

func (b *Backend) RenderDDL(changes []schema.Change) ([]string, error) {
	statements := make([]string, 0, len(changes))
	for _, change := range changes {
		switch change.Action {
		case schema.ActionCreateTable:
			statements = append(statements, "CREATE TABLE `"+change.Table+"` (`id` BIGINT);")
		case schema.ActionAddColumn:
			statements = append(statements, "ALTER TABLE `"+change.Table+"` ADD COLUMN `"+change.Column+"` "+change.Type+";")
		case schema.ActionModifyColumn:
			statements = append(statements, "ALTER TABLE `"+change.Table+"` MODIFY COLUMN `"+change.Column+"` "+change.Type+";")
		case schema.ActionDropColumn:
			statements = append(statements, "ALTER TABLE `"+change.Table+"` DROP COLUMN `"+change.Column+"`;")
		case schema.ActionDropTable:
			statements = append(statements, "DROP TABLE `"+change.Table+"`;")
		}
	}
	return statements, nil
}

func (b *Backend) ExecDDL(ctx context.Context, statements []string) (int, error) {
	for i, statement := range statements {
		if b.FailAt == i+1 {
			return i, apperrors.New(apperrors.CodeBackendError, fmt.Sprintf("fake DDL failure at statement %d: %s", i+1, statement), nil)
		}
		b.Executed = append(b.Executed, statement)
	}
	if b.DDLCommitErr != nil {
		return len(statements), dbbackend.NewCommitIndeterminateError(b.DDLCommitErr)
	}
	return len(statements), nil
}

func (b *Backend) ExecDML(ctx context.Context, sql string) (int64, error) {
	if b.DMLErr != nil {
		return 0, b.DMLErr
	}
	b.ExecutedDML = append(b.ExecutedDML, sql)
	return b.DMLAffected, nil
}

func (b *Backend) ExecDMLBound(ctx context.Context, sql string, binding dbbackend.DMLPlanBinding) (int64, error) {
	if binding.PlanFingerprint == "" {
		return 0, apperrors.New(apperrors.CodeValidationFailed, "DML plan binding is required", nil)
	}
	if b.BoundExplainErr != nil {
		return 0, b.BoundExplainErr
	}
	rows := b.ExplainRows
	if rows == 0 {
		rows = 2
	}
	if b.BoundExplainRows != nil {
		rows = *b.BoundExplainRows
	}
	current := fakeExplainResult(rows)
	if current.PlanFingerprint != binding.PlanFingerprint || current.EstimatedRows != binding.EstimatedRows {
		return 0, apperrors.New(apperrors.CodeConflict, "DML plan changed after authorization; preview and authorize again", nil)
	}
	affected, err := b.ExecDML(ctx, sql)
	if err != nil {
		return affected, err
	}
	if b.BoundCommitErr != nil {
		return affected, dbbackend.NewCommitIndeterminateError(b.BoundCommitErr)
	}
	return affected, nil
}

func fakeExplainResult(rows int64) dbbackend.ExplainResult {
	result := dbbackend.QueryResult{
		Columns: []string{"id", "select_type", "table", "rows"},
		Rows:    [][]string{{"1", "SIMPLE", "users", fmt.Sprint(rows)}},
	}
	return dbbackend.ExplainResult{
		Columns:         result.Columns,
		Rows:            result.Rows,
		EstimatedRows:   rows,
		PlanFingerprint: dbbackend.PlanFingerprint(result),
	}
}
