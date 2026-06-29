package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestVersionJSON(t *testing.T) {
	SetVersionInfo("v0.0.0-test", "deadbeef", "2026-06-02")
	defer SetVersionInfo("dev", "", "")

	var out, errOut bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"-o", "json", "version"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v, stderr=%s", err, errOut.String())
	}
	var env struct {
		APIVersion string `json:"apiVersion"`
		Kind       string `json:"kind"`
		Success    bool   `json:"success"`
		Data       struct {
			Version string `json:"version"`
			Commit  string `json:"commit"`
			Built   string `json:"built"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("version output is not JSON: %v\n%s", err, out.String())
	}
	if env.APIVersion != "dbgov.io/v1" || env.Kind != "VersionInfo" || !env.Success {
		t.Fatalf("unexpected envelope: %+v", env)
	}
	if env.Data.Version != "v0.0.0-test" || env.Data.Commit != "deadbeef" || env.Data.Built != "2026-06-02" {
		t.Fatalf("unexpected version data: %+v", env.Data)
	}
}

func TestVersionTable(t *testing.T) {
	SetVersionInfo("v0.0.0-test", "deadbeef", "2026-06-02")
	defer SetVersionInfo("dev", "", "")

	var out, errOut bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"-o", "table", "version"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v, stderr=%s", err, errOut.String())
	}
	if want := "dbgov-cli v0.0.0-test (commit: deadbeef, built: 2026-06-02)\n"; out.String() != want {
		t.Fatalf("unexpected version table: %q", out.String())
	}
}

func TestVersionPlain(t *testing.T) {
	SetVersionInfo("v0.0.0-test", "deadbeef", "2026-06-02")
	defer SetVersionInfo("dev", "", "")

	var out, errOut bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"-o", "plain", "version"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v, stderr=%s", err, errOut.String())
	}
	if want := "v0.0.0-test\n"; out.String() != want {
		t.Fatalf("unexpected version plain: %q", out.String())
	}
}

func TestRootVersionFlag(t *testing.T) {
	SetVersionInfo("v0.0.0-test", "deadbeef", "2026-06-02")
	defer SetVersionInfo("dev", "", "")

	var out, errOut bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--version"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v, stderr=%s", err, errOut.String())
	}
	if got := out.String(); got != "dbgov-cli version v0.0.0-test\n" {
		t.Fatalf("unexpected --version output: %q", got)
	}
}

func TestCapabilitiesPlain(t *testing.T) {
	var out, errOut bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"-o", "plain", "capabilities"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v, stderr=%s", err, errOut.String())
	}
	want := strings.Join(capabilityPlainCommands(), "\n") + "\n"
	if out.String() != want {
		t.Fatalf("unexpected capabilities plain:\n%s", out.String())
	}
	if strings.Contains(out.String(), "{") || strings.Contains(out.String(), "\t") {
		t.Fatalf("capabilities plain should be a command list, got %q", out.String())
	}
}

func TestGoldenCapabilities(t *testing.T) {
	var out, errOut bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"-o", "json", "capabilities"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v, stderr=%s", err, errOut.String())
	}
	assertGoldenJSON(t, "testdata/golden/capabilities.json", out.Bytes())
}
