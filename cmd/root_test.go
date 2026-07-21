package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestSchemaDiffUsesFakeBackendAndReportsDestructiveChange(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	secureMutationAuditTestParent(t, tmp)
	desired := writeTestFile(t, tmp, "desired.sql", `CREATE TABLE users (id BIGINT, name VARCHAR(100));`)
	var out, errOut bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--output", "plain", "--config", tmp + "/config.yaml", "schema", "diff", "-f", desired, "--fake"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	text := out.String()
	if !strings.Contains(text, "ADD_COLUMN") || !strings.Contains(text, "name") {
		t.Fatalf("output missing add column diff:\n%s", text)
	}
	if !strings.Contains(text, "DROP_COLUMN") || !strings.Contains(text, "legacy") || !strings.Contains(text, "DESTRUCTIVE") {
		t.Fatalf("output missing destructive drop column diff:\n%s", text)
	}
}

func TestCtxSetUseCurrentDeleteLifecycle(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	secureMutationAuditTestParent(t, home)
	configPath := home + "/config.yaml"
	var out, errOut bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--config", configPath, "--yes", "--ticket", "TEST-1", "--allow-context-change", "ctx", "set", "local", "--engine", "mysql", "--host", "127.0.0.1", "--port", "3306", "--database", "demo", "--server", "mysql://127.0.0.1:3306"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("ctx set error = %v", err)
	}

	out.Reset()
	cmd = NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--config", configPath, "--yes", "--ticket", "TEST-1", "--allow-context-change", "ctx", "use", "local"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("ctx use error = %v", err)
	}

	out.Reset()
	cmd = NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--config", configPath, "ctx", "current", "--output", "plain"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("ctx current error = %v", err)
	}
	if !strings.Contains(out.String(), "local") || !strings.Contains(out.String(), "mysql") {
		t.Fatalf("ctx current output = %q", out.String())
	}

	cmd = NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--config", configPath, "--yes", "--ticket", "TEST-1", "--allow-context-delete", "ctx", "delete", "local"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("ctx delete error = %v", err)
	}
}
