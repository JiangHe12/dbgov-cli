package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "dbgov-cli-test-home-")
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "create isolated test home: %v\n", err)
		os.Exit(1)
	}
	resolvedHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "resolve isolated test home: %v\n", err)
		_ = os.RemoveAll(home)
		os.Exit(1)
	}
	home = resolvedHome
	if err := secureMutationAuditTestParentPath(home); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "secure isolated test home: %v\n", err)
		_ = os.RemoveAll(home)
		os.Exit(1)
	}

	type environmentValue struct {
		name    string
		value   string
		present bool
	}
	environment := []environmentValue{
		{name: "HOME"},
		{name: "USERPROFILE"},
		{name: "TMPDIR"},
		{name: "TEMP"},
		{name: "TMP"},
	}
	for index := range environment {
		environment[index].value, environment[index].present = os.LookupEnv(environment[index].name)
		_ = os.Setenv(environment[index].name, home)
	}
	if err := ensureMutationAuditParent(filepath.Join(home, ".dbgov")); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "create isolated audit directory: %v\n", err)
		_ = os.RemoveAll(home)
		os.Exit(1)
	}

	code := m.Run()

	for _, variable := range environment {
		if variable.present {
			_ = os.Setenv(variable.name, variable.value)
		} else {
			_ = os.Unsetenv(variable.name)
		}
	}
	_ = os.RemoveAll(home)
	os.Exit(code)
}
