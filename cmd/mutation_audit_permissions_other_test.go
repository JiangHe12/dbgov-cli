//go:build !windows

package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func secureMutationAuditTestParent(t *testing.T, path string) {
	t.Helper()
	if err := secureMutationAuditTestParentPath(path); err != nil {
		t.Fatalf("Chmod(test parent) error = %v", err)
	}
}

func secureMutationAuditTestParentPath(path string) error {
	return os.Chmod(path, 0o700)
}

func TestMutationSpoolUnixRejectsInsecureModesAndSymlinks(t *testing.T) {
	parent := t.TempDir()
	secureMutationAuditTestParent(t, parent)
	spool := filepath.Join(parent, "audit.log"+mutationAuditSpoolSuffix)
	if err := os.Mkdir(spool, 0o755); err != nil {
		t.Fatalf("Mkdir(spool) error = %v", err)
	}
	if err := verifyMutationSpoolDirectory(spool); err == nil {
		t.Fatal("verifyMutationSpoolDirectory() error = nil for mode 0755")
	}
	if err := os.Chmod(spool, 0o700); err != nil {
		t.Fatalf("Chmod(spool) error = %v", err)
	}
	file := filepath.Join(spool, "00000000000000000001-00000000000000000000000000000001.json")
	if err := os.WriteFile(file, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(spool record) error = %v", err)
	}
	if err := verifyMutationSpoolFile(file); err == nil {
		t.Fatal("verifyMutationSpoolFile() error = nil for mode 0644")
	}
	link := filepath.Join(parent, "spool-link")
	if err := os.Symlink(spool, link); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}
	if err := verifyMutationSpoolDirectory(link); err == nil {
		t.Fatal("verifyMutationSpoolDirectory() error = nil for symlink")
	}
}

func TestExistingAuditParentModesAreNotRewritten(t *testing.T) {
	t.Run("custom path", func(t *testing.T) {
		parent := t.TempDir()
		if err := os.Chmod(parent, 0o750); err != nil {
			t.Fatal(err)
		}
		exerciseMutationAuditPath(t, filepath.Join(parent, "audit.log"))
		info, err := os.Stat(parent)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o750 {
			t.Fatalf("custom audit parent mode = %#o, want unchanged 0750", got)
		}
	})

	t.Run("default path", func(t *testing.T) {
		home := t.TempDir()
		if err := os.Chmod(home, 0o700); err != nil {
			t.Fatal(err)
		}
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		parent := filepath.Join(home, ".dbgov")
		if err := os.Mkdir(parent, 0o750); err != nil {
			t.Fatal(err)
		}
		exerciseMutationAuditPath(t, "")
		info, err := os.Stat(parent)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o750 {
			t.Fatalf("default audit parent mode = %#o, want unchanged 0750", got)
		}
	})
}
