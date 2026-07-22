package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/JiangHe12/opskit-core/v2/apperrors"
	coreaudit "github.com/JiangHe12/opskit-core/v2/audit"

	dbgaudit "github.com/JiangHe12/dbgov-cli/internal/audit"
	"github.com/JiangHe12/dbgov-cli/internal/backend/fake"
)

const mutationAuditConcurrencyTestTimeout = 5 * time.Second

func TestMutationAuditWritesSanitizedIntentThenOutcome(t *testing.T) {
	const (
		ticketSentinel = "TICKET-SENSITIVE-SENTINEL"
		sqlSentinel    = "UPDATE secret_table SET token='SECRET-BODY'"
		targetSentinel = "production-database"
		errorSentinel  = "BACKEND-ERROR-SENSITIVE-SENTINEL"
	)
	var records []dbgaudit.Event
	root := t.TempDir()
	secureMutationAuditTestParent(t, root)
	f := mutationAuditTestFlags(filepath.Join(root, "audit.log"))
	f.Ticket = ticketSentinel
	f.mutationAudit = &mutationAuditRuntime{
		appendRecord: func(_ string, record dbgaudit.Event, _ coreaudit.Options) error {
			records = append(records, record)
			return nil
		},
		now:    func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
		random: bytes.NewReader(bytes.Repeat([]byte{0x42}, 16)),
	}
	event := dbgaudit.New(
		dbgaudit.EventTypeDataExec,
		"tester@host",
		dbgaudit.Context{Name: "prod"},
		dbgaudit.Target{Database: targetSentinel, ObjectType: "data", Object: "exec"},
	)
	event.Statement = sqlSentinel
	event.Risk = "R2"
	metadata := mutationValueMetadata(string(dbgaudit.EventTypeDataExec), sqlSentinel)
	metadata.Items = 1
	handle, err := beginMutationAudit(f, mutationAuditSpec{
		Action:   string(dbgaudit.EventTypeDataExec),
		Event:    event,
		Metadata: metadata,
	})
	if err != nil {
		t.Fatalf("beginMutationAudit() error = %v", err)
	}
	operationErr := apperrors.New(apperrors.CodeBackendError, errorSentinel, nil)
	if err := finishMutationAudit(handle, dbgaudit.MutationOutcome{Failed: 1}, operationErr); !errors.Is(err, operationErr) {
		t.Fatalf("finishMutationAudit() error = %v, want operation error", err)
	}
	if len(records) != 2 ||
		records[0].Phase != mutationAuditPhaseIntent ||
		records[1].Phase != mutationAuditPhaseOutcome ||
		records[0].MutationID == "" ||
		records[0].MutationID != records[1].MutationID {
		t.Fatalf("mutation records = %#v, want correlated intent/outcome", records)
	}
	encoded, err := json.Marshal(records)
	if err != nil {
		t.Fatalf("Marshal(records) error = %v", err)
	}
	for _, raw := range []string{ticketSentinel, sqlSentinel, targetSentinel, errorSentinel} {
		if bytes.Contains(encoded, []byte(raw)) {
			t.Fatalf("mutation audit leaked %q: %s", raw, encoded)
		}
	}
	for _, safeField := range []string{
		`"ticketFingerprint":"sha256:`,
		`"statementFingerprint":"sha256:`,
		`"fingerprint":"sha256:`,
		`"payloadFingerprint":"sha256:`,
		`"errorCode":"BACKEND_ERROR"`,
	} {
		if !bytes.Contains(encoded, []byte(safeField)) {
			t.Fatalf("mutation audit lacks %q: %s", safeField, encoded)
		}
	}
}

func TestEmptyBatchOutcomeDoesNotInventASuccess(t *testing.T) {
	var records []dbgaudit.Event
	root := t.TempDir()
	secureMutationAuditTestParent(t, root)
	f := mutationAuditTestFlags(filepath.Join(root, "audit.log"))
	f.mutationAudit = &mutationAuditRuntime{
		appendRecord: func(_ string, record dbgaudit.Event, _ coreaudit.Options) error {
			records = append(records, record)
			return nil
		},
		now:    time.Now,
		random: bytes.NewReader(bytes.Repeat([]byte{0x23}, 16)),
	}
	handle, err := beginMutationAudit(f, mutationAuditSpec{
		Action: string(dbgaudit.EventTypeSchemaApply),
		Event:  dbgaudit.New(dbgaudit.EventTypeSchemaApply, "tester@host", dbgaudit.Context{}, dbgaudit.Target{}),
	})
	if err != nil {
		t.Fatalf("beginMutationAudit() error = %v", err)
	}
	if err := finishBatchMutationAudit(handle, 0, 0, nil); err != nil {
		t.Fatalf("finishBatchMutationAudit() error = %v", err)
	}
	if len(records) != 2 || records[1].Outcome == nil {
		t.Fatalf("mutation records = %#v, want intent and outcome", records)
	}
	outcome := records[1].Outcome
	if outcome.Succeeded != 0 || outcome.Failed != 0 || outcome.Skipped != 0 {
		t.Fatalf("empty batch outcome = %+v, want zero counts", *outcome)
	}
}

func TestBatchOutcomeCountsStayWithinTotalAfterLateFailure(t *testing.T) {
	var records []dbgaudit.Event
	root := t.TempDir()
	secureMutationAuditTestParent(t, root)
	f := mutationAuditTestFlags(filepath.Join(root, "audit.log"))
	f.mutationAudit = &mutationAuditRuntime{
		appendRecord: func(_ string, record dbgaudit.Event, _ coreaudit.Options) error {
			records = append(records, record)
			return nil
		},
		now:    time.Now,
		random: bytes.NewReader(bytes.Repeat([]byte{0x25}, 16)),
	}
	handle, err := beginMutationAudit(f, mutationAuditSpec{
		Action:   string(dbgaudit.EventTypeCredentialMigrate),
		Event:    dbgaudit.New(dbgaudit.EventTypeCredentialMigrate, "tester@host", dbgaudit.Context{}, dbgaudit.Target{}),
		Metadata: dbgaudit.MutationMetadata{Items: 2},
	})
	if err != nil {
		t.Fatalf("beginMutationAudit() error = %v", err)
	}
	operationErr := apperrors.New(apperrors.CodeLocalIOError, "late config persistence failure", nil)
	if err := finishBatchMutationAudit(handle, 2, 2, operationErr); !errors.Is(err, operationErr) {
		t.Fatalf("finishBatchMutationAudit() error = %v, want operation error", err)
	}
	outcome := records[1].Outcome
	if outcome == nil ||
		outcome.Status != dbgaudit.StatusPartial ||
		outcome.Succeeded != 2 ||
		outcome.Failed != 0 ||
		outcome.Skipped != 0 {
		t.Fatalf("late-failure batch outcome = %+v, want bounded 2/0/0 partial outcome", outcome)
	}
}

func TestMutationIntentFailureBlocksDataExec(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	secureMutationAuditTestParent(t, home)
	backend := fake.New()
	restore := stubFakeBackend(t, backend)
	defer restore()

	f := mutationAuditTestFlags(filepath.Join(home, "secure-audit", "audit.log"))
	f.Yes = true
	f.mutationAudit = &mutationAuditRuntime{
		appendRecord: func(string, dbgaudit.Event, coreaudit.Options) error {
			return errors.New("injected intent failure")
		},
		now:    time.Now,
		random: bytes.NewReader(bytes.Repeat([]byte{0x24}, 16)),
	}
	err := runDataExec(f, dataExecOptions{
		sql:  "INSERT INTO users (name) VALUES ('blocked')",
		fake: true,
	})
	if err == nil {
		t.Fatal("runDataExec() error = nil, want intent persistence failure")
	}
	if len(backend.ExecutedDML) != 0 {
		t.Fatalf("executed DML after intent failure: %v", backend.ExecutedDML)
	}
}

func TestIntentPostCommitFailureQueuesCorrelatedSkippedOutcome(t *testing.T) {
	auditPath := secureCoreAuditPathForTest(t)
	f := mutationAuditTestFlags(auditPath)
	f.mutationAudit = &mutationAuditRuntime{
		appendRecordWithResult: func(path string, record dbgaudit.Event, options coreaudit.Options) (coreaudit.AppendResult, error) {
			result, err := dbgaudit.AppendWithResult(path, record, options)
			if err != nil {
				return result, err
			}
			return coreaudit.AppendResult{State: coreaudit.AppendCommitCommittedPostCommitError},
				errors.New("injected post-commit failure")
		},
		now:    func() time.Time { return time.Unix(1_700_000_010, 0).UTC() },
		random: bytes.NewReader(bytes.Repeat([]byte{0x28}, 16)),
	}
	_, err := beginMutationAudit(f, mutationAuditSpec{
		Action: string(dbgaudit.EventTypeSchemaApply),
		Event: dbgaudit.New(
			dbgaudit.EventTypeSchemaApply,
			"tester@host",
			dbgaudit.Context{Name: "test"},
			dbgaudit.Target{ObjectType: "schema"},
		),
		Metadata: dbgaudit.MutationMetadata{Items: 2},
	})
	if got := apperrors.AsAppError(err).Code; got != codeAuditIncomplete {
		t.Fatalf("beginMutationAudit() code = %s, want %s (err=%v)", got, codeAuditIncomplete, err)
	}

	raw, err := coreaudit.QueryRaw(auditPath, coreaudit.Filter{})
	if err != nil {
		t.Fatalf("QueryRaw() error = %v", err)
	}
	if len(raw.Records) != 1 {
		t.Fatalf("active audit records = %d, want appended intent", len(raw.Records))
	}
	var intent dbgaudit.Event
	if err := json.Unmarshal([]byte(raw.Records[0].Line), &intent); err != nil {
		t.Fatalf("Unmarshal(intent) error = %v", err)
	}
	files := mutationSpoolJSONFiles(t, mutationAuditSpoolPath(auditPath))
	if len(files) != 1 {
		t.Fatalf("spooled outcomes = %v, want one", files)
	}
	outcome, err := readMutationSpoolRecord(files[0])
	if err != nil {
		t.Fatalf("readMutationSpoolRecord() error = %v", err)
	}
	if intent.Phase != mutationAuditPhaseIntent ||
		outcome.Phase != mutationAuditPhaseOutcome ||
		intent.MutationID == "" ||
		outcome.MutationID != intent.MutationID ||
		outcome.Status != dbgaudit.StatusFailed ||
		outcome.Outcome == nil ||
		outcome.Outcome.ErrorCode != string(codeAuditIncomplete) ||
		outcome.Outcome.Executed != 0 ||
		outcome.Outcome.Succeeded != 0 ||
		outcome.Outcome.Failed != 0 ||
		outcome.Outcome.Skipped != 2 {
		t.Fatalf("intent/outcome pair = intent:%+v outcome:%+v", intent, outcome)
	}
}

func TestCommittedOutcomePostCommitErrorDoesNotSpoolDuplicate(t *testing.T) {
	root := t.TempDir()
	secureMutationAuditTestParent(t, root)
	auditPath := filepath.Join(root, "audit.log")
	f := mutationAuditTestFlags(auditPath)
	appendCalls := 0
	f.mutationAudit = &mutationAuditRuntime{
		appendRecordWithResult: func(string, dbgaudit.Event, coreaudit.Options) (coreaudit.AppendResult, error) {
			appendCalls++
			if appendCalls == 1 {
				return coreaudit.AppendResult{State: coreaudit.AppendCommitCommitted}, nil
			}
			return coreaudit.AppendResult{State: coreaudit.AppendCommitCommittedPostCommitError},
				errors.New("injected post-commit failure")
		},
		now:    time.Now,
		random: bytes.NewReader(bytes.Repeat([]byte{0x29}, 16)),
	}
	handle, err := beginMutationAudit(f, mutationAuditSpec{
		Action: string(dbgaudit.EventTypeContextDelete),
		Event: dbgaudit.New(
			dbgaudit.EventTypeContextDelete,
			"tester@host",
			dbgaudit.Context{},
			dbgaudit.Target{ObjectType: "context"},
		),
	})
	if err != nil {
		t.Fatalf("beginMutationAudit() error = %v", err)
	}
	if err := finishBatchMutationAudit(handle, 0, 0, nil); apperrors.AsAppError(err).Code != codeAuditIncomplete {
		t.Fatalf("finishBatchMutationAudit() error = %v, want AUDIT_INCOMPLETE", err)
	}
	if files := mutationSpoolJSONFiles(t, mutationAuditSpoolPath(auditPath)); len(files) != 0 {
		t.Fatalf("known-committed outcome was spooled for replay: %v", files)
	}
}

func TestIndeterminateOutcomeDoesNotSpoolDuplicate(t *testing.T) {
	root := t.TempDir()
	secureMutationAuditTestParent(t, root)
	auditPath := filepath.Join(root, "audit.log")
	f := mutationAuditTestFlags(auditPath)
	appendCalls := 0
	f.mutationAudit = &mutationAuditRuntime{
		appendRecordWithResult: func(string, dbgaudit.Event, coreaudit.Options) (coreaudit.AppendResult, error) {
			appendCalls++
			if appendCalls == 1 {
				return coreaudit.AppendResult{State: coreaudit.AppendCommitCommitted}, nil
			}
			return coreaudit.AppendResult{State: coreaudit.AppendCommitIndeterminate},
				errors.New("injected indeterminate append")
		},
		now:    time.Now,
		random: bytes.NewReader(bytes.Repeat([]byte{0x2a}, 16)),
	}
	handle, err := beginMutationAudit(f, mutationAuditSpec{
		Action: string(dbgaudit.EventTypeContextDelete),
		Event: dbgaudit.New(
			dbgaudit.EventTypeContextDelete,
			"tester@host",
			dbgaudit.Context{},
			dbgaudit.Target{ObjectType: "context"},
		),
	})
	if err != nil {
		t.Fatalf("beginMutationAudit() error = %v", err)
	}
	finishErr := finishBatchMutationAudit(handle, 0, 0, nil)
	if apperrors.AsAppError(finishErr).Code != codeAuditIncomplete {
		t.Fatalf("finishBatchMutationAudit() error = %v, want AUDIT_INCOMPLETE", finishErr)
	}
	if files := mutationSpoolJSONFiles(t, mutationAuditSpoolPath(auditPath)); len(files) != 0 {
		t.Fatalf("indeterminate outcome was spooled for replay: %v (error=%v)", files, finishErr)
	}
}

func TestIndeterminateReplayIsQuarantinedAndNotRetried(t *testing.T) {
	root := t.TempDir()
	secureMutationAuditTestParent(t, root)
	auditPath := filepath.Join(root, "audit.log")
	f := mutationAuditTestFlags(auditPath)
	pending := mutationAuditOutcomeRecord("11111111111111111111111111111111")
	if err := spoolMutationAuditOutcome(f, auditPath, pending); err != nil {
		t.Fatalf("spoolMutationAuditOutcome() error = %v", err)
	}
	appendCalls := 0
	f.mutationAudit = &mutationAuditRuntime{
		appendRecordWithResult: func(string, dbgaudit.Event, coreaudit.Options) (coreaudit.AppendResult, error) {
			appendCalls++
			return coreaudit.AppendResult{State: coreaudit.AppendCommitIndeterminate},
				errors.New("injected indeterminate replay")
		},
		now:    time.Now,
		random: bytes.NewReader(nil),
	}
	for attempt := 0; attempt < 2; attempt++ {
		err := replayMutationAuditSpool(f, auditPath)
		if got := apperrors.AsAppError(err).Code; got != codeAuditIncomplete {
			t.Fatalf("replay attempt %d code = %s, want %s (err=%v)", attempt+1, got, codeAuditIncomplete, err)
		}
	}
	if appendCalls != 1 {
		t.Fatalf("replay append calls = %d, want exactly one indeterminate attempt", appendCalls)
	}
	entries, err := os.ReadDir(mutationAuditSpoolPath(auditPath))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || !strings.HasSuffix(entries[0].Name(), mutationAuditUncertainMark) {
		t.Fatalf("spool after indeterminate replay = %v, want one quarantined marker", entries)
	}
	second := mutationAuditOutcomeRecord("22222222222222222222222222222222")
	if err := spoolMutationAuditOutcome(f, auditPath, second); err != nil {
		t.Fatalf("spool outcome behind indeterminate marker error = %v", err)
	}
	if err := replayMutationAuditSpool(f, auditPath); apperrors.AsAppError(err).Code != codeAuditIncomplete {
		t.Fatalf("replay with later pending outcome error = %v, want AUDIT_INCOMPLETE", err)
	}
	if appendCalls != 1 {
		t.Fatalf("replay append calls after later outcome = %d, want 1", appendCalls)
	}
	entries, err = os.ReadDir(mutationAuditSpoolPath(auditPath))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("spool entries after later outcome = %v, want marker plus pending record", entries)
	}
}

func TestCommittedPostCommitReplayRemovesSpoolRecord(t *testing.T) {
	root := t.TempDir()
	secureMutationAuditTestParent(t, root)
	auditPath := filepath.Join(root, "audit.log")
	f := mutationAuditTestFlags(auditPath)
	pending := mutationAuditOutcomeRecord("22222222222222222222222222222222")
	if err := spoolMutationAuditOutcome(f, auditPath, pending); err != nil {
		t.Fatalf("spoolMutationAuditOutcome() error = %v", err)
	}
	appendCalls := 0
	f.mutationAudit = &mutationAuditRuntime{
		appendRecordWithResult: func(string, dbgaudit.Event, coreaudit.Options) (coreaudit.AppendResult, error) {
			appendCalls++
			return coreaudit.AppendResult{State: coreaudit.AppendCommitCommittedPostCommitError},
				errors.New("injected post-commit replay failure")
		},
		now:    time.Now,
		random: bytes.NewReader(nil),
	}
	if err := replayMutationAuditSpool(f, auditPath); apperrors.AsAppError(err).Code != codeAuditIncomplete {
		t.Fatalf("replayMutationAuditSpool() error = %v, want %s", err, codeAuditIncomplete)
	}
	if err := replayMutationAuditSpool(f, auditPath); err != nil {
		t.Fatalf("second replayMutationAuditSpool() error = %v", err)
	}
	if appendCalls != 1 {
		t.Fatalf("replay append calls = %d, want 1", appendCalls)
	}
	if entries, err := os.ReadDir(mutationAuditSpoolPath(auditPath)); err != nil || len(entries) != 0 {
		t.Fatalf("spool after known-committed replay = %v (err=%v), want empty", entries, err)
	}
}

func TestNotCommittedReplayRemainsRetryable(t *testing.T) {
	root := t.TempDir()
	secureMutationAuditTestParent(t, root)
	auditPath := filepath.Join(root, "audit.log")
	f := mutationAuditTestFlags(auditPath)
	pending := mutationAuditOutcomeRecord("33333333333333333333333333333333")
	if err := spoolMutationAuditOutcome(f, auditPath, pending); err != nil {
		t.Fatalf("spoolMutationAuditOutcome() error = %v", err)
	}
	appendCalls := 0
	f.mutationAudit = &mutationAuditRuntime{
		appendRecordWithResult: func(string, dbgaudit.Event, coreaudit.Options) (coreaudit.AppendResult, error) {
			appendCalls++
			return coreaudit.AppendResult{State: coreaudit.AppendCommitNotCommitted},
				errors.New("injected not-committed replay failure")
		},
		now:    time.Now,
		random: bytes.NewReader(nil),
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := replayMutationAuditSpool(f, auditPath); err == nil {
			t.Fatalf("replay attempt %d error = nil, want not-committed failure", attempt+1)
		}
	}
	if appendCalls != 2 {
		t.Fatalf("replay append calls = %d, want 2 retryable attempts", appendCalls)
	}
	files := mutationSpoolJSONFiles(t, mutationAuditSpoolPath(auditPath))
	if len(files) != 1 || strings.HasSuffix(files[0], mutationAuditUncertainMark) {
		t.Fatalf("not-committed spool = %v, want one replayable JSON record", files)
	}
}

func TestMutationOutcomeFailureSpoolsSanitizedAndReplaysBeforeNextIntent(t *testing.T) {
	const (
		ticketSentinel = "TICKET-SENSITIVE-SENTINEL"
		sqlSentinel    = "DELETE FROM secret_table"
		errorSentinel  = "ERROR-SENSITIVE-SENTINEL"
	)
	auditPath := secureCoreAuditPathForTest(t)
	f := mutationAuditTestFlags(auditPath)
	f.Ticket = ticketSentinel
	appendCalls := 0
	f.mutationAudit = &mutationAuditRuntime{
		appendRecord: func(path string, record dbgaudit.Event, options coreaudit.Options) error {
			appendCalls++
			if appendCalls == 2 {
				return errors.New("injected outcome append failure")
			}
			return dbgaudit.Append(path, record, options)
		},
		now:    func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
		random: bytes.NewReader(bytes.Repeat([]byte{0x43}, 32)),
	}
	event := dbgaudit.New(
		dbgaudit.EventTypeDataExec,
		"tester@host",
		dbgaudit.Context{Name: "prod"},
		dbgaudit.Target{Database: "secret-database", ObjectType: "data", Object: "exec"},
	)
	event.Statement = sqlSentinel
	metadata := mutationValueMetadata(string(dbgaudit.EventTypeDataExec), sqlSentinel)
	metadata.Items = 1
	handle, err := beginMutationAudit(f, mutationAuditSpec{
		Action:    string(dbgaudit.EventTypeDataExec),
		Event:     event,
		Metadata:  metadata,
		AuditPath: auditPath,
	})
	if err != nil {
		t.Fatalf("beginMutationAudit() error = %v", err)
	}
	operationErr := apperrors.New(apperrors.CodeBackendError, errorSentinel, nil)
	err = finishMutationAudit(handle, dbgaudit.MutationOutcome{Failed: 1}, operationErr)
	if got := apperrors.AsAppError(err).Code; got != codeAuditIncomplete {
		t.Fatalf("finishMutationAudit() code = %s, want %s (err=%v)", got, codeAuditIncomplete, err)
	}
	assertAuditPathsExclude(t, []string{auditPath, mutationAuditSpoolPath(auditPath)}, []string{
		ticketSentinel,
		sqlSentinel,
		errorSentinel,
		"secret-database",
	})

	var replayed []dbgaudit.Event
	f.mutationAudit = &mutationAuditRuntime{
		appendRecord: func(_ string, record dbgaudit.Event, _ coreaudit.Options) error {
			replayed = append(replayed, record)
			return nil
		},
		now:    func() time.Time { return time.Unix(1_700_000_001, 0).UTC() },
		random: bytes.NewReader(bytes.Repeat([]byte{0x44}, 16)),
	}
	next, err := beginMutationAudit(f, mutationAuditSpec{
		Action:    string(dbgaudit.EventTypeContextDelete),
		Event:     dbgaudit.New(dbgaudit.EventTypeContextDelete, "tester@host", dbgaudit.Context{}, dbgaudit.Target{ObjectType: "context", Object: "next"}),
		AuditPath: auditPath,
	})
	if err != nil {
		t.Fatalf("next beginMutationAudit() error = %v", err)
	}
	if len(replayed) != 2 ||
		replayed[0].Phase != mutationAuditPhaseOutcome ||
		replayed[0].MutationID != handle.id ||
		replayed[1].Phase != mutationAuditPhaseIntent ||
		replayed[1].MutationID != next.id {
		t.Fatalf("replay order = %#v, want prior outcome then next intent", replayed)
	}
	if got := mutationSpoolJSONFiles(t, mutationAuditSpoolPath(auditPath)); len(got) != 0 {
		t.Fatalf("spool after replay = %v, want empty", got)
	}
}

func TestConcurrentMutationOutcomeSpoolingKeepsEveryRecord(t *testing.T) {
	root := t.TempDir()
	secureMutationAuditTestParent(t, root)
	auditPath := filepath.Join(root, "audit.log")
	const count = 8
	var wait sync.WaitGroup
	errorsByWriter := make([]error, count)
	for index := 0; index < count; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			f := mutationAuditTestFlags(auditPath)
			f.mutationAudit = &mutationAuditRuntime{
				appendRecord: func(string, dbgaudit.Event, coreaudit.Options) error {
					return errors.New("audit unavailable")
				},
				now:    func() time.Time { return time.Unix(300-int64(index), 0).UTC() },
				random: bytes.NewReader(nil),
			}
			record := mutationAuditOutcomeRecord(strings.Repeat(string("12345678"[index]), 32))
			errorsByWriter[index] = spoolMutationAuditOutcome(f, auditPath, record)
		}(index)
	}
	wait.Wait()
	for index, err := range errorsByWriter {
		if err != nil {
			t.Errorf("spool writer %d error = %v", index, err)
		}
	}
	files := mutationSpoolJSONFiles(t, mutationAuditSpoolPath(auditPath))
	if len(files) != count {
		t.Fatalf("spooled JSON files = %d, want %d: %v", len(files), count, files)
	}
}

func TestMutationOutcomeFallbackPrecedesConcurrentIntent(t *testing.T) {
	root := t.TempDir()
	secureMutationAuditTestParent(t, root)
	auditPath := filepath.Join(root, "audit.log")
	const priorID = "11111111111111111111111111111111"

	outcomeAppendStarted := make(chan struct{})
	releaseOutcomeAppend := make(chan struct{})
	nextIntentAppended := make(chan struct{})
	var callsMu sync.Mutex
	var calls []string
	firstOutcomeAttempt := true
	runtime := &mutationAuditRuntime{
		appendRecord: func(_ string, record dbgaudit.Event, _ coreaudit.Options) error {
			callsMu.Lock()
			calls = append(calls, record.MutationID+"/"+record.Phase)
			blockOutcome := record.MutationID == priorID &&
				record.Phase == mutationAuditPhaseOutcome &&
				firstOutcomeAttempt
			if blockOutcome {
				firstOutcomeAttempt = false
			}
			callsMu.Unlock()
			if blockOutcome {
				close(outcomeAppendStarted)
				<-releaseOutcomeAppend
				return errors.New("injected outcome append failure")
			}
			if record.MutationID != priorID && record.Phase == mutationAuditPhaseIntent {
				close(nextIntentAppended)
			}
			return nil
		},
		now:    func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
		random: bytes.NewReader(bytes.Repeat([]byte{0x22}, 16)),
	}
	f := mutationAuditTestFlags(auditPath)
	f.mutationAudit = runtime
	prior := &mutationAuditHandle{
		f:    f,
		id:   priorID,
		path: auditPath,
		spec: mutationAuditSpec{
			Action: string(dbgaudit.EventTypeDataExec),
			Event: dbgaudit.New(
				dbgaudit.EventTypeDataExec,
				"tester@host",
				dbgaudit.Context{Name: "test"},
				dbgaudit.Target{ObjectType: "data"},
			),
			Metadata: dbgaudit.MutationMetadata{Items: 1},
		},
	}

	finishDone := make(chan error, 1)
	go func() {
		finishDone <- finishMutationAudit(prior, dbgaudit.MutationOutcome{Succeeded: 1}, nil)
	}()
	var releaseOutcomeAppendOnce sync.Once
	releaseBlockedOutcomeAppend := func() {
		releaseOutcomeAppendOnce.Do(func() { close(releaseOutcomeAppend) })
	}
	t.Cleanup(releaseBlockedOutcomeAppend)
	select {
	case <-outcomeAppendStarted:
	case err := <-finishDone:
		t.Fatalf("finishMutationAudit() completed before reaching the blocked append: %v", err)
	case <-time.After(mutationAuditConcurrencyTestTimeout):
		t.Fatal("timed out waiting for the blocked mutation outcome append")
	}

	beginDone := make(chan error, 1)
	go func() {
		_, err := beginMutationAudit(f, mutationAuditSpec{
			Action: string(dbgaudit.EventTypeContextDelete),
			Event: dbgaudit.New(
				dbgaudit.EventTypeContextDelete,
				"tester@host",
				dbgaudit.Context{Name: "test"},
				dbgaudit.Target{ObjectType: "context"},
			),
		})
		beginDone <- err
	}()

	intentRanEarly := false
	select {
	case <-nextIntentAppended:
		intentRanEarly = true
	case <-time.After(150 * time.Millisecond):
	}
	releaseBlockedOutcomeAppend()

	select {
	case err := <-finishDone:
		if apperrors.AsAppError(err).Code != codeAuditIncomplete {
			t.Fatalf("finishMutationAudit() error = %v, want %s", err, codeAuditIncomplete)
		}
	case <-time.After(mutationAuditConcurrencyTestTimeout):
		t.Fatal("timed out waiting for the blocked mutation outcome to finish")
	}
	select {
	case err := <-beginDone:
		if err != nil {
			t.Fatalf("beginMutationAudit() error = %v", err)
		}
	case <-time.After(mutationAuditConcurrencyTestTimeout):
		t.Fatal("timed out waiting for the concurrent mutation intent")
	}
	if intentRanEarly {
		t.Fatal("concurrent intent appended before the prior failed outcome was durably spooled")
	}
	callsMu.Lock()
	gotCalls := append([]string(nil), calls...)
	callsMu.Unlock()
	if len(gotCalls) != 3 ||
		gotCalls[0] != priorID+"/"+mutationAuditPhaseOutcome ||
		gotCalls[1] != priorID+"/"+mutationAuditPhaseOutcome ||
		!strings.HasSuffix(gotCalls[2], "/"+mutationAuditPhaseIntent) {
		t.Fatalf("append order = %v, want failed outcome, replayed outcome, then next intent", gotCalls)
	}
}

func TestAlreadyStartedHandleReplaysPendingOutcomeBeforeItsOwnOutcome(t *testing.T) {
	root := t.TempDir()
	secureMutationAuditTestParent(t, root)
	auditPath := filepath.Join(root, "audit.log")
	var recordsMu sync.Mutex
	var records []dbgaudit.Event
	firstOutcomeFails := true
	firstID := ""
	random := append(bytes.Repeat([]byte{0x31}, 16), bytes.Repeat([]byte{0x32}, 16)...)
	f := mutationAuditTestFlags(auditPath)
	f.mutationAudit = &mutationAuditRuntime{
		appendRecord: func(_ string, record dbgaudit.Event, _ coreaudit.Options) error {
			recordsMu.Lock()
			defer recordsMu.Unlock()
			if firstOutcomeFails &&
				record.MutationID == firstID &&
				record.Phase == mutationAuditPhaseOutcome {
				firstOutcomeFails = false
				return errors.New("injected first outcome append failure")
			}
			records = append(records, record)
			return nil
		},
		now:    func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
		random: bytes.NewReader(random),
	}
	newSpec := func(eventType dbgaudit.EventType) mutationAuditSpec {
		return mutationAuditSpec{
			Action: string(eventType),
			Event: dbgaudit.New(
				eventType,
				"tester@host",
				dbgaudit.Context{Name: "test"},
				dbgaudit.Target{ObjectType: "data"},
			),
			Metadata: dbgaudit.MutationMetadata{Items: 1},
		}
	}
	first, err := beginMutationAudit(f, newSpec(dbgaudit.EventTypeDataExec))
	if err != nil {
		t.Fatalf("beginMutationAudit(first) error = %v", err)
	}
	firstID = first.id
	second, err := beginMutationAudit(f, newSpec(dbgaudit.EventTypeSchemaApply))
	if err != nil {
		t.Fatalf("beginMutationAudit(second) error = %v", err)
	}
	if err := finishMutationAudit(first, dbgaudit.MutationOutcome{Succeeded: 1}, nil); apperrors.AsAppError(err).Code != codeAuditIncomplete {
		t.Fatalf("finishMutationAudit(first) error = %v, want %s", err, codeAuditIncomplete)
	}
	if err := finishMutationAudit(second, dbgaudit.MutationOutcome{Succeeded: 1}, nil); err != nil {
		t.Fatalf("finishMutationAudit(second) error = %v", err)
	}

	recordsMu.Lock()
	got := append([]dbgaudit.Event(nil), records...)
	recordsMu.Unlock()
	if len(got) != 4 ||
		got[0].MutationID != first.id || got[0].Phase != mutationAuditPhaseIntent ||
		got[1].MutationID != second.id || got[1].Phase != mutationAuditPhaseIntent ||
		got[2].MutationID != first.id || got[2].Phase != mutationAuditPhaseOutcome ||
		got[3].MutationID != second.id || got[3].Phase != mutationAuditPhaseOutcome {
		t.Fatalf("successful append order = %#v, want first intent, second intent, replayed first outcome, second outcome", got)
	}
	if files := mutationSpoolJSONFiles(t, mutationAuditSpoolPath(auditPath)); len(files) != 0 {
		t.Fatalf("spool after second outcome = %v, want empty", files)
	}
}

func TestPendingMutationOutcomeReplaysBeforeOrdinaryR0Audit(t *testing.T) {
	root := t.TempDir()
	secureMutationAuditTestParent(t, root)
	auditPath := filepath.Join(root, "audit.log")
	var records []dbgaudit.Event
	f := mutationAuditTestFlags(auditPath)
	f.mutationAudit = &mutationAuditRuntime{
		appendRecord: func(_ string, record dbgaudit.Event, _ coreaudit.Options) error {
			records = append(records, record)
			return nil
		},
		now:    func() time.Time { return time.Unix(1_700_000_100, 0).UTC() },
		random: bytes.NewReader(nil),
	}
	pending := mutationAuditOutcomeRecord("11111111111111111111111111111111")
	if err := spoolMutationAuditOutcome(f, auditPath, pending); err != nil {
		t.Fatalf("spoolMutationAuditOutcome() error = %v", err)
	}
	event := dbgaudit.New(
		dbgaudit.EventTypeQuery,
		"tester@host",
		dbgaudit.Context{Name: "test"},
		dbgaudit.Target{ObjectType: "data"},
	)
	if err := appendQueuedAuditEvent(f, auditPath, event); err != nil {
		t.Fatalf("appendQueuedAuditEvent() error = %v", err)
	}
	if len(records) != 2 ||
		records[0].MutationID != pending.MutationID ||
		records[0].Phase != mutationAuditPhaseOutcome ||
		records[1].EventType != dbgaudit.EventTypeQuery ||
		records[1].Phase != "" {
		t.Fatalf("append order = %#v, want pending outcome then ordinary R0 audit", records)
	}
	if files := mutationSpoolJSONFiles(t, mutationAuditSpoolPath(auditPath)); len(files) != 0 {
		t.Fatalf("spool after ordinary audit = %v, want empty", files)
	}
}

func TestAuditTimestampIsAssignedInsideAppendOrderLock(t *testing.T) {
	root := t.TempDir()
	secureMutationAuditTestParent(t, root)
	auditPath := filepath.Join(root, "audit.log")
	firstNowStarted := make(chan struct{})
	releaseFirstNow := make(chan struct{})
	secondAppended := make(chan struct{})
	var nowMu sync.Mutex
	nowCalls := 0
	var recordsMu sync.Mutex
	var records []dbgaudit.Event
	base := time.Unix(1_700_000_200, 0).UTC()
	f := mutationAuditTestFlags(auditPath)
	f.mutationAudit = &mutationAuditRuntime{
		appendRecord: func(_ string, record dbgaudit.Event, _ coreaudit.Options) error {
			recordsMu.Lock()
			records = append(records, record)
			recordsMu.Unlock()
			if record.EventType == dbgaudit.EventTypeExplain {
				close(secondAppended)
			}
			return nil
		},
		now: func() time.Time {
			nowMu.Lock()
			nowCalls++
			call := nowCalls
			nowMu.Unlock()
			if call == 1 {
				close(firstNowStarted)
				<-releaseFirstNow
			}
			return base.Add(time.Duration(call) * time.Second)
		},
		random: bytes.NewReader(nil),
	}
	appendEvent := func(eventType dbgaudit.EventType) error {
		return appendQueuedAuditEvent(f, auditPath, dbgaudit.New(
			eventType,
			"tester@host",
			dbgaudit.Context{Name: "test"},
			dbgaudit.Target{ObjectType: "data"},
		))
	}
	firstDone := make(chan error, 1)
	go func() { firstDone <- appendEvent(dbgaudit.EventTypeQuery) }()
	var releaseFirstNowOnce sync.Once
	releaseBlockedTimestamp := func() {
		releaseFirstNowOnce.Do(func() { close(releaseFirstNow) })
	}
	t.Cleanup(releaseBlockedTimestamp)
	select {
	case <-firstNowStarted:
	case err := <-firstDone:
		t.Fatalf("first append completed before reaching the blocked timestamp: %v", err)
	case <-time.After(mutationAuditConcurrencyTestTimeout):
		t.Fatal("timed out waiting for the blocked audit timestamp assignment")
	}
	secondDone := make(chan error, 1)
	go func() { secondDone <- appendEvent(dbgaudit.EventTypeExplain) }()

	appendedBeforeRelease := false
	select {
	case <-secondAppended:
		appendedBeforeRelease = true
	case <-time.After(150 * time.Millisecond):
	}
	releaseBlockedTimestamp()
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatalf("first append error = %v", err)
		}
	case <-time.After(mutationAuditConcurrencyTestTimeout):
		t.Fatal("timed out waiting for the first audit append")
	}
	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatalf("second append error = %v", err)
		}
	case <-time.After(mutationAuditConcurrencyTestTimeout):
		t.Fatal("timed out waiting for the second audit append")
	}
	if appendedBeforeRelease {
		t.Fatal("second audit appended while the first append-order lock was assigning its timestamp")
	}
	recordsMu.Lock()
	got := append([]dbgaudit.Event(nil), records...)
	recordsMu.Unlock()
	if len(got) != 2 ||
		got[0].EventType != dbgaudit.EventTypeQuery ||
		got[1].EventType != dbgaudit.EventTypeExplain ||
		!got[0].Timestamp.Before(got[1].Timestamp) {
		t.Fatalf("audit records = %#v, want append-order timestamps", got)
	}
}

func TestMutationReplayRejectsUnexpectedEntryBeforeIntent(t *testing.T) {
	root := t.TempDir()
	secureMutationAuditTestParent(t, root)
	auditPath := filepath.Join(root, "audit.log")
	spoolPath := mutationAuditSpoolPath(auditPath)
	if err := ensureMutationSpoolDirectory(spoolPath); err != nil {
		t.Fatalf("ensureMutationSpoolDirectory() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(spoolPath, "unexpected"), []byte("unsafe"), 0o600); err != nil {
		t.Fatalf("WriteFile(unexpected) error = %v", err)
	}
	appendCalls := 0
	f := mutationAuditTestFlags(auditPath)
	f.mutationAudit = &mutationAuditRuntime{
		appendRecord: func(string, dbgaudit.Event, coreaudit.Options) error {
			appendCalls++
			return nil
		},
		now:    time.Now,
		random: bytes.NewReader(bytes.Repeat([]byte{0x11}, 16)),
	}
	_, err := beginMutationAudit(f, mutationAuditSpec{
		Action: string(dbgaudit.EventTypeContextSet),
		Event:  dbgaudit.New(dbgaudit.EventTypeContextSet, "tester@host", dbgaudit.Context{}, dbgaudit.Target{}),
	})
	if got := apperrors.AsAppError(err).Code; got != codeAuditIncomplete {
		t.Fatalf("beginMutationAudit() code = %s, want %s (err=%v)", got, codeAuditIncomplete, err)
	}
	if appendCalls != 0 {
		t.Fatalf("append calls = %d, want 0", appendCalls)
	}
}

func TestInstallIntentFailureCreatesNoTarget(t *testing.T) {
	target := filepath.Join(t.TempDir(), "skills")
	previousFS := skillFS
	skillFS = fstest.MapFS{
		"skills/dbgov-cli/SKILL.md": {Data: []byte("test skill")},
	}
	t.Cleanup(func() { skillFS = previousFS })
	f := mutationAuditTestFlags(filepath.Join(t.TempDir(), "secure-audit", "audit.log"))
	f.Yes = true
	f.mutationAudit = &mutationAuditRuntime{
		appendRecord: func(string, dbgaudit.Event, coreaudit.Options) error {
			return errors.New("audit unavailable")
		},
		now:    time.Now,
		random: bytes.NewReader(bytes.Repeat([]byte{0x45}, 16)),
	}
	if err := installSkills(f, target); err == nil {
		t.Fatal("installSkills() error = nil, want audit failure")
	}
	if _, err := os.Stat(filepath.Join(target, "dbgov-cli")); !os.IsNotExist(err) {
		t.Fatalf("install target exists after intent failure: %v", err)
	}
}

func TestMutationSpoolRejectsTamperedSchema(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*dbgaudit.Event)
	}{
		{
			name: "non canonical fingerprint",
			mutate: func(record *dbgaudit.Event) {
				record.TicketFingerprint = "ticket-secret"
				record.TicketBytes = 13
			},
		},
		{
			name: "outcome count exceeds intent",
			mutate: func(record *dbgaudit.Event) {
				record.Outcome.Succeeded = 2
			},
		},
		{
			name: "event type differs from action",
			mutate: func(record *dbgaudit.Event) {
				record.EventType = dbgaudit.EventTypeContextDelete
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			secureMutationAuditTestParent(t, root)
			spoolPath := filepath.Join(root, "spool")
			if err := ensureMutationSpoolDirectory(spoolPath); err != nil {
				t.Fatal(err)
			}
			const mutationID = "11111111111111111111111111111111"
			record := mutationAuditOutcomeRecord(mutationID)
			tt.mutate(&record)
			data, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(spoolPath, "00000000000000000001-"+mutationID+".json")
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := secureMutationSpoolFile(path); err != nil {
				t.Fatal(err)
			}
			if _, err := readMutationSpoolRecord(path); apperrors.AsAppError(err).Code != apperrors.CodeLocalIOError {
				t.Fatalf("tampered spool error = %v, want LOCAL_IO_ERROR", err)
			}
		})
	}
}

func TestMutationSpoolRejectsFilenameIdentityMismatch(t *testing.T) {
	root := t.TempDir()
	secureMutationAuditTestParent(t, root)
	spoolPath := filepath.Join(root, "spool")
	if err := ensureMutationSpoolDirectory(spoolPath); err != nil {
		t.Fatal(err)
	}
	record := mutationAuditOutcomeRecord("11111111111111111111111111111111")
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(spoolPath, "00000000000000000001-22222222222222222222222222222222.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := secureMutationSpoolFile(path); err != nil {
		t.Fatal(err)
	}
	if _, err := readMutationSpoolRecord(path); apperrors.AsAppError(err).Code != apperrors.CodeLocalIOError {
		t.Fatalf("mismatched spool identity error = %v, want LOCAL_IO_ERROR", err)
	}
}

func TestMutationEntryPointCoverage(t *testing.T) {
	checks := map[string][]string{
		"schema.go":       {"EventTypeSchemaApply", "EventTypeSchemaDump"},
		"data.go":         {"EventTypeDataExec"},
		"gitops.go":       {"EventTypeExport", "EventTypeImport", "EventTypeReconcile"},
		"rollback.go":     {"EventTypeRollback"},
		"ctx.go":          {"EventTypeContextSet", "EventTypeContextUse", "EventTypeContextDelete"},
		"ctx_portable.go": {"EventTypeContextImport"},
		"ctx_role.go":     {"EventTypeRoleAssign", "EventTypeRoleRevoke"},
		"ctx_migrate.go":  {"EventTypeCredentialMigrate"},
		"audit_prune.go":  {"EventTypeAuditPrune"},
		"install.go":      {"EventTypeInstallSkill"},
	}
	for file, eventTypes := range checks {
		data, err := os.ReadFile(file) //nolint:gosec // Test reads repository source files.
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", file, err)
		}
		source := string(data)
		for _, eventType := range eventTypes {
			if !strings.Contains(source, eventType) {
				t.Errorf("%s lacks %s mutation audit wiring", file, eventType)
				continue
			}
			if !hasMutationAuditAction(source, eventType) {
				t.Errorf("%s %s lacks nearby mutation Action", file, eventType)
			}
		}
	}
}

func hasMutationAuditAction(source, eventType string) bool {
	const actionField = "Action:"
	for offset := 0; offset < len(source); {
		index := strings.Index(source[offset:], actionField)
		if index < 0 {
			return false
		}
		index += offset
		windowEnd := index + 160
		if windowEnd > len(source) {
			windowEnd = len(source)
		}
		if strings.Contains(source[index:windowEnd], eventType) {
			return true
		}
		offset = index + len(actionField)
	}
	return false
}

func mutationAuditTestFlags(auditPath string) *cliFlags {
	return &cliFlags{
		Output:            "json",
		Out:               io.Discard,
		Err:               io.Discard,
		NonInteractive:    true,
		trustedOperator:   "tester@host",
		mutationAuditPath: auditPath,
	}
}

func mutationAuditOutcomeRecord(id string) dbgaudit.Event {
	outcome := &dbgaudit.MutationOutcome{Status: dbgaudit.StatusSucceeded, Succeeded: 1}
	return dbgaudit.Event{
		APIVersion: mutationAuditAPIVersion,
		Kind:       mutationAuditKind,
		Timestamp:  time.Unix(1_700_000_000, 0).UTC(),
		EventType:  dbgaudit.EventTypeDataExec,
		Operator:   "tester@host",
		Context:    dbgaudit.Context{Name: "test"},
		Target:     dbgaudit.Target{ObjectType: "data"},
		Risk:       "R1",
		Status:     dbgaudit.StatusSucceeded,
		MutationID: id,
		Phase:      mutationAuditPhaseOutcome,
		Action:     string(dbgaudit.EventTypeDataExec),
		Metadata:   &dbgaudit.MutationMetadata{Items: 1},
		Outcome:    outcome,
	}
}

func mutationSpoolJSONFiles(t *testing.T, spoolPath string) []string {
	t.Helper()
	entries, err := os.ReadDir(spoolPath)
	if err != nil {
		t.Fatalf("ReadDir(%s) error = %v", spoolPath, err)
	}
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".json") {
			files = append(files, filepath.Join(spoolPath, entry.Name()))
		}
	}
	return files
}

func assertAuditPathsExclude(t *testing.T, roots, sentinels []string) {
	t.Helper()
	for _, root := range roots {
		info, err := os.Stat(root)
		if err != nil {
			t.Fatalf("Stat(%s) error = %v", root, err)
		}
		paths := []string{root}
		if info.IsDir() {
			paths = nil
			err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
				if walkErr == nil && !entry.IsDir() {
					paths = append(paths, path)
				}
				return walkErr
			})
			if err != nil {
				t.Fatalf("WalkDir(%s) error = %v", root, err)
			}
		}
		for _, path := range paths {
			data, err := os.ReadFile(path) //nolint:gosec // Test reads files under t.TempDir.
			if err != nil {
				t.Fatalf("ReadFile(%s) error = %v", path, err)
			}
			for _, sentinel := range sentinels {
				if bytes.Contains(data, []byte(sentinel)) {
					t.Fatalf("sensitive sentinel %q leaked into %s", sentinel, path)
				}
			}
		}
	}
}

func exerciseMutationAuditPath(t *testing.T, auditPath string) {
	t.Helper()
	f := mutationAuditTestFlags(auditPath)
	f.mutationAudit = &mutationAuditRuntime{
		appendRecord: func(string, dbgaudit.Event, coreaudit.Options) error { return nil },
		now:          time.Now,
		random:       bytes.NewReader(bytes.Repeat([]byte{0x27}, 16)),
	}
	handle, err := beginMutationAudit(f, mutationAuditSpec{
		Action: string(dbgaudit.EventTypeContextSet),
		Event:  dbgaudit.New(dbgaudit.EventTypeContextSet, "tester@host", dbgaudit.Context{}, dbgaudit.Target{}),
		Metadata: dbgaudit.MutationMetadata{
			Items: 1,
		},
	})
	if err != nil {
		t.Fatalf("beginMutationAudit() error = %v", err)
	}
	if err := finishBatchMutationAudit(handle, 1, 1, nil); err != nil {
		t.Fatalf("finishBatchMutationAudit() error = %v", err)
	}
}

func secureCoreAuditPathForTest(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".dbgov-core-audit-tests", filepath.Base(t.TempDir()))
	if err := ensurePrivateMutationDirectory(dir); err != nil {
		t.Fatalf("ensurePrivateMutationDirectory() error = %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "audit.log")
}
