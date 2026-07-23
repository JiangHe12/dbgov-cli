//go:build windows

package dbgovctx

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

type testTokenOwner struct {
	owner *windows.SID
}

func TestMain(m *testing.M) {
	root, err := configureWindowsTestEnvironment("dbgovctx-test-")
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "configure Windows test environment: %v\n", err)
		os.Exit(1)
	}
	code := m.Run()
	if err := os.RemoveAll(root); err != nil && code == 0 {
		_, _ = fmt.Fprintf(os.Stderr, "remove Windows test environment: %v\n", err)
		code = 1
	}
	os.Exit(code)
}

func configureWindowsTestEnvironment(prefix string) (string, error) {
	if err := useCurrentUserAsTestDefaultOwner(); err != nil {
		return "", err
	}
	root, err := os.MkdirTemp("", prefix)
	if err != nil {
		return "", err
	}
	cleanup := func(err error) (string, error) {
		_ = os.RemoveAll(root)
		return "", err
	}
	if err := protectWindowsTestDirectory(root); err != nil {
		return cleanup(err)
	}
	temp := filepath.Join(root, "temp")
	home := filepath.Join(root, "home")
	for _, path := range []string{temp, home} {
		if err := os.Mkdir(path, 0o700); err != nil {
			return cleanup(err)
		}
		if err := protectWindowsTestDirectory(path); err != nil {
			return cleanup(err)
		}
	}
	for name, value := range map[string]string{
		"TEMP":        temp,
		"TMP":         temp,
		"TMPDIR":      temp,
		"HOME":        home,
		"USERPROFILE": home,
	} {
		if err := os.Setenv(name, value); err != nil {
			return cleanup(err)
		}
	}
	if !strings.EqualFold(filepath.Clean(os.TempDir()), filepath.Clean(temp)) {
		return cleanup(fmt.Errorf("Windows temp directory = %q, want %q", os.TempDir(), temp))
	}
	return root, nil
}

func useCurrentUserAsTestDefaultOwner() error {
	var token windows.Token
	if err := windows.OpenProcessToken(
		windows.CurrentProcess(),
		windows.TOKEN_QUERY|windows.TOKEN_ADJUST_DEFAULT,
		&token,
	); err != nil {
		return err
	}
	defer func() { _ = token.Close() }()

	user, err := token.GetTokenUser()
	if err != nil {
		return err
	}
	owner := testTokenOwner{owner: user.User.Sid}
	return windows.SetTokenInformation(
		token,
		windows.TokenOwner,
		(*byte)(unsafe.Pointer(&owner)),
		uint32(unsafe.Sizeof(owner)),
	)
}

func protectWindowsTestDirectory(path string) error {
	userSID, err := currentWindowsTestUserSID()
	if err != nil {
		return err
	}
	systemSID, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return err
	}
	adminSID, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
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
		windowsTestExplicitAccess(userSID, windows.TRUSTEE_IS_USER, fullControl),
		windowsTestExplicitAccess(systemSID, windows.TRUSTEE_IS_WELL_KNOWN_GROUP, fullControl),
		windowsTestExplicitAccess(adminSID, windows.TRUSTEE_IS_GROUP, fullControl),
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

func currentWindowsTestUserSID() (*windows.SID, error) {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		return nil, err
	}
	defer func() { _ = token.Close() }()
	user, err := token.GetTokenUser()
	if err != nil {
		return nil, err
	}
	return user.User.Sid.Copy()
}

func windowsTestExplicitAccess(
	sid *windows.SID,
	trusteeType windows.TRUSTEE_TYPE,
	permissions windows.ACCESS_MASK,
) windows.EXPLICIT_ACCESS {
	return windows.EXPLICIT_ACCESS{
		AccessPermissions: permissions,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
		Trustee: windows.TRUSTEE{
			MultipleTrusteeOperation: windows.NO_MULTIPLE_TRUSTEE,
			TrusteeForm:              windows.TRUSTEE_IS_SID,
			TrusteeType:              trusteeType,
			TrusteeValue:             windows.TrusteeValueFromSID(sid),
		},
	}
}
