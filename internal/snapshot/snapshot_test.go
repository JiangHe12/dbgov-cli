package snapshot

import (
	"path/filepath"
	"testing"
	"time"
)

func TestCaptureListLoadRoundTrip(t *testing.T) {
	base := filepath.Join(t.TempDir(), "snapshots")
	firstTime := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	secondTime := firstTime.Add(time.Hour)

	firstID, err := Capture(base, Meta{
		Timestamp: firstTime,
		Operator:  "alice",
		Command:   "apply",
		Ticket:    "CHG-1",
		Context:   "dev",
	}, map[string]string{"users": "CREATE TABLE `users` (`id` BIGINT);"})
	if err != nil {
		t.Fatalf("Capture(first) error = %v", err)
	}
	secondID, err := Capture(base, Meta{
		Timestamp: secondTime,
		Operator:  "bob",
		Command:   "reconcile",
		Ticket:    "CHG-2",
		Context:   "prod",
	}, map[string]string{
		"users":  "CREATE TABLE `users` (`id` BIGINT);",
		"orders": "CREATE TABLE `orders` (`id` BIGINT);",
	})
	if err != nil {
		t.Fatalf("Capture(second) error = %v", err)
	}
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
	if snap.Meta.ID != firstID || snap.Meta.Operator != "alice" || snap.Tables["users"] == "" {
		t.Fatalf("Load() = %+v", snap)
	}
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
