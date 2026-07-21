//go:build integration

package cmd

import (
	"os"
	"testing"
)

func requiredDBIntegrationEnv(t *testing.T, name string) string {
	t.Helper()
	value := os.Getenv(name)
	if value != "" {
		return value
	}
	if os.Getenv("DBGOV_TEST_REQUIRED") == "1" {
		t.Fatalf("%s is required when DBGOV_TEST_REQUIRED=1", name)
	}
	t.Skipf("%s is not set", name)
	return ""
}
