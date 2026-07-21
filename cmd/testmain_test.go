package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestMain(m *testing.M) {
	tempRoot, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "resolve isolated test root: %v\n", err)
		os.Exit(1)
	}
	home, err := os.MkdirTemp(tempRoot, "dbgov-cli-test-home-")
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "create isolated test home: %v\n", err)
		os.Exit(1)
	}
	oldHome, hadHome := os.LookupEnv("HOME")
	oldProfile, hadProfile := os.LookupEnv("USERPROFILE")
	oldTemp, hadTemp := os.LookupEnv("TMPDIR")
	_ = os.Setenv("HOME", home)
	_ = os.Setenv("USERPROFILE", home)
	_ = os.Setenv("TMPDIR", tempRoot)
	if err := secureMutationAuditTestParentPath(home); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "secure isolated test home: %v\n", err)
		_ = os.RemoveAll(home)
		os.Exit(1)
	}
	if err := ensureMutationAuditParent(filepath.Join(home, ".dbgov")); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "create isolated audit directory: %v\n", err)
		_ = os.RemoveAll(home)
		os.Exit(1)
	}

	code := m.Run()

	if hadHome {
		_ = os.Setenv("HOME", oldHome)
	} else {
		_ = os.Unsetenv("HOME")
	}
	if hadProfile {
		_ = os.Setenv("USERPROFILE", oldProfile)
	} else {
		_ = os.Unsetenv("USERPROFILE")
	}
	if hadTemp {
		_ = os.Setenv("TMPDIR", oldTemp)
	} else {
		_ = os.Unsetenv("TMPDIR")
	}
	_ = os.RemoveAll(home)
	os.Exit(code)
}
