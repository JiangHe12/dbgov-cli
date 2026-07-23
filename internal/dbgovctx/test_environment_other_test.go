//go:build !windows

package dbgovctx

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestMain(m *testing.M) {
	root, err := configureNonWindowsTestEnvironment("dbgovctx-test-")
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "configure test environment: %v\n", err)
		os.Exit(1)
	}
	code := m.Run()
	if err := os.RemoveAll(root); err != nil && code == 0 {
		_, _ = fmt.Fprintf(os.Stderr, "remove test environment: %v\n", err)
		code = 1
	}
	os.Exit(code)
}

func configureNonWindowsTestEnvironment(prefix string) (string, error) {
	tempRoot, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		return "", err
	}
	root, err := os.MkdirTemp(tempRoot, prefix)
	if err != nil {
		return "", err
	}
	cleanup := func(err error) (string, error) {
		_ = os.RemoveAll(root)
		return "", err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return cleanup(err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return cleanup(err)
	}
	temp := filepath.Join(root, "temp")
	home := filepath.Join(root, "home")
	for _, path := range []string{temp, home} {
		if err := os.Mkdir(path, 0o700); err != nil {
			return cleanup(err)
		}
	}
	for name, value := range map[string]string{
		"TEMP":        temp,
		"TMP":         temp,
		"TMPDIR":      temp,
		"HOME":        home,
		"USERPROFILE": home,
	} {
		if err := os.Setenv(name, value); err != nil {
			return cleanup(err)
		}
	}
	resolvedTemp, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		return cleanup(err)
	}
	if filepath.Clean(resolvedTemp) != filepath.Clean(temp) {
		return cleanup(fmt.Errorf("temp directory = %q, want %q", resolvedTemp, temp))
	}
	return root, nil
}
