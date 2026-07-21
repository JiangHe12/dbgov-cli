package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JiangHe12/opskit-core/v2/apperrors"
)

const auditWarningText = "warning: failed to write audit log:"

func TestAuditWriteFailureWarnsWithoutFailingSuccessfulCommand(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	secureMutationAuditTestParent(t, home)
	blockDefaultAuditPath(t, home, ".dbgov")

	stderr, err := executeDbgovWithStderr(t,
		"--config", filepath.Join(home, "config.yaml"),
		"-o", "json",
		"audit", "query", "--path", filepath.Join(home, "missing.log"),
	)
	if err != nil {
		t.Fatalf("audit query error = %v", err)
	}
	if !strings.Contains(stderr, auditWarningText) {
		t.Fatalf("stderr = %q, want audit warning", stderr)
	}
}

func TestAuditWriteFailureDoesNotReplaceOriginalExitCode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	secureMutationAuditTestParent(t, home)
	blockDefaultAuditPath(t, home, ".dbgov")
	malformed := filepath.Join(home, "malformed.log")
	writePrivateAuditTestFile(t, malformed, []byte("{not-json}\n"))

	stderr, err := executeDbgovWithStderr(t,
		"--config", filepath.Join(home, "config.yaml"),
		"-o", "json",
		"audit", "verify", "--path", malformed, "--strict",
	)
	if apperrors.ExitCode(err) != 9 {
		t.Fatalf("error = %v; exit = %d, want 9", err, apperrors.ExitCode(err))
	}
	if !strings.Contains(stderr, auditWarningText) {
		t.Fatalf("stderr = %q, want audit warning", stderr)
	}
}

func TestAuditDefaultPathFailureWarnsWithoutFailingCommand(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("HOMEDRIVE", "")
	t.Setenv("HOMEPATH", "")
	configPath := filepath.Join(t.TempDir(), "config.yaml")

	stderr, err := executeDbgovWithStderr(t,
		"--config", configPath,
		"-o", "json",
		"audit", "query", "--path", filepath.Join(t.TempDir(), "missing.log"),
	)
	if err != nil {
		t.Fatalf("audit query error = %v", err)
	}
	if !strings.Contains(stderr, auditWarningText) {
		t.Fatalf("stderr = %q, want DefaultPath audit warning", stderr)
	}
}

func executeDbgovWithStderr(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command := NewRootCmd()
	command.SetOut(&stdout)
	command.SetErr(&stderr)
	command.SetArgs(args)
	err := command.Execute()
	return stderr.String(), err
}

func blockDefaultAuditPath(t *testing.T, home, configDir string) {
	t.Helper()
	path := filepath.Join(home, configDir, "audit.log")
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", path, err)
	}
}

func writePrivateAuditTestFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := secureMutationSpoolFile(path); err != nil {
		t.Fatalf("secure audit test file: %v", err)
	}
}
