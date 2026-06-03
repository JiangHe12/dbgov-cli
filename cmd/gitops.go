package cmd

import (
	"github.com/spf13/cobra"

	dbgaudit "github.com/JiangHe12/dbgov-cli/internal/audit"
	"github.com/JiangHe12/dbgov-cli/internal/safety"
	"github.com/JiangHe12/dbgov-cli/internal/schema"
)

type exportOptions struct {
	dir  string
	fake bool
}

type importOptions struct {
	dir              string
	fake             bool
	dryRun           bool
	allowDestructive bool
}

func newExportCmd(f *cliFlags) *cobra.Command {
	var opts exportOptions
	cmd := &cobra.Command{
		Use:   "export --dir ./schema",
		Short: "Export current schema DDL to a directory",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExport(f, opts)
		},
	}
	cmd.Flags().StringVar(&opts.dir, "dir", "", "Directory to write schema SQL files")
	cmd.Flags().BoolVar(&opts.fake, "fake", false, "Use in-memory fake backend")
	_ = cmd.MarkFlagRequired("dir")
	return cmd
}

func newImportCmd(f *cliFlags) *cobra.Command {
	var opts importOptions
	cmd := &cobra.Command{
		Use:   "import <schema-dir>",
		Short: "Import desired schema from a directory",
		Args:  requireExactArgs("import", 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.dir = args[0]
			return runImport(f, opts)
		},
	}
	cmd.Flags().BoolVar(&opts.fake, "fake", false, "Use in-memory fake backend")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "Print the plan without executing DDL")
	cmd.Flags().BoolVar(&opts.allowDestructive, "allow-destructive", false, "Allow destructive schema changes")
	return cmd
}

func runExport(f *cliFlags, opts exportOptions) error {
	if err := authorizeRead(f); err != nil {
		return err
	}
	b, meta, err := buildBackend(f, backendOptions{Fake: opts.fake})
	if err != nil {
		return err
	}
	current, err := b.IntrospectSchema(commandContext(f))
	if err != nil {
		event := dbgaudit.New(dbgaudit.EventTypeExport, currentOperator(f), auditContext(meta), auditTarget(meta, "schema", "export"))
		event.Risk = "R0"
		emitAudit(f, event, err)
		return err
	}
	result, err := dumpSchema(commandContext(f), b, current, opts.dir)
	event := dbgaudit.New(dbgaudit.EventTypeExport, currentOperator(f), auditContext(meta), auditTarget(meta, "schema", "export"))
	event.Risk = "R0"
	emitAudit(f, event, err)
	if err != nil {
		return err
	}
	return printSchemaDump(f, result)
}

func runImport(f *cliFlags, opts importOptions) error {
	desired, err := schema.LoadDesiredDir(opts.dir)
	if err != nil {
		return err
	}
	b, meta, err := buildBackend(f, backendOptions{Fake: opts.fake})
	if err != nil {
		return err
	}
	current, err := b.IntrospectSchema(commandContext(f))
	if err != nil {
		event := newImportAuditEvent(f, meta, schemaPlan{})
		emitAudit(f, event, err)
		return err
	}
	plan, err := buildSchemaPlan(b, current, desired)
	if err != nil {
		event := newImportAuditEvent(f, meta, plan)
		emitAudit(f, event, err)
		return err
	}
	if opts.dryRun {
		event := newImportAuditEvent(f, meta, plan)
		event.DryRun = true
		emitAudit(f, event, nil)
		return printSchemaPlan(f, plan)
	}

	var requiredAllows []safety.AllowFlag
	granted := map[safety.AllowFlag]bool{}
	if plan.Destructive {
		requiredAllows = []safety.AllowFlag{safety.AllowDestructive}
		if opts.allowDestructive {
			granted[safety.AllowDestructive] = true
		}
	}
	if err := authorizeWrite(f, safetyRisk(plan.OverallRisk), meta, requiredAllows, granted); err != nil {
		event := newImportAuditEvent(f, meta, plan)
		event.Status = dbgaudit.StatusDenied
		setAuditError(&event, err)
		emitAudit(f, event, nil)
		return err
	}

	statements := schemaPlanStatements(plan)
	executed, err := b.ExecDDL(commandContext(f), statements)
	event := newImportAuditEvent(f, meta, plan)
	event.Executed = executed
	if err != nil && executed < len(statements) {
		event.FailedStatement = statements[executed]
	}
	emitAudit(f, event, err)
	if err != nil {
		return err
	}
	return printSchemaPlan(f, plan)
}

func newImportAuditEvent(f *cliFlags, meta contextMeta, plan schemaPlan) dbgaudit.Event {
	event := dbgaudit.New(dbgaudit.EventTypeImport, currentOperator(f), auditContext(meta), auditTarget(meta, "schema", "import"))
	event.Risk = plan.OverallRisk
	event.Destructive = plan.Destructive
	event.Statement = schemaPlanSQL(plan)
	return event
}
