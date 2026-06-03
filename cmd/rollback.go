package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	dbgaudit "github.com/JiangHe12/dbgov-cli/internal/audit"
	dbgsnapshot "github.com/JiangHe12/dbgov-cli/internal/snapshot"
	"github.com/JiangHe12/opskit-core/printer"
)

type rollbackListResult struct {
	Snapshots []dbgsnapshot.Meta `json:"snapshots"`
}

func newRollbackCmd(f *cliFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rollback",
		Short: "Inspect schema rollback snapshots",
	}
	cmd.AddCommand(rollbackListCmd(f))
	return cmd
}

func rollbackListCmd(f *cliFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List schema snapshots",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRollbackList(f)
		},
	}
}

func runRollbackList(f *cliFlags) error {
	if err := authorizeRead(f); err != nil {
		return err
	}
	baseDir, err := snapshotBaseDir()
	if err != nil {
		event := rollbackListAuditEvent(f)
		emitAudit(f, event, err)
		return err
	}
	metas, err := dbgsnapshot.List(baseDir)
	event := rollbackListAuditEvent(f)
	emitAudit(f, event, err)
	if err != nil {
		return err
	}
	return printRollbackList(f, rollbackListResult{Snapshots: metas})
}

func rollbackListAuditEvent(f *cliFlags) dbgaudit.Event {
	return dbgaudit.New(dbgaudit.EventTypeRollback, currentOperator(f), dbgaudit.Context{}, dbgaudit.Target{ObjectType: "rollback", Object: "list"})
}

func printRollbackList(f *cliFlags, result rollbackListResult) error {
	if result.Snapshots == nil {
		result.Snapshots = []dbgsnapshot.Meta{}
	}
	p := newPrinter(f)
	if f.Output == "json" {
		return p.JSONDataEnvelope(printer.JSONDataEnvelope{Kind: "RollbackSnapshotList", Data: result})
	}
	rows := make([][]string, 0, len(result.Snapshots))
	for _, meta := range result.Snapshots {
		rows = append(rows, []string{
			meta.ID,
			meta.Timestamp.Format("2006-01-02T15:04:05Z"),
			meta.Operator,
			meta.Command,
			meta.Ticket,
			fmt.Sprint(meta.TableCount),
		})
	}
	p.Table([]string{"ID", "TIMESTAMP", "OPERATOR", "COMMAND", "TICKET", "TABLES"}, rows)
	return nil
}
