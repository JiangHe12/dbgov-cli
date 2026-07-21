package cmd

import (
	"strings"
	"testing"
)

func TestGlobalFlagsHelp(t *testing.T) {
	out, err := executeCommandForTest("--help")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, flag := range []string{
		"--debug",
		"--trace",
		"--no-color",
		"--allow-context-change",
		"--allow-context-delete",
		"--allow-role-change",
	} {
		if !strings.Contains(out, flag) {
			t.Fatalf("help missing %s:\n%s", flag, out)
		}
	}
}

func TestGlobalFlagsWithVersion(t *testing.T) {
	SetVersionInfo("v0.0.0-test", "deadbeef", "2026-06-29")
	t.Cleanup(func() { SetVersionInfo("dev", "", "") })

	out, err := executeCommandForTest("--debug", "--trace", "--no-color", "-o", "plain", "version")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if want := "v0.0.0-test\n"; out != want {
		t.Fatalf("version plain = %q, want %q", out, want)
	}
}
