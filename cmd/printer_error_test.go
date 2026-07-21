package cmd

import (
	"bytes"
	"errors"
	"testing"

	coreaudit "github.com/JiangHe12/opskit-core/v2/audit"
)

var errOutputUnavailable = errors.New("output unavailable")

type failingOutputWriter struct{}

func (failingOutputWriter) Write([]byte) (int, error) {
	return 0, errOutputUnavailable
}

func TestPrinterWriteErrorsPropagateFromCommandRenderers(t *testing.T) {
	tests := []struct {
		name string
		run  func() error
	}{
		{
			name: "success",
			run: func() error {
				return printCredentialMigrationResult(
					&cliFlags{Output: "table", Out: failingOutputWriter{}},
					"encrypted-file",
					1,
				)
			},
		},
		{
			name: "target header",
			run: func() error {
				return printDataExecPlan(
					&cliFlags{Output: "table", Out: failingOutputWriter{}},
					contextMeta{Name: "test", Engine: "mysql"},
					dataExecPlan{},
				)
			},
		},
		{
			name: "table",
			run: func() error {
				return printAuditVerify(
					&cliFlags{Output: "table", Out: failingOutputWriter{}},
					coreaudit.VerifyResult{},
				)
			},
		},
		{
			name: "warning",
			run: func() error {
				return printRollbackResult(
					&cliFlags{Output: "plain", Out: &bytes.Buffer{}, Err: failingOutputWriter{}},
					contextMeta{},
					rollbackResult{Warnings: []string{"warning"}},
				)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.run(); !errors.Is(err, errOutputUnavailable) {
				t.Fatalf("renderer error = %v, want output failure", err)
			}
		})
	}
}
