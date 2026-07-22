package cmd

import (
	"bytes"
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/JiangHe12/opskit-core/v2/apperrors"
	coreaudit "github.com/JiangHe12/opskit-core/v2/audit"
	corectx "github.com/JiangHe12/opskit-core/v2/ctx"
	"github.com/JiangHe12/opskit-core/v2/lockfile"

	dbgaudit "github.com/JiangHe12/dbgov-cli/internal/audit"
	"github.com/JiangHe12/dbgov-cli/internal/dbgovctx"
	"github.com/JiangHe12/dbgov-cli/internal/safety"
)

const maxAuditEvidenceLineLen = 4 * 1024 * 1024

func TestStrictAuditVerifyCoversAuthenticatedIntegrityProblems(t *testing.T) {
	tests := []coreaudit.VerifyResult{
		{IntegrityErrors: 1},
		{SequenceViolations: 1},
		{CheckpointViolations: 1},
		{TruncationDetected: true},
	}
	for _, result := range tests {
		if err := strictVerifyError(result, true); apperrors.AsAppError(err).Code != apperrors.CodeValidationFailed {
			t.Fatalf("strictVerifyError(%+v) = %v, want VALIDATION_FAILED", result, err)
		}
	}
}

func TestAuditVerifyTableReportsAuthenticatedIntegrityFields(t *testing.T) {
	var out bytes.Buffer
	err := printAuditVerify(&cliFlags{Output: "table", Out: &out}, coreaudit.VerifyResult{
		Authenticated:         4,
		LegacyUnauthenticated: 5,
		EncryptedOpaque:       6,
		IntegrityErrors:       1,
		SequenceViolations:    2,
		CheckpointViolations:  3,
		TruncationDetected:    true,
		Lock:                  coreaudit.VerifyLockStatus{Present: true},
	})
	if err != nil {
		t.Fatalf("printAuditVerify() error = %v", err)
	}
	for _, want := range []string{
		"AUTHENTICATED",
		"LEGACY_UNAUTHENTICATED",
		"ENCRYPTED_OPAQUE",
		"INTEGRITY_ERRORS",
		"SEQUENCE_VIOLATIONS",
		"CHECKPOINT_VIOLATIONS",
		"TRUNCATION_DETECTED",
		"LOCK_PRESENT",
		"true",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("audit verify output missing %q: %s", want, out.String())
		}
	}
}

func TestAuditControlRotationDoesNotEnterTargetNamespace(t *testing.T) {
	target := secureCoreAuditPathForTest(t)
	control := auditControlPath(target)
	for index := 0; index < 2; index++ {
		event := dbgaudit.New(
			dbgaudit.EventTypeAuditPrune,
			"tester@host",
			dbgaudit.Context{},
			dbgaudit.Target{ObjectType: "audit"},
		)
		event.Timestamp = time.Unix(int64(index+1), 0).UTC()
		if err := dbgaudit.Append(control, event, coreaudit.Options{MaxSizeBytes: 1}); err != nil {
			t.Fatalf("append control record %d: %v", index, err)
		}
	}
	controlRotations, err := coreaudit.RotatedFiles(control)
	if err != nil || len(controlRotations) == 0 {
		t.Fatalf("control rotations = %v, error = %v; want at least one", controlRotations, err)
	}
	targetRotations, err := coreaudit.RotatedFiles(target)
	if err != nil {
		t.Fatal(err)
	}
	if len(targetRotations) != 0 {
		t.Fatalf("target rotations include control evidence: %v", targetRotations)
	}
}

func TestAuditPruneRequiresExactR3Authorization(t *testing.T) {
	tests := []struct {
		name    string
		auth    []string
		wantErr apperrors.ErrorCode
		deleted bool
	}{
		{
			name:    "missing ticket",
			auth:    []string{"--yes", "--allow-audit-prune"},
			wantErr: apperrors.CodeAuthorizationRequired,
		},
		{
			name:    "wrong allow flag",
			auth:    []string{"--yes", "--ticket", "TEST-1", "--allow-role-change"},
			wantErr: apperrors.CodeAuthorizationRequired,
		},
		{
			name:    "exact allow flag",
			auth:    []string{"--yes", "--ticket", "TEST-1", "--allow-audit-prune"},
			deleted: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			path, rotated := writeAuditPruneFixture(t, home)
			args := append([]string{"-o", "json"}, tt.auth...)
			args = append(args, "audit", "prune", "--path", path, "--keep-last", "0", "--confirm")
			_, err := executeCommandForTest(args...)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("authorized prune error = %v", err)
				}
			} else if got := apperrors.AsAppError(err).Code; got != tt.wantErr {
				t.Fatalf("error code = %q, want %q (err=%v)", got, tt.wantErr, err)
			}
			_, statErr := os.Stat(rotated)
			if tt.deleted {
				if !os.IsNotExist(statErr) {
					t.Fatalf("authorized prune kept %s: %v", rotated, statErr)
				}
			} else if statErr != nil {
				t.Fatalf("denied prune changed %s: %v", rotated, statErr)
			}
		})
	}
}

func TestAuditPruneIgnoresSpoofedOperatorAndContextOverride(t *testing.T) {
	operator, err := resolveTrustedOperator()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	configPath := filepath.Join(home, "config.yaml")
	dbgovctx.SetConfigPath(configPath)
	t.Cleanup(func() { dbgovctx.SetConfigPath("") })
	if err := dbgovctx.SetContext("guard", dbgovctx.Context{
		Base: corectx.Base{Roles: map[string]string{
			operator:        safety.RoleReader,
			"spoofed-admin": safety.RoleAdmin,
		}},
		Engine: "mysql",
	}); err != nil {
		t.Fatal(err)
	}
	if err := dbgovctx.SetContext("override", dbgovctx.Context{
		Base:   corectx.Base{Roles: map[string]string{operator: safety.RoleAdmin}},
		Engine: "mysql",
	}); err != nil {
		t.Fatal(err)
	}
	if err := dbgovctx.UseContext("guard"); err != nil {
		t.Fatal(err)
	}
	path, rotated := writeAuditPruneFixture(t, home)
	t.Setenv("DBGOV_OPERATOR", "spoofed-admin")
	_, err = executeCommandForTest(
		"--config", configPath,
		"--context", "override",
		"--operator", "spoofed-admin",
		"--yes", "--ticket", "TEST-1", "--allow-audit-prune",
		"audit", "prune", "--path", path, "--keep-last", "0", "--confirm",
	)
	if apperrors.AsAppError(err).Code != apperrors.CodeAuthorizationRequired {
		t.Fatalf("spoofed prune error = %v, want authorization required", err)
	}
	if _, err := os.Stat(rotated); err != nil {
		t.Fatalf("spoofed prune changed evidence: %v", err)
	}
}

func TestAuditPruneDryRunReturnsBeforeAuthorization(t *testing.T) {
	oldUser, oldHost := currentOSUser, currentHost
	currentOSUser = func() (*user.User, error) {
		return &user.User{Username: "trusted"}, nil
	}
	currentHost = func() (string, error) { return "host", nil }
	t.Cleanup(func() {
		currentOSUser = oldUser
		currentHost = oldHost
	})

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	configPath := filepath.Join(home, "config.yaml")
	dbgovctx.SetConfigPath(configPath)
	t.Cleanup(func() { dbgovctx.SetConfigPath("") })
	if err := dbgovctx.SetContext("guard", dbgovctx.Context{
		Base:   corectx.Base{Roles: map[string]string{"trusted@host": safety.RoleReader}},
		Engine: "mysql",
	}); err != nil {
		t.Fatal(err)
	}
	if err := dbgovctx.UseContext("guard"); err != nil {
		t.Fatal(err)
	}
	path, rotated := writeAuditPruneFixture(t, home)
	beforeActive, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out, err := executeCommandForTest(
		"--config", configPath, "-o", "json",
		"audit", "prune", "--path", path, "--keep-last", "0", "--confirm", "--dry-run",
	)
	if err != nil {
		t.Fatalf("dry-run error = %v; out=%s", err, out)
	}
	if !strings.Contains(out, `"dryRun": true`) || !strings.Contains(out, filepath.Base(rotated)) {
		t.Fatalf("dry-run output = %s", out)
	}
	if _, err := os.Stat(rotated); err != nil {
		t.Fatalf("dry-run changed evidence: %v", err)
	}
	afterActive, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(afterActive, beforeActive) {
		t.Fatalf("dry-run wrote active target: before=%q after=%q", beforeActive, afterActive)
	}
}

func TestAuditPruneRejectsAuthenticatedAuditV2Evidence(t *testing.T) {
	const envelope = ` { "kind": "AuditEnvelope", "apiVersion": "opskit-core.io/audit/v2" }` + "\n"
	tests := []struct {
		name       string
		checkpoint bool
		activeV2   bool
		rotatedV2  bool
	}{
		{name: "checkpoint", checkpoint: true},
		{name: "active envelope", activeV2: true},
		{name: "rotated envelope", rotatedV2: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			path, rotated := writeAuditPruneFixture(t, home)
			if tt.activeV2 {
				if err := os.WriteFile(path, []byte(envelope), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if tt.rotatedV2 {
				if err := os.WriteFile(rotated, []byte(envelope), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if tt.checkpoint {
				if err := os.WriteFile(path+".checkpoint", []byte("{}"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			beforeActive, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			beforeRotated, err := os.ReadFile(rotated)
			if err != nil {
				t.Fatal(err)
			}
			_, err = executeCommandForTest(
				"-o", "json", "--yes", "--ticket", "TEST-1", "--allow-audit-prune",
				"audit", "prune", "--path", path, "--keep-last", "0", "--confirm",
			)
			if err == nil {
				t.Fatal("invalid v2 evidence was pruned")
			}
			afterActive, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			afterRotated, readErr := os.ReadFile(rotated)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if !bytes.Equal(afterActive, beforeActive) {
				t.Fatal("failed prune changed the target active log")
			}
			if !bytes.Equal(afterRotated, beforeRotated) {
				t.Fatal("invalid v2 prune changed the rotated candidate")
			}
		})
	}
}

func TestAuditV2AmbiguityFailsClosedWithoutChangingEvidence(t *testing.T) {
	tests := []struct {
		name     string
		content  []byte
		wantCode apperrors.ErrorCode
	}{
		{
			name: "duplicate identity markers",
			content: []byte(
				`{"apiVersion":"opskit-core.io/audit/v2","kind":"AuditEnvelope",` +
					`"apiVersion":"dbgov.io/audit/v1","kind":"AuditEvent"}` + "\n",
			),
			wantCode: apperrors.CodeNotImplemented,
		},
		{
			name:     "duplicate unrelated top level key",
			content:  []byte(`{"value":1,"value":2}` + "\n"),
			wantCode: apperrors.CodeNotImplemented,
		},
		{
			name:     "api version marker only",
			content:  []byte(`{"apiVersion":"opskit-core.io/audit/v2","kind":"AuditEvent"}` + "\n"),
			wantCode: apperrors.CodeNotImplemented,
		},
		{
			name:     "kind marker only",
			content:  []byte(`{"apiVersion":"dbgov.io/audit/v1","kind":"AuditEnvelope"}` + "\n"),
			wantCode: apperrors.CodeNotImplemented,
		},
		{
			name:     "unicode escaped identity key",
			content:  []byte(`{"\u0061piVersion":"opskit-core.io/audit/v2"}` + "\n"),
			wantCode: apperrors.CodeNotImplemented,
		},
		{
			name:     "partial authenticated shape",
			content:  []byte(`{"keyId":"k"}` + "\n"),
			wantCode: apperrors.CodeNotImplemented,
		},
		{
			name: "authenticated shape with changed identity",
			content: []byte(
				`{"apiVersion":"attacker.invalid/audit/v1","kind":"LegacyEvent","keyId":"k",` +
					`"sequence":1,"payloadEncoding":"json","payload":{},"mac":"x"}` + "\n",
			),
			wantCode: apperrors.CodeNotImplemented,
		},
		{
			name:     "malformed line with fixed marker",
			content:  []byte(`{"kind":"AuditEnvelope"` + "\n"),
			wantCode: apperrors.CodeNotImplemented,
		},
		{
			name:     "malformed api version marker",
			content:  []byte(`{"apiVersion":` + "\n"),
			wantCode: apperrors.CodeNotImplemented,
		},
		{
			name:     "non string api version marker",
			content:  []byte(`{"apiVersion":2,"kind":"AuditEvent"}` + "\n"),
			wantCode: apperrors.CodeNotImplemented,
		},
		{
			name:     "non string kind marker",
			content:  []byte(`{"apiVersion":"dbgov.io/audit/v1","kind":null}` + "\n"),
			wantCode: apperrors.CodeNotImplemented,
		},
		{
			name: "two mebibyte line",
			content: []byte(
				`{"padding":"` + strings.Repeat("x", 2*1024*1024) +
					`","apiVersion":"opskit-core.io/audit/v2"}` + "\n",
			),
			wantCode: apperrors.CodeNotImplemented,
		},
		{
			name:     "line above supported maximum",
			content:  append(bytes.Repeat([]byte("x"), maxAuditEvidenceLineLen+1), '\n'),
			wantCode: apperrors.CodeLocalIOError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			path, rotated := writeAuditPruneFixture(t, home)
			if err := os.WriteFile(rotated, tt.content, 0o600); err != nil {
				t.Fatal(err)
			}
			beforeActive, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			beforeRotated, err := os.ReadFile(rotated)
			if err != nil {
				t.Fatal(err)
			}
			_, err = executeCommandForTest(
				"-o", "json", "--yes", "--ticket", "TEST-1", "--allow-audit-prune",
				"audit", "prune", "--path", path, "--keep-last", "0", "--confirm",
			)
			if err == nil {
				t.Fatalf("ambiguous v2 evidence was pruned (legacy expected code %s)", tt.wantCode)
			}
			afterActive, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			afterRotated, readErr := os.ReadFile(rotated)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if !bytes.Equal(afterActive, beforeActive) {
				t.Fatal("failed prune changed the target active log")
			}
			if !bytes.Equal(afterRotated, beforeRotated) {
				t.Fatal("v2 ambiguity rejection changed the rotated candidate")
			}
		})
	}
}

func TestAuditPruneRejectsV2InRetainedRotation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path, legacy := writeAuditPruneFixture(t, home)
	v2 := path + ".20260525-010203.log"
	if err := os.WriteFile(v2, []byte(`{"kind":"AuditEnvelope"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := executeCommandForTest(
		"-o", "json", "--yes", "--ticket", "TEST-1", "--allow-audit-prune",
		"audit", "prune", "--path", path, "--keep-last", "1", "--confirm",
	)
	if err == nil {
		t.Fatal("invalid retained v2 evidence was pruned")
	}
	for _, evidence := range []string{legacy, v2} {
		if _, statErr := os.Stat(evidence); statErr != nil {
			t.Fatalf("retained v2 rejection changed %s: %v", evidence, statErr)
		}
	}
}

func TestAuditCheckpointLstatRejectsNonRegularArtifacts(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T, string)
	}{
		{
			name: "directory",
			setup: func(t *testing.T, checkpoint string) {
				t.Helper()
				if err := os.Mkdir(checkpoint, 0o700); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "broken symlink",
			setup: func(t *testing.T, checkpoint string) {
				t.Helper()
				if err := os.Symlink(checkpoint+".missing", checkpoint); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			path, rotated := writeAuditPruneFixture(t, home)
			if err := os.Remove(path + ".checkpoint"); err != nil {
				t.Fatal(err)
			}
			tt.setup(t, path+".checkpoint")
			before, err := os.ReadFile(rotated)
			if err != nil {
				t.Fatal(err)
			}
			_, err = executeCommandForTest(
				"-o", "json", "--yes", "--ticket", "TEST-1", "--allow-audit-prune",
				"audit", "prune", "--path", path, "--keep-last", "0", "--confirm",
			)
			if err == nil {
				t.Fatal("non-regular checkpoint artifact was accepted")
			}
			after, readErr := os.ReadFile(rotated)
			if readErr != nil || !bytes.Equal(after, before) {
				t.Fatalf("checkpoint rejection changed evidence: %q, %v", after, readErr)
			}
		})
	}
}

func TestAuditPruneLockAndCandidateConsistency(t *testing.T) {
	t.Run("preview does not wait for lock", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		path, rotated := writeAuditPruneFixture(t, home)
		held := lockfile.New(path)
		if err := held.Acquire(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = held.Release() })
		t.Setenv("DBGOV_LOCK_TIMEOUT", "100ms")
		out, err := executeCommandForTest(
			"-o", "json", "audit", "prune", "--path", path, "--keep-last", "0",
		)
		if err != nil || !strings.Contains(out, filepath.Base(rotated)) {
			t.Fatalf("locked preview = (%s, %v), want immediate candidate list", out, err)
		}
	})

	t.Run("confirmed prune waits for lock", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		path, rotated := writeAuditPruneFixture(t, home)
		held := lockfile.New(path)
		if err := held.Acquire(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = held.Release() })
		t.Setenv("DBGOV_LOCK_TIMEOUT", "100ms")
		_, err := executeCommandForTest(
			"-o", "json", "--yes", "--ticket", "TEST-1", "--allow-audit-prune",
			"audit", "prune", "--path", path, "--keep-last", "0", "--confirm",
		)
		if err == nil || apperrors.AsAppError(err).Code != apperrors.CodeLocalIOError {
			t.Fatalf("confirmed prune lock error = %v, want LOCAL_IO_ERROR", err)
		}
		if _, statErr := os.Stat(rotated); statErr != nil {
			t.Fatalf("lock timeout changed evidence: %v", statErr)
		}
	})

	t.Run("changed candidates conflict", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		path, first := writeAuditPruneFixture(t, home)
		opts := auditPruneOptions{keepLast: 0}
		preview, err := auditPruneCandidates(path, opts)
		if err != nil {
			t.Fatal(err)
		}
		second := path + ".20260525-010203.log"
		if err := os.WriteFile(second, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		err = pruneAuditUnderLock(path, opts, preview)
		if err == nil || apperrors.AsAppError(err).Code != apperrors.CodeConflict {
			t.Fatalf("candidate change error = %v, want CONFLICT", err)
		}
		for _, candidate := range []string{first, second} {
			if _, statErr := os.Stat(candidate); statErr != nil {
				t.Fatalf("candidate conflict changed %s: %v", candidate, statErr)
			}
		}
	})

	t.Run("success audit appends after releasing same path lock", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		path, rotated := writeAuditPruneFixture(t, home)
		t.Setenv("DBGOV_LOCK_TIMEOUT", "100ms")
		_, err := executeCommandForTest(
			"-o", "json", "--yes", "--ticket", "TEST-1", "--allow-audit-prune",
			"audit", "prune", "--path", path, "--keep-last", "0", "--confirm",
		)
		if err != nil {
			t.Fatalf("confirmed prune deadlocked its success audit append: %v", err)
		}
		if _, statErr := os.Stat(rotated); !os.IsNotExist(statErr) {
			t.Fatalf("confirmed prune kept %s: %v", rotated, statErr)
		}
		controlPath := auditControlPath(path)
		active, readErr := os.ReadFile(controlPath)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !bytes.Contains(active, []byte(`"eventType":"audit.prune"`)) {
			t.Fatalf("control audit log lacks post-unlock success record: %s", active)
		}
	})
}

func TestAuditPruneDryRunListsV2Candidate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path, rotated := writeAuditPruneFixture(t, home)
	envelope := []byte(`{"apiVersion":"opskit-core.io/audit/v2","kind":"AuditEnvelope"}` + "\n")
	if err := os.WriteFile(rotated, envelope, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := executeCommandForTest(
		"-o", "json",
		"audit", "prune", "--path", path, "--keep-last", "0",
	)
	if err != nil {
		t.Fatalf("v2 prune dry-run error = %v; out=%s", err, out)
	}
	if !strings.Contains(out, filepath.Base(rotated)) || !strings.Contains(out, `"dryRun": true`) {
		t.Fatalf("v2 prune dry-run omitted candidate: %s", out)
	}
	if after, err := os.ReadFile(rotated); err != nil || !bytes.Equal(after, envelope) {
		t.Fatalf("v2 prune dry-run changed candidate: %q, %v", after, err)
	}
}

func TestAuditPruneCandidateOrderUsesStrictTimestampOrder(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "audit.log")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	first := path + ".20260101-000000.log"
	second := path + ".20260201-000000.log"
	third := path + ".20260301-000000.log"
	for _, filePath := range []string{third, first, second} {
		if err := os.WriteFile(filePath, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := auditPruneCandidates(path, auditPruneOptions{keepLast: 1})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{first, second}; !reflect.DeepEqual(got, want) {
		t.Fatalf("candidates = %v, want timestamp order %v", got, want)
	}
}

func TestAuditPruneCandidatesUseNumericOrdinalOrderAndStrictNames(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "audit.log")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{
		".20260101-000000.2.log",
		".20260101-000000.10.log",
		".20260101-000000.1.log",
		".20260101-000000.01.log",
		".20260101-000000.extra.log",
	} {
		if err := os.WriteFile(path+suffix, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := auditPruneCandidates(path, auditPruneOptions{keepLast: 1})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		path + ".20260101-000000.1.log",
		path + ".20260101-000000.2.log",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("candidates = %v, want strict numeric order %v", got, want)
	}
}

func writeAuditPruneFixture(t *testing.T, home string) (string, string) {
	t.Helper()
	path := filepath.Join(home, "audit.log")
	rotated := writeAuthenticatedAuditRotationsForTest(t, path, []string{"20260524-010203"})
	return path, rotated[0]
}
