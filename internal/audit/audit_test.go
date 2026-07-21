package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JiangHe12/opskit-core/v2/apperrors"
	coreaudit "github.com/JiangHe12/opskit-core/v2/audit"
)

func TestEventAppendRecordVerifyAndQueryRaw(t *testing.T) {
	home := t.TempDir()
	if err := os.Remove(home); err != nil {
		t.Fatalf("Remove(test home) error = %v", err)
	}
	if err := os.Remove(filepath.Dir(home)); err != nil {
		t.Fatalf("Remove(test parent) error = %v", err)
	}
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
	if err := Append(path, event, coreaudit.Options{}); err != nil {
		t.Fatalf("Append() error = %v", err)
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

func TestAppendAndHistoricalSanitizeRemoveRawSensitiveFields(t *testing.T) {
	home := t.TempDir()
	if err := os.Remove(home); err != nil {
		t.Fatalf("Remove(test home) error = %v", err)
	}
	if err := os.Remove(filepath.Dir(home)); err != nil {
		t.Fatalf("Remove(test parent) error = %v", err)
	}
	path := filepath.Join(home, ".dbgov", "audit.log")
	event := New(
		EventTypeDataExec,
		"tester",
		Context{Name: "prod"},
		Target{Database: "secret-db", ObjectType: "data", Object: "secret-target"},
	)
	event.Ticket = "SECRET-TICKET"
	event.Reason = "SECRET-REASON"
	event.Statement = "UPDATE secrets SET value='SECRET-SQL'"
	event.FailedStatement = "DELETE FROM SECRET-FAILED"
	event.SnapshotID = "SECRET-SNAPSHOT"
	event.Error = &ErrorInfo{Code: "BACKEND_ERROR", Message: "SECRET-ERROR"}
	if err := Append(path, event, coreaudit.Options{}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	result, err := coreaudit.QueryRaw(path, coreaudit.Filter{})
	if err != nil || len(result.Records) != 1 {
		t.Fatalf("QueryRaw() = %+v, %v", result, err)
	}
	data := []byte(result.Records[0].Line)
	for _, raw := range []string{
		"SECRET-TICKET",
		"SECRET-REASON",
		"SECRET-SQL",
		"SECRET-FAILED",
		"SECRET-SNAPSHOT",
		"SECRET-ERROR",
		"secret-db",
		"secret-target",
	} {
		if strings.Contains(string(data), raw) {
			t.Fatalf("audit leaked %q: %s", raw, data)
		}
	}
	var persisted Event
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if persisted.TicketFingerprint == "" ||
		persisted.ReasonFingerprint == "" ||
		persisted.StatementFingerprint == "" ||
		persisted.FailedStatementFingerprint == "" ||
		persisted.SnapshotFingerprint == "" ||
		persisted.ErrorFingerprint == "" ||
		persisted.Target.Fingerprint == "" {
		t.Fatalf("sanitized event lacks fingerprints: %#v", persisted)
	}

	historical := Sanitize(event)
	if historical.Ticket != "" ||
		historical.Reason != "" ||
		historical.Statement != "" ||
		historical.FailedStatement != "" ||
		historical.SnapshotID != "" ||
		historical.Target.Database != "" ||
		historical.Target.Object != "" ||
		historical.Error == nil ||
		historical.Error.Message != "" {
		t.Fatalf("historical event was not sanitized: %#v", historical)
	}
}

func TestHistoricalSanitizeDropsUntrustedFingerprintsAndMutationShape(t *testing.T) {
	event := New(EventTypeDataExec, "tester", Context{}, Target{ObjectType: "data"})
	event.TicketFingerprint = "raw-ticket-in-fingerprint-field"
	event.TicketBytes = 31
	event.Target.Fingerprint = "sha256:not-hex"
	event.Target.Bytes = 7
	event.ErrorFingerprint = strings.Repeat("a", 64)
	event.ErrorBytes = 1
	event.Metadata = &MutationMetadata{
		PayloadFingerprint: "payload-secret",
		PayloadBytes:       14,
		Items:              1,
	}
	event.Outcome = &MutationOutcome{
		Status:    StatusSucceeded,
		Succeeded: 2,
	}

	sanitized := Sanitize(event)
	if sanitized.TicketFingerprint != "" || sanitized.TicketBytes != 0 ||
		sanitized.Target.Fingerprint != "" || sanitized.Target.Bytes != 0 ||
		sanitized.ErrorFingerprint != "" || sanitized.ErrorBytes != 0 {
		t.Fatalf("invalid historical fingerprints survived sanitization: %#v", sanitized)
	}
	if sanitized.Metadata != nil || sanitized.Outcome != nil {
		t.Fatalf("invalid historical mutation shape survived sanitization: %#v", sanitized)
	}

	fingerprint, size := Fingerprint("ticket", "safe")
	event = New(EventTypeDataExec, "tester", Context{}, Target{ObjectType: "data"})
	event.TicketFingerprint = fingerprint
	event.TicketBytes = size
	sanitized = Sanitize(event)
	if sanitized.TicketFingerprint != fingerprint || sanitized.TicketBytes != size {
		t.Fatalf("canonical historical fingerprint was not preserved: %#v", sanitized)
	}
}

func TestValidMutationOutcomeAcceptsUncertainItem(t *testing.T) {
	affected := int64(3)
	outcome := &MutationOutcome{
		Status:       StatusFailed,
		ErrorCode:    string(apperrors.CodeBackendError),
		Uncertain:    1,
		AffectedRows: &affected,
	}
	if !ValidMutationOutcome(outcome, 1) {
		t.Fatalf("ValidMutationOutcome(%+v, 1) = false", outcome)
	}
	outcome.Status = StatusSucceeded
	outcome.ErrorCode = ""
	if ValidMutationOutcome(outcome, 1) {
		t.Fatalf("ValidMutationOutcome(%+v, 1) = true for uncertain success", outcome)
	}
}
