package snapshot

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/JiangHe12/opskit-core/v2/apperrors"
)

func TestCaptureListLoadRoundTrip(t *testing.T) {
	base := filepath.Join(t.TempDir(), "snapshots")
	firstTime := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	secondTime := firstTime.Add(time.Hour)

	firstID := prepareSnapshotForTest(t, base, Meta{
		Timestamp: firstTime,
		Operator:  "alice",
		Command:   "apply",
		Ticket:    "CHG-1",
		Context:   "dev",
		Target: &Target{
			Context:  "dev",
			Engine:   "mysql",
			Host:     "db.dev.example",
			Port:     3306,
			Database: "app",
		},
	}, map[string]string{"users": "CREATE TABLE `users` (`id` BIGINT);"})
	secondID := prepareSnapshotForTest(t, base, Meta{
		Timestamp: secondTime,
		Operator:  "bob",
		Command:   "reconcile",
		Ticket:    "CHG-2",
		Context:   "prod",
	}, map[string]string{
		"users":  "CREATE TABLE `users` (`id` BIGINT);",
		"orders": "CREATE TABLE `orders` (`id` BIGINT);",
	})
	if firstID == "" || secondID == "" || firstID == secondID {
		t.Fatalf("snapshot ids = %q/%q, want unique non-empty ids", firstID, secondID)
	}

	metas, err := List(base)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(metas) != 2 || metas[0].ID != secondID || metas[1].ID != firstID {
		t.Fatalf("List() = %+v, want reverse chronological order", metas)
	}
	if metas[0].TableCount != 2 || metas[0].Command != "reconcile" || metas[1].TableCount != 1 {
		t.Fatalf("List() metadata = %+v", metas)
	}

	snap, err := Load(base, firstID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if snap.Meta.ID != firstID ||
		snap.Meta.Operator != "alice" ||
		snap.Meta.Target == nil ||
		snap.Meta.Target.Database != "app" ||
		snap.Tables["users"] == "" {
		t.Fatalf("Load() = %+v", snap)
	}
}

func TestLoadRetainsLegacyUnboundSnapshotCompatibility(t *testing.T) {
	base := filepath.Join(t.TempDir(), "snapshots")
	id := prepareSnapshotForTest(t, base, Meta{
		Timestamp: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Command:   "apply",
		Context:   "legacy",
	}, map[string]string{"users": "CREATE TABLE `users` (`id` BIGINT);"})
	snap, err := Load(base, id)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if snap.Meta.Target != nil {
		t.Fatalf("legacy target = %+v, want nil", snap.Meta.Target)
	}
}

func TestPrepareRejectsMalformedTargetBinding(t *testing.T) {
	_, _, err := Prepare(Meta{
		Command: "apply",
		Target: &Target{
			Context:  "prod",
			Engine:   "postgres",
			Host:     "db.example",
			Port:     5432,
			Database: "app",
		},
	}, map[string]string{})
	if got := apperrors.AsAppError(err).Code; got != apperrors.CodeValidationFailed {
		t.Fatalf("error code = %s, want %s (err=%v)", got, apperrors.CodeValidationFailed, err)
	}
}

func prepareSnapshotForTest(t *testing.T, base string, meta Meta, tables map[string]string) string {
	t.Helper()
	id, data, err := Prepare(meta, tables)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, id+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestListMissingDirectoryReturnsEmpty(t *testing.T) {
	metas, err := List(filepath.Join(t.TempDir(), "missing"))
	if err != nil {
		t.Fatalf("List(missing) error = %v", err)
	}
	if len(metas) != 0 {
		t.Fatalf("List(missing) = %+v, want empty", metas)
	}
}

func TestLoadInvalidSnapshotIDIsValidationFailed(t *testing.T) {
	_, err := Load(t.TempDir(), "../escape")
	if got := apperrors.AsAppError(err).Code; got != apperrors.CodeValidationFailed {
		t.Fatalf("error code = %s, want %s; err = %v", got, apperrors.CodeValidationFailed, err)
	}
}
