//go:build windows

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func secureMutationAuditTestParent(t *testing.T, path string) {
	t.Helper()
	if err := secureMutationAuditTestParentPath(path); err != nil {
		t.Fatalf("setMutationSpoolACL(test parent) error = %v", err)
	}
}

func secureMutationAuditTestParentPath(path string) error {
	return setMutationSpoolACL(path, windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT)
}

func TestMutationSpoolWindowsUsesOwnerOnlyACLAndRejectsReparsePoints(t *testing.T) {
	parent := t.TempDir()
	secureMutationAuditTestParent(t, parent)
	spool := filepath.Join(parent, "audit.log"+mutationAuditSpoolSuffix)
	if err := ensureMutationSpoolDirectory(spool); err != nil {
		t.Fatalf("ensureMutationSpoolDirectory() error = %v", err)
	}
	if err := verifyMutationSpoolDirectory(spool); err != nil {
		t.Fatalf("verifyMutationSpoolDirectory() error = %v", err)
	}
	file := filepath.Join(spool, "test.tmp")
	if err := os.WriteFile(file, []byte("test"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := secureMutationSpoolFile(file); err != nil {
		t.Fatalf("secureMutationSpoolFile() error = %v", err)
	}
	if err := verifyMutationSpoolFile(file); err != nil {
		t.Fatalf("verifyMutationSpoolFile() error = %v", err)
	}
	link := filepath.Join(parent, "spool-link")
	if err := os.Symlink(spool, link); err != nil {
		t.Skipf("creating a Windows symlink requires Developer Mode or privilege: %v", err)
	}
	if err := verifyMutationSpoolDirectory(link); err == nil {
		t.Fatal("verifyMutationSpoolDirectory() error = nil for reparse point")
	}
}

func TestExistingAuditParentACLsAreNotRewritten(t *testing.T) {
	t.Run("custom path", func(t *testing.T) {
		parent := t.TempDir()
		if err := setReadableMutationAuditTestParentACL(parent); err != nil {
			t.Fatal(err)
		}
		before := mutationAuditTestSecurityDescriptor(t, parent)
		exerciseMutationAuditPath(t, filepath.Join(parent, "audit.log"))
		after := mutationAuditTestSecurityDescriptor(t, parent)
		if after != before {
			t.Fatalf("custom audit parent ACL changed:\nbefore: %s\nafter:  %s", before, after)
		}
	})

	t.Run("default path", func(t *testing.T) {
		home := t.TempDir()
		secureMutationAuditTestParent(t, home)
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		parent := filepath.Join(home, ".dbgov")
		if err := createPrivateMutationDirectory(parent); err != nil {
			t.Fatal(err)
		}
		if err := setReadableMutationAuditTestParentACL(parent); err != nil {
			t.Fatal(err)
		}
		before := mutationAuditTestSecurityDescriptor(t, parent)
		exerciseMutationAuditPath(t, "")
		after := mutationAuditTestSecurityDescriptor(t, parent)
		if after != before {
			t.Fatalf("default audit parent ACL changed:\nbefore: %s\nafter:  %s", before, after)
		}
	})
}

func setReadableMutationAuditTestParentACL(path string) error {
	userSID, systemSID, adminSID, err := trustedMutationSpoolSIDs()
	if err != nil {
		return err
	}
	usersSID, err := windows.CreateWellKnownSid(windows.WinBuiltinUsersSid)
	if err != nil {
		return err
	}
	fullControl := windows.ACCESS_MASK(
		windows.STANDARD_RIGHTS_ALL |
			windows.FILE_GENERIC_READ |
			windows.FILE_GENERIC_WRITE |
			windows.FILE_GENERIC_EXECUTE |
			windows.DELETE,
	)
	readOnly := windows.ACCESS_MASK(windows.FILE_GENERIC_READ | windows.FILE_GENERIC_EXECUTE)
	entries := []windows.EXPLICIT_ACCESS{
		mutationSpoolExplicitAccess(userSID, windows.TRUSTEE_IS_USER, fullControl, windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT),
		mutationSpoolExplicitAccess(systemSID, windows.TRUSTEE_IS_WELL_KNOWN_GROUP, fullControl, windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT),
		mutationSpoolExplicitAccess(adminSID, windows.TRUSTEE_IS_GROUP, fullControl, windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT),
		mutationSpoolExplicitAccess(usersSID, windows.TRUSTEE_IS_GROUP, readOnly, windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT),
	}
	dacl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		dacl,
		nil,
	)
}

func mutationAuditTestSecurityDescriptor(t *testing.T, path string) string {
	t.Helper()
	descriptor, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		t.Fatal(err)
	}
	return descriptor.String()
}
