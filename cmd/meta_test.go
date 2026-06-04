package cmd

import (
	"bytes"
	"encoding/json"
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
			Date    string `json:"date"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("version output is not JSON: %v\n%s", err, out.String())
	}
	if env.APIVersion != "dbgov.io/v1" || env.Kind != "VersionInfo" || !env.Success {
		t.Fatalf("unexpected envelope: %+v", env)
	}
	if env.Data.Version != "v0.0.0-test" || env.Data.Commit != "deadbeef" || env.Data.Date != "2026-06-02" {
		t.Fatalf("unexpected version data: %+v", env.Data)
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
