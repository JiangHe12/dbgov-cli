package cmd

import (
	"github.com/JiangHe12/opskit-core/apperrors"
	"github.com/JiangHe12/opskit-core/audit"
	"github.com/JiangHe12/opskit-core/credstore"
	corectx "github.com/JiangHe12/opskit-core/ctx"
	"github.com/JiangHe12/opskit-core/lockfile"
	"github.com/JiangHe12/opskit-core/printer"
	"github.com/JiangHe12/opskit-core/safety"
	"github.com/JiangHe12/opskit-core/telemetry"
)

func init() {
	apperrors.Configure(apperrors.Options{
		APIVersion: "dbgov.io/v1",
		Suggestions: map[apperrors.ErrorCode]string{
			apperrors.CodeCredentialStoreMissing: "Re-run dbgov ctx set with a password, or set DBGOV_PASSWORD.",
		},
	})
	audit.Configure(audit.Config{
		APIVersion:         "dbgov.io/audit/v1",
		ConfigDirName:      ".dbgov",
		PrivateKeyEnvVar:   "DBGOV_AUDIT_PRIVATE_KEY",
		TargetTypeJSONName: "objectType",
	})
	corectx.Configure(corectx.Options{APIVersion: "dbgov.io/context/v1", ConfigDirName: ".dbgov"})
	lockfile.Configure(lockfile.Options{TimeoutEnvVar: "DBGOV_LOCK_TIMEOUT"})
	printer.Configure(printer.Options{APIVersion: "dbgov.io/v1"})
	safety.Configure(safety.Config{
		Prompt:                   "Proceed with database operation? [y/N] ",
		OperatorEnvVar:           "DBGOV_OPERATOR",
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
