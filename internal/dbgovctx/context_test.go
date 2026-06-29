package dbgovctx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	corectx "github.com/JiangHe12/opskit-core/ctx"
)

func TestLoadMigratesLegacyContextAPIVersion(t *testing.T) {
	corectx.Configure(corectx.Options{APIVersion: SupportedContextAPIVersion, ConfigDirName: ".dbgov"})
	t.Cleanup(func() {
		corectx.Configure(corectx.Options{APIVersion: "opskit-core.io/context/v1", ConfigDirName: ".opskit"})
		SetConfigPath("")
	})
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte(`apiVersion: dbgov.io/context/v1
current-context: dev
contexts:
    dev:
        engine: mysql
        host: 127.0.0.1
        port: 3306
`), 0o600); err != nil {
		t.Fatal(err)
	}
	SetConfigPath(configPath)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.APIVersion != SupportedContextAPIVersion {
		t.Fatalf("APIVersion = %q, want %q", cfg.APIVersion, SupportedContextAPIVersion)
	}
	updated, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(updated), legacyContextAPIVersion) || !strings.Contains(string(updated), SupportedContextAPIVersion) {
		t.Fatalf("context file was not migrated:\n%s", updated)
	}
}
