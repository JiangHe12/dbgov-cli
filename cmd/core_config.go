package cmd

import (
	"github.com/JiangHe12/opskit-core/v2/apperrors"
	"github.com/JiangHe12/opskit-core/v2/audit"
	"github.com/JiangHe12/opskit-core/v2/credstore"
	corectx "github.com/JiangHe12/opskit-core/v2/ctx"
	"github.com/JiangHe12/opskit-core/v2/lockfile"
	"github.com/JiangHe12/opskit-core/v2/printer"
	"github.com/JiangHe12/opskit-core/v2/safety"
	"github.com/JiangHe12/opskit-core/v2/telemetry"

	"github.com/JiangHe12/dbgov-cli/internal/dbgovctx"
)

func init() {
	apperrors.Configure(apperrors.Options{
		APIVersion: "dbgov-cli.io/v1",
		Suggestions: map[apperrors.ErrorCode]string{
			apperrors.CodeCredentialStoreMissing: "Re-run dbgov ctx set with a password, or set DBGOV_PASSWORD.",
		},
	})
	audit.Configure(audit.Config{
		APIVersion:         "dbgov-cli.io/audit/v1",
		ConfigDirName:      ".dbgov",
		PrivateKeyEnvVar:   "DBGOV_AUDIT_PRIVATE_KEY",
		TargetTypeJSONName: "objectType",
	})
	corectx.Configure(corectx.Options{APIVersion: dbgovctx.SupportedContextAPIVersion, ConfigDirName: ".dbgov"})
	lockfile.Configure(lockfile.Options{TimeoutEnvVar: "DBGOV_LOCK_TIMEOUT"})
	printer.Configure(printer.Options{APIVersion: "dbgov-cli.io/v1", JSONEnvelopeByDefault: true})
	safety.Configure(safety.Config{
		Prompt:                   "Proceed with database operation? [y/N] ",
		RoleAssignmentHintFormat: "assign one with: dbgov ctx role set <context> --target-operator=%s --role=%s",
	})
	telemetry.Configure(telemetry.Config{
		ServiceName:      "dbgov",
		AttributePrefix:  "dbgov",
		MetricNamePrefix: "dbgov",
	})
	//nolint:gosec // DBGOV_MASTER_PASSWORD and DBGOV001 are configuration identifiers, not embedded credentials.
	credstore.Configure(credstore.Options{
		MasterPasswordEnv:  "DBGOV_MASTER_PASSWORD",
		PromptName:         "dbgov",
		ConfigDirName:      ".dbgov",
		KeychainService:    "dbgov",
		EncryptedFileMagic: []byte("DBGOV001"),
	})
}
