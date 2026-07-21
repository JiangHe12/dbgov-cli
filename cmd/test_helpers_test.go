package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JiangHe12/opskit-core/v2/apperrors"
)

func writeTestFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

func executeCommandForTest(args ...string) (string, error) {
	if err := secureTemporaryTestHome(); err != nil {
		return "", err
	}
	var out bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func secureTemporaryTestHome() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	temp, err := filepath.Abs(os.TempDir())
	if err != nil {
		return err
	}
	temp, err = filepath.EvalSymlinks(temp)
	if err != nil {
		return err
	}
	home, err = filepath.Abs(home)
	if err != nil {
		return err
	}
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(temp, home)
	if err != nil {
		return err
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return apperrors.New(
			apperrors.CodeLocalIOError,
			"refusing to run command tests outside an isolated temporary home",
			nil,
		)
	}
	if info, statErr := os.Lstat(home); statErr == nil && info.IsDir() {
		if err := secureMutationAuditTestParentPath(home); err != nil {
			return err
		}
	}
	auditDir := filepath.Join(home, ".dbgov")
	if info, statErr := os.Lstat(auditDir); statErr == nil && info.IsDir() {
		if err := secureMutationAuditTestParentPath(auditDir); err != nil {
			return err
		}
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return statErr
	}
	return ensureMutationAuditParent(auditDir)
}
