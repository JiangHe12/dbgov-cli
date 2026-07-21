//go:build windows

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"github.com/JiangHe12/opskit-core/v2/apperrors"
	"golang.org/x/sys/windows"
)

func ensureMutationAuditParent(path string) error {
	if _, err := os.Lstat(path); err == nil {
		return verifyMutationSpoolParent(path)
	} else if !os.IsNotExist(err) {
		return apperrors.New(apperrors.CodeLocalIOError, "failed to inspect mutation audit parent", nil)
	}
	if err := verifyMutationSpoolParent(filepath.Dir(path)); err != nil {
		return err
	}
	if err := createPrivateMutationDirectory(path); err != nil {
		if os.IsExist(err) {
			return verifyMutationSpoolDirectory(path)
		}
		return apperrors.New(apperrors.CodeLocalIOError, "failed to create mutation audit parent", nil)
	}
	return verifyMutationSpoolDirectory(path)
}

func ensureMutationSpoolDirectory(path string) error {
	if err := verifyMutationSpoolDirectoryPath(filepath.Dir(path), "parent", false); err != nil {
		return err
	}
	_, err := os.Lstat(path)
	if err == nil {
		return verifyMutationSpoolDirectory(path)
	}
	if !os.IsNotExist(err) {
		return apperrors.New(apperrors.CodeLocalIOError, "failed to inspect mutation outcome spool directory", nil)
	}
	if err := createPrivateMutationDirectory(path); err != nil {
		if os.IsExist(err) {
			return verifyMutationSpoolDirectory(path)
		}
		return apperrors.New(apperrors.CodeLocalIOError, "failed to create mutation outcome spool directory", nil)
	}
	return verifyMutationSpoolDirectory(path)
}

func createPrivateMutationDirectory(path string) error {
	userSID, systemSID, adminSID, err := trustedMutationSpoolSIDs()
	if err != nil {
		return err
	}
	descriptor, err := windows.SecurityDescriptorFromString(fmt.Sprintf(
		"O:%sD:P(A;OICI;FA;;;%s)(A;OICI;FA;;;%s)(A;OICI;FA;;;%s)",
		userSID,
		userSID,
		systemSID,
		adminSID,
	))
	if err != nil {
		return apperrors.New(apperrors.CodeLocalIOError, "failed to create private mutation directory descriptor", nil)
	}
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return apperrors.New(apperrors.CodeLocalIOError, "failed to encode private mutation directory path", nil)
	}
	attributes := windows.SecurityAttributes{SecurityDescriptor: descriptor}
	attributes.Length = uint32(unsafe.Sizeof(attributes))
	return windows.CreateDirectory(pathPtr, &attributes)
}

func verifyMutationSpoolDirectory(path string) error {
	return verifyMutationSpoolDirectoryPath(path, "spool", true)
}

func verifyMutationSpoolParent(path string) error {
	return verifyMutationSpoolDirectoryPath(path, "parent", false)
}

func verifyMutationSpoolDirectoryPath(path, kind string, exact bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return apperrors.New(apperrors.CodeLocalIOError, "failed to inspect mutation outcome spool "+kind, nil)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return apperrors.New(apperrors.CodeLocalIOError, "mutation outcome spool "+kind+" must be a real directory", nil)
	}
	if err := rejectMutationSpoolReparsePoint(path); err != nil {
		return err
	}
	if exact {
		return verifyMutationSpoolACL(path)
	}
	return verifyMutationSpoolParentACL(path)
}

func secureMutationSpoolFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return apperrors.New(apperrors.CodeLocalIOError, "failed to inspect mutation outcome spool file", nil)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return apperrors.New(apperrors.CodeLocalIOError, "mutation outcome spool entry must be a regular file", nil)
	}
	if err := rejectMutationSpoolReparsePoint(path); err != nil {
		return err
	}
	if err := setMutationSpoolACL(path, windows.NO_INHERITANCE); err != nil {
		return apperrors.New(apperrors.CodeLocalIOError, "failed to secure mutation outcome spool file", nil)
	}
	return verifyMutationSpoolACL(path)
}

func verifyMutationSpoolFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return apperrors.New(apperrors.CodeLocalIOError, "failed to inspect mutation outcome spool file", nil)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return apperrors.New(apperrors.CodeLocalIOError, "mutation outcome spool entry must be a regular file", nil)
	}
	if err := rejectMutationSpoolReparsePoint(path); err != nil {
		return err
	}
	return verifyMutationSpoolACL(path)
}

func rejectMutationSpoolReparsePoint(path string) error {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return apperrors.New(apperrors.CodeLocalIOError, "failed to encode mutation outcome spool path", nil)
	}
	attributes, err := windows.GetFileAttributes(pathPtr)
	if err != nil {
		return apperrors.New(apperrors.CodeLocalIOError, "failed to inspect mutation outcome spool attributes", nil)
	}
	if attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return apperrors.New(apperrors.CodeLocalIOError, "mutation outcome spool path must not be a reparse point", nil)
	}
	return nil
}

func rejectAuditPathReparsePoint(path string) error {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return apperrors.New(apperrors.CodeLocalIOError, "failed to encode audit path", nil)
	}
	attributes, err := windows.GetFileAttributes(pathPtr)
	if err != nil {
		return apperrors.New(apperrors.CodeLocalIOError, "failed to inspect audit path attributes", nil)
	}
	if attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return apperrors.New(apperrors.CodeLocalIOError, "audit path must not be a reparse point", nil)
	}
	return nil
}

func setMutationSpoolACL(path string, inheritance uint32) error {
	userSID, systemSID, adminSID, err := trustedMutationSpoolSIDs()
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
	entries := []windows.EXPLICIT_ACCESS{
		mutationSpoolExplicitAccess(userSID, windows.TRUSTEE_IS_USER, fullControl, inheritance),
		mutationSpoolExplicitAccess(systemSID, windows.TRUSTEE_IS_WELL_KNOWN_GROUP, fullControl, inheritance),
		mutationSpoolExplicitAccess(adminSID, windows.TRUSTEE_IS_GROUP, fullControl, inheritance),
	}
	dacl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		return apperrors.New(apperrors.CodeLocalIOError, "failed to create mutation outcome spool DACL", nil)
	}
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		dacl,
		nil,
	); err != nil {
		return apperrors.New(apperrors.CodeLocalIOError, "failed to set mutation outcome spool DACL", nil)
	}
	return nil
}

func verifyMutationSpoolACL(path string) error { //nolint:gocyclo // Exact owner, DACL, ACE type, SID, and duplicate checks form one boundary.
	userSID, systemSID, adminSID, err := trustedMutationSpoolSIDs()
	if err != nil {
		return err
	}
	descriptor, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return apperrors.New(apperrors.CodeLocalIOError, "failed to read mutation outcome spool DACL", nil)
	}
	owner, _, err := descriptor.Owner()
	if err != nil || owner == nil || !owner.Equals(userSID) {
		return apperrors.New(apperrors.CodeLocalIOError, "mutation outcome spool path is not owned by the current user", nil)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil {
		return apperrors.New(apperrors.CodeLocalIOError, "mutation outcome spool path has no valid DACL", nil)
	}
	if dacl.AceCount != 3 {
		return apperrors.New(apperrors.CodeLocalIOError, "mutation outcome spool path must have exactly three trusted ACEs", nil)
	}
	var userFound, systemFound, adminFound bool
	for index := uint16(0); index < dacl.AceCount; index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(index), &ace); err != nil {
			return apperrors.New(apperrors.CodeLocalIOError, "failed to inspect mutation outcome spool ACE", nil)
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return apperrors.New(apperrors.CodeLocalIOError, "mutation outcome spool path has an unexpected ACE type", nil)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart)) //nolint:gosec // Windows stores the SID in the ACE tail.
		switch {
		case sid.Equals(userSID):
			if userFound {
				return apperrors.New(apperrors.CodeLocalIOError, "mutation outcome spool path has a duplicate owner ACE", nil)
			}
			userFound = true
		case sid.Equals(systemSID):
			if systemFound {
				return apperrors.New(apperrors.CodeLocalIOError, "mutation outcome spool path has a duplicate SYSTEM ACE", nil)
			}
			systemFound = true
		case sid.Equals(adminSID):
			if adminFound {
				return apperrors.New(apperrors.CodeLocalIOError, "mutation outcome spool path has a duplicate Administrators ACE", nil)
			}
			adminFound = true
		default:
			return apperrors.New(apperrors.CodeLocalIOError, "mutation outcome spool path grants access to an untrusted SID", nil)
		}
	}
	if !userFound || !systemFound || !adminFound {
		return apperrors.New(apperrors.CodeLocalIOError, "mutation outcome spool path is missing a trusted ACE", nil)
	}
	return nil
}

func verifyMutationSpoolParentACL(path string) error { //nolint:gocyclo // Parent ownership and every effective ACE must be checked together.
	userSID, systemSID, adminSID, err := trustedMutationSpoolSIDs()
	if err != nil {
		return err
	}
	descriptor, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return apperrors.New(apperrors.CodeLocalIOError, "failed to read mutation outcome spool parent DACL", nil)
	}
	owner, _, err := descriptor.Owner()
	if err != nil || owner == nil || !owner.Equals(userSID) {
		return apperrors.New(apperrors.CodeLocalIOError, "mutation outcome spool parent is not owned by the current user", nil)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil {
		return apperrors.New(apperrors.CodeLocalIOError, "mutation outcome spool parent has no valid DACL", nil)
	}
	const fileDeleteChild windows.ACCESS_MASK = 0x00000040
	dangerous := windows.FILE_WRITE_DATA |
		windows.FILE_APPEND_DATA |
		windows.FILE_WRITE_EA |
		windows.FILE_WRITE_ATTRIBUTES |
		fileDeleteChild |
		windows.DELETE |
		windows.WRITE_DAC |
		windows.WRITE_OWNER |
		windows.GENERIC_WRITE |
		windows.GENERIC_ALL
	for index := uint16(0); index < dacl.AceCount; index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(index), &ace); err != nil {
			return apperrors.New(apperrors.CodeLocalIOError, "failed to inspect mutation outcome spool parent ACE", nil)
		}
		if ace.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE ||
			ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 {
			continue
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return apperrors.New(apperrors.CodeLocalIOError, "mutation outcome spool parent has an unsupported ACE type", nil)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart)) //nolint:gosec // Windows stores the SID in the ACE tail.
		if sid.Equals(userSID) || sid.Equals(systemSID) || sid.Equals(adminSID) {
			continue
		}
		if ace.Mask&dangerous != 0 {
			return apperrors.New(apperrors.CodeLocalIOError, "mutation outcome spool parent grants write access to an untrusted SID", nil)
		}
	}
	return nil
}

func trustedMutationSpoolSIDs() (*windows.SID, *windows.SID, *windows.SID, error) {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		return nil, nil, nil, apperrors.New(apperrors.CodeLocalIOError, "failed to open current process token", nil)
	}
	defer func() { _ = token.Close() }()
	user, err := token.GetTokenUser()
	if err != nil {
		return nil, nil, nil, apperrors.New(apperrors.CodeLocalIOError, "failed to resolve current user SID", nil)
	}
	userSID, err := user.User.Sid.Copy()
	if err != nil {
		return nil, nil, nil, apperrors.New(apperrors.CodeLocalIOError, "failed to copy current user SID", nil)
	}
	systemSID, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return nil, nil, nil, apperrors.New(apperrors.CodeLocalIOError, "failed to resolve SYSTEM SID", nil)
	}
	adminSID, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return nil, nil, nil, apperrors.New(apperrors.CodeLocalIOError, "failed to resolve Administrators SID", nil)
	}
	return userSID, systemSID, adminSID, nil
}

func mutationSpoolExplicitAccess(
	sid *windows.SID,
	trusteeType windows.TRUSTEE_TYPE,
	permissions windows.ACCESS_MASK,
	inheritance uint32,
) windows.EXPLICIT_ACCESS {
	return windows.EXPLICIT_ACCESS{
		AccessPermissions: permissions,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       inheritance,
		Trustee: windows.TRUSTEE{
			MultipleTrusteeOperation: windows.NO_MULTIPLE_TRUSTEE,
			TrusteeForm:              windows.TRUSTEE_IS_SID,
			TrusteeType:              trusteeType,
			TrusteeValue:             windows.TrusteeValueFromSID(sid),
		},
	}
}

func commitMutationSpoolFile(from, to string) error {
	fromPtr, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	toPtr, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(fromPtr, toPtr, windows.MOVEFILE_WRITE_THROUGH)
}

func replacePrivateMutationFile(from, to string) error {
	fromPtr, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	toPtr, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(fromPtr, toPtr, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

func syncMutationSpoolDirectory(_ string) error {
	// MOVEFILE_WRITE_THROUGH covers committed records. A replayed deletion may
	// be observed again after a crash; replay is deliberately at-least-once.
	return nil
}
