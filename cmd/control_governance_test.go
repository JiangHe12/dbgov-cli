package cmd

import (
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JiangHe12/opskit-core/v2/apperrors"
	corectx "github.com/JiangHe12/opskit-core/v2/ctx"

	dbgaudit "github.com/JiangHe12/dbgov-cli/internal/audit"
	"github.com/JiangHe12/dbgov-cli/internal/dbgovctx"
	"github.com/JiangHe12/dbgov-cli/internal/safety"
)

func TestTrustedOperatorUsesOSUserAndHostname(t *testing.T) {
	originalUser := currentOSUser
	originalHost := currentHost
	currentOSUser = func() (*user.User, error) {
		return &user.User{Username: `DOMAIN\alice`}, nil
	}
	currentHost = func() (string, error) { return "Build-HOST", nil }
	t.Cleanup(func() {
		currentOSUser = originalUser
		currentHost = originalHost
	})

	got, err := resolveTrustedOperator()
	if err != nil {
		t.Fatalf("resolveTrustedOperator() error = %v", err)
	}
	if got != `DOMAIN\alice@build-host` {
		t.Fatalf("operator = %q", got)
	}
}

func TestTrustedOperatorFailsClosed(t *testing.T) {
	originalUser := currentOSUser
	originalHost := currentHost
	t.Cleanup(func() {
		currentOSUser = originalUser
		currentHost = originalHost
	})

	currentOSUser = func() (*user.User, error) { return nil, errors.New("lookup failed") }
	if _, err := executeCommandForTest("version"); apperrors.AsAppError(err).Code != apperrors.CodeAuthFailed {
		t.Fatalf("user lookup error = %#v", err)
	}

	currentOSUser = func() (*user.User, error) { return &user.User{Username: "alice"}, nil }
	currentHost = func() (string, error) { return "", nil }
	if _, err := executeCommandForTest("version"); apperrors.AsAppError(err).Code != apperrors.CodeAuthFailed {
		t.Fatalf("empty hostname error = %#v", err)
	}
}

func TestOperatorFlagAndEnvironmentCannotSpoofAuditIdentity(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DBGOV_OPERATOR", "environment-spoof")
	configPath := filepath.Join(home, "config.yaml")
	actual, err := resolveTrustedOperator()
	if err != nil {
		t.Fatal(err)
	}

	_, err = executeCommandForTest(
		"--config", configPath,
		"--operator", "flag-spoof",
		"--yes", "--ticket", "TEST-1", "--allow-context-change",
		"ctx", "set", "local", "--host", "127.0.0.1",
	)
	if err != nil {
		t.Fatalf("ctx set error = %v", err)
	}
	event := lastAuditEvent(t, home)
	if event.EventType != dbgaudit.EventTypeContextSet || event.Operator != actual {
		t.Fatalf("context audit event = %+v, want operator %q", event, actual)
	}
	if event.Operator == "flag-spoof" || event.Operator == "environment-spoof" {
		t.Fatalf("spoofed audit operator = %q", event.Operator)
	}
}

func TestOperatorFlagCannotSpoofRoleAuthorization(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	configPath := filepath.Join(home, "config.yaml")
	dbgovctx.SetConfigPath(configPath)
	t.Cleanup(func() { dbgovctx.SetConfigPath("") })
	actual, err := resolveTrustedOperator()
	if err != nil {
		t.Fatal(err)
	}
	ctx := testContextWithRoles(map[string]string{
		actual:  safety.RoleReader,
		"spoof": safety.RoleAdmin,
	})
	if err := dbgovctx.SetContext("prod", ctx); err != nil {
		t.Fatal(err)
	}

	_, err = executeCommandForTest(
		"--config", configPath,
		"--operator", "spoof",
		"--yes", "--ticket", "TEST-1", "--allow-context-delete",
		"ctx", "delete", "prod",
	)
	if apperrors.AsAppError(err).Code != apperrors.CodeAuthorizationRequired {
		t.Fatalf("delete error = %#v", err)
	}
	cfg, loadErr := dbgovctx.Load()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if _, exists := cfg.Contexts["prod"]; !exists {
		t.Fatal("spoofed operator deleted protected context")
	}
}

func TestContextSetUsesTargetPreChangePolicy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	configPath := filepath.Join(home, "config.yaml")
	dbgovctx.SetConfigPath(configPath)
	t.Cleanup(func() { dbgovctx.SetConfigPath("") })
	actual, err := resolveTrustedOperator()
	if err != nil {
		t.Fatal(err)
	}
	if err := dbgovctx.SetContext("current", testContextWithRoles(map[string]string{actual: safety.RoleAdmin})); err != nil {
		t.Fatal(err)
	}
	if err := dbgovctx.SetContext("target", testContextWithRoles(map[string]string{actual: safety.RoleReader})); err != nil {
		t.Fatal(err)
	}
	if err := dbgovctx.UseContext("current"); err != nil {
		t.Fatal(err)
	}

	_, err = executeCommandForTest(
		"--config", configPath,
		"--yes", "--ticket", "TEST-1", "--allow-context-change",
		"ctx", "set", "target", "--host", "changed.example",
	)
	if apperrors.AsAppError(err).Code != apperrors.CodeAuthorizationRequired {
		t.Fatalf("ctx set error = %#v", err)
	}
	cfg, loadErr := dbgovctx.Load()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if cfg.Contexts["target"].Host != "127.0.0.1" {
		t.Fatalf("target changed under current-context policy: %+v", cfg.Contexts["target"])
	}
}

func TestNewContextUsesPersistedCurrentPolicyNotContextOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	configPath := filepath.Join(home, "config.yaml")
	dbgovctx.SetConfigPath(configPath)
	t.Cleanup(func() { dbgovctx.SetConfigPath("") })
	actual, err := resolveTrustedOperator()
	if err != nil {
		t.Fatal(err)
	}
	if err := dbgovctx.SetContext("locked", testContextWithRoles(map[string]string{actual: safety.RoleReader})); err != nil {
		t.Fatal(err)
	}
	if err := dbgovctx.SetContext("override-admin", testContextWithRoles(map[string]string{actual: safety.RoleAdmin})); err != nil {
		t.Fatal(err)
	}
	if err := dbgovctx.UseContext("locked"); err != nil {
		t.Fatal(err)
	}

	_, err = executeCommandForTest(
		"--config", configPath,
		"--context", "override-admin",
		"--yes", "--ticket", "TEST-1", "--allow-context-change",
		"ctx", "set", "new-target", "--host", "new.example",
	)
	if apperrors.AsAppError(err).Code != apperrors.CodeAuthorizationRequired {
		t.Fatalf("ctx set error = %#v", err)
	}
	cfg, loadErr := dbgovctx.Load()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if _, exists := cfg.Contexts["new-target"]; exists {
		t.Fatal("--context override bypassed persisted current policy")
	}
}

func TestContextUseCannotDowngradePersistedCurrentPolicy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	configPath := filepath.Join(home, "config.yaml")
	dbgovctx.SetConfigPath(configPath)
	t.Cleanup(func() { dbgovctx.SetConfigPath("") })
	actual, err := resolveTrustedOperator()
	if err != nil {
		t.Fatal(err)
	}
	if err := dbgovctx.SetContext("locked", testContextWithRoles(map[string]string{actual: safety.RoleReader})); err != nil {
		t.Fatal(err)
	}
	if err := dbgovctx.SetContext("weak", testContextWithRoles(nil)); err != nil {
		t.Fatal(err)
	}
	if err := dbgovctx.UseContext("locked"); err != nil {
		t.Fatal(err)
	}

	_, err = executeCommandForTest(
		"--config", configPath,
		"--yes", "--ticket", "TEST-1", "--allow-context-change",
		"ctx", "use", "weak",
	)
	if apperrors.AsAppError(err).Code != apperrors.CodeAuthorizationRequired {
		t.Fatalf("ctx use error = %#v", err)
	}
	cfg, loadErr := dbgovctx.Load()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if cfg.CurrentContext != "locked" {
		t.Fatalf("current context downgraded to %q", cfg.CurrentContext)
	}
}

func TestContextUseWithoutCurrentUsesTargetPolicy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	configPath := filepath.Join(home, "config.yaml")
	dbgovctx.SetConfigPath(configPath)
	t.Cleanup(func() { dbgovctx.SetConfigPath("") })
	actual, err := resolveTrustedOperator()
	if err != nil {
		t.Fatal(err)
	}
	if err := dbgovctx.SetContext("locked", testContextWithRoles(map[string]string{actual: safety.RoleReader})); err != nil {
		t.Fatal(err)
	}

	_, err = executeCommandForTest(
		"--config", configPath,
		"--yes", "--ticket", "TEST-1", "--allow-context-change",
		"ctx", "use", "locked",
	)
	if apperrors.AsAppError(err).Code != apperrors.CodeAuthorizationRequired {
		t.Fatalf("ctx use error = %#v", err)
	}
	cfg, loadErr := dbgovctx.Load()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if cfg.CurrentContext != "" {
		t.Fatalf("target policy bypass set current context to %q", cfg.CurrentContext)
	}
}

func TestContextUseCurrentStillRequiresR3AndAudits(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	configPath := filepath.Join(home, "config.yaml")
	dbgovctx.SetConfigPath(configPath)
	t.Cleanup(func() { dbgovctx.SetConfigPath("") })
	actual, err := resolveTrustedOperator()
	if err != nil {
		t.Fatal(err)
	}
	ctx := testContextWithRoles(map[string]string{actual: safety.RoleAdmin})
	if err := dbgovctx.SetContext("current", ctx); err != nil {
		t.Fatal(err)
	}
	if err := dbgovctx.UseContext("current"); err != nil {
		t.Fatal(err)
	}

	if _, err := executeCommandForTest("--config", configPath, "ctx", "use", "current"); apperrors.AsAppError(err).Code != apperrors.CodeAuthorizationRequired {
		t.Fatalf("same-context use without R3 authorization = %#v", err)
	}
	_, err = executeCommandForTest(
		"--config", configPath,
		"--yes", "--ticket", "TEST-1", "--allow-context-change",
		"ctx", "use", "current",
	)
	if err != nil {
		t.Fatalf("authorized same-context use error = %v", err)
	}
	event := lastAuditEvent(t, home)
	if event.EventType != dbgaudit.EventTypeContextUse || event.Operator != actual || event.Risk != "R3" || event.Context.Name != "current" {
		t.Fatalf("same-context use audit event = %+v", event)
	}
}

func TestContextReplacementUsesPreChangeTicketPattern(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	configPath := filepath.Join(home, "config.yaml")
	dbgovctx.SetConfigPath(configPath)
	t.Cleanup(func() { dbgovctx.SetConfigPath("") })
	original := testContextWithRoles(nil)
	original.TicketPattern = `^SEC-[0-9]+$`
	if err := dbgovctx.SetContext("prod", original); err != nil {
		t.Fatal(err)
	}

	_, err := executeCommandForTest(
		"--config", configPath,
		"--yes", "--ticket", "WRONG-1", "--allow-context-change",
		"ctx", "set", "prod", "--host", "changed.example",
	)
	if apperrors.AsAppError(err).Code != apperrors.CodeAuthorizationRequired {
		t.Fatalf("ctx set error = %#v", err)
	}
	cfg, loadErr := dbgovctx.Load()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if cfg.Contexts["prod"].Host != original.Host || cfg.Contexts["prod"].TicketPattern != original.TicketPattern {
		t.Fatalf("incoming context bypassed old ticket policy: %+v", cfg.Contexts["prod"])
	}
}

func TestContextImportForceUsesTargetPreChangePolicy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	configPath := filepath.Join(home, "config.yaml")
	dbgovctx.SetConfigPath(configPath)
	t.Cleanup(func() { dbgovctx.SetConfigPath("") })
	actual, err := resolveTrustedOperator()
	if err != nil {
		t.Fatal(err)
	}
	if err := dbgovctx.SetContext("current", testContextWithRoles(map[string]string{actual: safety.RoleAdmin})); err != nil {
		t.Fatal(err)
	}
	target := testContextWithRoles(map[string]string{actual: safety.RoleReader})
	if err := dbgovctx.SetContext("target", target); err != nil {
		t.Fatal(err)
	}
	if err := dbgovctx.UseContext("current"); err != nil {
		t.Fatal(err)
	}
	importPath := writeTestFile(t, home, "target.yaml", `apiVersion: dbgov-cli.io/ctx-export/v1
name: target
context:
    server: mysql://changed.example:3306
    engine: mysql
    host: changed.example
    port: 3306
`)

	_, err = executeCommandForTest(
		"--config", configPath,
		"--yes", "--ticket", "TEST-1", "--allow-context-change",
		"ctx", "import", "-f", importPath, "--force",
	)
	if apperrors.AsAppError(err).Code != apperrors.CodeAuthorizationRequired {
		t.Fatalf("ctx import error = %#v", err)
	}
	cfg, loadErr := dbgovctx.Load()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if cfg.Contexts["target"].Host != target.Host {
		t.Fatalf("import used current policy instead of target policy: %+v", cfg.Contexts["target"])
	}
}

func TestContextImportValidatesEntireDocumentBeforeAuthorization(t *testing.T) {
	base := `apiVersion: dbgov-cli.io/ctx-export/v1
name: imported
context:
    engine: mysql
    host: 127.0.0.1
    port: 3306
`
	cases := map[string]string{
		"unknown field":          base + "    proteced: true\n",
		"invalid role":           base + "    roles:\n        alice: owner\n",
		"invalid ticket pattern": base + "    ticketPattern: '['\n",
		"multiple documents":     base + "---\napiVersion: dbgov-cli.io/ctx-export/v1\nname: second\ncontext: {}\n",
	}
	for name, document := range cases {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			configPath := filepath.Join(home, "config.yaml")
			importPath := writeTestFile(t, home, "invalid.yaml", document)

			_, err := executeCommandForTest("--config", configPath, "ctx", "import", "-f", importPath)
			if apperrors.AsAppError(err).Code != apperrors.CodeUsageError {
				t.Fatalf("import error = %#v", err)
			}
			dbgovctx.SetConfigPath(configPath)
			t.Cleanup(func() { dbgovctx.SetConfigPath("") })
			cfg, loadErr := dbgovctx.Load()
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			if len(cfg.Contexts) != 0 {
				t.Fatalf("invalid import wrote contexts: %+v", cfg.Contexts)
			}
		})
	}
}

func TestRoleChangeUsesTargetPreChangePolicy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	configPath := filepath.Join(home, "config.yaml")
	dbgovctx.SetConfigPath(configPath)
	t.Cleanup(func() { dbgovctx.SetConfigPath("") })
	actual, err := resolveTrustedOperator()
	if err != nil {
		t.Fatal(err)
	}
	target := testContextWithRoles(map[string]string{actual: safety.RoleReader})
	if err := dbgovctx.SetContext("target", target); err != nil {
		t.Fatal(err)
	}

	_, err = executeCommandForTest(
		"--config", configPath,
		"--yes", "--ticket", "TEST-1", "--allow-role-change",
		"ctx", "role", "set", "target", "--target-operator", actual, "--role", "admin",
	)
	if apperrors.AsAppError(err).Code != apperrors.CodeAuthorizationRequired {
		t.Fatalf("role set error = %#v", err)
	}
	cfg, loadErr := dbgovctx.Load()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if cfg.Contexts["target"].Roles[actual] != safety.RoleReader {
		t.Fatalf("role change authorized by incoming role: %+v", cfg.Contexts["target"].Roles)
	}
}

func TestContextControlDryRunsHaveNoWritesOrAuthorization(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DBGOV_MASTER_PASSWORD", "test-passphrase")
	configPath := filepath.Join(home, "config.yaml")
	dbgovctx.SetConfigPath(configPath)
	t.Cleanup(func() { dbgovctx.SetConfigPath("") })
	actual, err := resolveTrustedOperator()
	if err != nil {
		t.Fatal(err)
	}
	locked := testContextWithRoles(map[string]string{actual: safety.RoleReader})
	locked.Password = "literal-secret"
	if err := dbgovctx.SetContext("locked", locked); err != nil {
		t.Fatal(err)
	}

	importPath := writeTestFile(t, home, "ctx-import.yaml", `apiVersion: dbgov-cli.io/ctx-export/v1
name: imported
context:
    server: mysql://127.0.0.1:3306
    engine: mysql
    host: 127.0.0.1
    port: 3306
`)
	commands := [][]string{
		{"--config", configPath, "-o", "json", "ctx", "set", "new", "--host", "127.0.0.1", "--password", "secret", "--credential-backend", "encrypted-file", "--dry-run"},
		{"--config", configPath, "-o", "json", "ctx", "use", "locked", "--dry-run"},
		{"--config", configPath, "-o", "json", "ctx", "delete", "locked", "--dry-run"},
		{"--config", configPath, "-o", "json", "ctx", "role", "set", "locked", "--target-operator", "someone", "--role", "admin", "--dry-run"},
		{"--config", configPath, "-o", "json", "ctx", "import", "-f", importPath, "--dry-run"},
		{"--config", configPath, "-o", "json", "ctx", "migrate-credentials", "--to", "encrypted-file", "--dry-run"},
	}
	for _, args := range commands {
		out, runErr := executeCommandForTest(args...)
		if runErr != nil {
			t.Fatalf("dry-run %v error = %v", args, runErr)
		}
		if !strings.Contains(out, `"dryRun": true`) {
			t.Fatalf("dry-run %v output = %s", args, out)
		}
	}

	cfg, err := dbgovctx.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Contexts) != 1 || cfg.CurrentContext != "" || cfg.Contexts["locked"].Password != "literal-secret" || len(cfg.Contexts["locked"].Roles) != 1 {
		t.Fatalf("dry-run mutated contexts: %+v", cfg.Contexts)
	}
	if _, err := os.Stat(filepath.Join(home, ".dbgov", "credentials.enc")); !os.IsNotExist(err) {
		t.Fatalf("dry-run wrote credential store: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".dbgov", "audit.log")); !os.IsNotExist(err) {
		t.Fatalf("dry-run wrote audit log: %v", err)
	}
}

func TestCredentialMigrationAuthorizesAllBeforeAnyWrite(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DBGOV_MASTER_PASSWORD", "test-passphrase")
	configPath := filepath.Join(home, "config.yaml")
	dbgovctx.SetConfigPath(configPath)
	t.Cleanup(func() { dbgovctx.SetConfigPath("") })
	actual, err := resolveTrustedOperator()
	if err != nil {
		t.Fatal(err)
	}
	admin := testContextWithRoles(map[string]string{actual: safety.RoleAdmin})
	admin.Password = "admin-secret"
	reader := testContextWithRoles(map[string]string{actual: safety.RoleReader})
	reader.Password = "reader-secret"
	if err := dbgovctx.SetContext("a-admin", admin); err != nil {
		t.Fatal(err)
	}
	if err := dbgovctx.SetContext("z-reader", reader); err != nil {
		t.Fatal(err)
	}

	_, err = executeCommandForTest(
		"--config", configPath,
		"--yes", "--ticket", "TEST-1", "--allow-context-change",
		"ctx", "migrate-credentials", "--to", "encrypted-file",
	)
	if apperrors.AsAppError(err).Code != apperrors.CodeAuthorizationRequired {
		t.Fatalf("migration error = %#v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".dbgov", "credentials.enc")); !os.IsNotExist(err) {
		t.Fatalf("migration wrote credentials before all authorization passed: %v", err)
	}
	cfg, loadErr := dbgovctx.Load()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if cfg.Contexts["a-admin"].Password != "admin-secret" || cfg.Contexts["z-reader"].Password != "reader-secret" {
		t.Fatalf("migration partially updated contexts: %+v", cfg.Contexts)
	}
}

func testContextWithRoles(roles map[string]string) dbgovctx.Context {
	return dbgovctx.Context{
		Base: corectx.Base{
			Server:     "mysql://127.0.0.1:3306",
			Roles:      roles,
			OTLPRedact: true,
		},
		Engine: "mysql",
		Host:   "127.0.0.1",
		Port:   3306,
	}
}
