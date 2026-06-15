package postgres

import (
	"context"
	"testing"

	"github.com/JiangHe12/opskit-core/apperrors"
)

func TestPlanRowsFromExplainJSON(t *testing.T) {
	t.Parallel()

	got, err := planRowsFromExplainJSON(`[{"Plan":{"Node Type":"Seq Scan","Plan Rows":42}}]`)
	if err != nil {
		t.Fatalf("planRowsFromExplainJSON() error = %v", err)
	}
	if got != 42 {
		t.Fatalf("planRowsFromExplainJSON() = %d, want 42", got)
	}
}

func TestUnsupportedPostgresMethodsReturnNotImplemented(t *testing.T) {
	t.Parallel()

	backend := &Backend{}
	tests := []struct {
		name string
		run  func() error
	}{
		{name: "introspect", run: func() error { _, err := backend.IntrospectSchema(context.Background()); return err }},
		{name: "table ddl", run: func() error { _, err := backend.TableDDL(context.Background(), "users"); return err }},
		{name: "render ddl", run: func() error { _, err := backend.RenderDDL(nil); return err }},
		{name: "exec ddl", run: func() error { _, err := backend.ExecDDL(context.Background(), nil); return err }},
		{name: "exec dml", run: func() error { _, err := backend.ExecDML(context.Background(), "UPDATE t SET a=1"); return err }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			appErr := apperrors.AsAppError(tt.run())
			if appErr.Code != apperrors.CodeNotImplemented {
				t.Fatalf("code = %s, want %s", appErr.Code, apperrors.CodeNotImplemented)
			}
		})
	}
}
