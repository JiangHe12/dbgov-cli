package cmd

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JiangHe12/opskit-core/v2/apperrors"

	"github.com/JiangHe12/dbgov-cli/internal/backend/fake"
)

func TestCommandClosesBackendOnSuccessAndEarlyFailure(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "query success", args: []string{"query", "--sql", "SELECT 1", "--fake"}},
		{name: "data validation failure", args: []string{"data", "exec", "--sql", "SELECT 1", "--fake"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			backend := fake.New()
			restore := stubFakeBackend(t, backend)
			defer restore()

			_, _ = executeCommandForTest(test.args...)
			if backend.CloseCalls != 1 {
				t.Fatalf("CloseCalls = %d, want 1", backend.CloseCalls)
			}
		})
	}
}

func TestReadCloseFailureIsReturned(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	backend := fake.New()
	backend.CloseErr = errors.New("close failed")
	restore := stubFakeBackend(t, backend)
	defer restore()

	_, err := executeCommandForTest("query", "--sql", "SELECT 1", "--fake")
	if appErr := apperrors.AsAppError(err); appErr.Code != apperrors.CodeBackendError {
		t.Fatalf("error = %#v, want BACKEND_ERROR", appErr)
	}
}

func TestMutationCloseFailureDoesNotInviteRetry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	backend := fake.New()
	backend.CloseErr = errors.New("close failed")
	restore := stubFakeBackend(t, backend)
	defer restore()

	_, err := executeCommandForTest("--yes", "data", "exec", "--sql", "INSERT INTO users(id) VALUES (1)", "--fake")
	appErr := apperrors.AsAppError(err)
	if appErr.Code != apperrors.CodePartialFailure {
		t.Fatalf("error = %#v, want PARTIAL_FAILURE", appErr)
	}
	if !strings.Contains(strings.ToLower(appErr.Suggestion), "do not retry") {
		t.Fatalf("suggestion = %q, want no-retry guidance", appErr.Suggestion)
	}
}

func TestSchemaDumpDirectoryCloseFailureDoesNotInviteRetry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	backend := fake.New()
	backend.CloseErr = errors.New("close failed")
	restore := stubFakeBackend(t, backend)
	defer restore()

	_, err := executeCommandForTest(
		"--yes",
		"schema", "dump",
		"--dir", filepath.Join(home, "schema"),
		"--fake",
	)
	appErr := apperrors.AsAppError(err)
	if appErr.Code != apperrors.CodePartialFailure {
		t.Fatalf("error = %#v, want PARTIAL_FAILURE", appErr)
	}
	if !strings.Contains(strings.ToLower(appErr.Suggestion), "do not retry") {
		t.Fatalf("suggestion = %q, want no-retry guidance", appErr.Suggestion)
	}
}
