package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	coreaudit "github.com/JiangHe12/opskit-core/audit"
)

func TestEventAppendRecordVerifyAndQueryRaw(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	impact := 42
	event := New(EventTypeSchemaDiff, "ci", Context{Name: "prod", Env: "prod", Protected: true}, Target{
		Database:   "appdb",
		ObjectType: "table",
		Object:     "users",
	})
	event.Timestamp = time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	event.Ticket = "DB-123"
	event.Reason = "review schema drift"
	event.Statement = "ALTER TABLE users DROP COLUMN legacy"
	event.Risk = "R3"
	event.ImpactRows = &impact
	event.Destructive = true
	event.DryRun = true
	event.Status = StatusSucceeded

	path := filepath.Join(home, ".dbgov", "audit.log")
	if err := coreaudit.AppendRecord(path, event, coreaudit.Options{}); err != nil {
		t.Fatalf("AppendRecord() error = %v", err)
	}
	verify, err := coreaudit.Verify(path, coreaudit.VerifyOptions{})
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if verify.Valid != 1 || verify.SchemaErrors != 0 {
		t.Fatalf("Verify() = %+v, want one valid record without schema errors", verify)
	}
	result, err := coreaudit.QueryRaw(path, coreaudit.Filter{EventType: string(EventTypeSchemaDiff), Operator: "ci"})
	if err != nil {
		t.Fatalf("QueryRaw() error = %v", err)
	}
	if len(result.Records) != 1 {
		t.Fatalf("QueryRaw() records = %d, want 1", len(result.Records))
	}
	var got Event
	if err := json.Unmarshal([]byte(result.Records[0].Line), &got); err != nil {
		t.Fatalf("unmarshal raw event: %v", err)
	}
	if got.APIVersion != APIVersion || got.Kind != KindAuditEvent || got.EventID == "" {
		t.Fatalf("envelope not filled: %+v", got)
	}
	if got.EventType != EventTypeSchemaDiff || got.Operator != "ci" || got.Target.ObjectType != "table" {
		t.Fatalf("event fields lost: %+v", got)
	}
	if got.ImpactRows == nil || *got.ImpactRows != impact || !got.Destructive || !got.DryRun || got.Risk != "R3" {
		t.Fatalf("governance fields lost: %+v", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	raw := string(data)
	for _, want := range []string{`"eventType":"schema.diff"`, `"timestamp":"2026-06-02T12:00:00Z"`, `"operator":"ci"`, `"objectType":"table"`, `"impactRows":42`, `"destructive":true`} {
		if !strings.Contains(raw, want) {
			t.Fatalf("audit JSON missing %s:\n%s", want, raw)
		}
	}
}
