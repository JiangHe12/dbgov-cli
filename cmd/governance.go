package cmd

import (
	"context"
	"path/filepath"

	"github.com/JiangHe12/opskit-core/apperrors"
	coreaudit "github.com/JiangHe12/opskit-core/audit"

	dbgaudit "github.com/JiangHe12/dbgov-cli/internal/audit"
	dbbackend "github.com/JiangHe12/dbgov-cli/internal/backend"
	"github.com/JiangHe12/dbgov-cli/internal/backend/fake"
	"github.com/JiangHe12/dbgov-cli/internal/backend/mysql"
	"github.com/JiangHe12/dbgov-cli/internal/safety"
	"github.com/JiangHe12/dbgov-cli/internal/schema"
	dbgsnapshot "github.com/JiangHe12/dbgov-cli/internal/snapshot"
)

type backendOptions struct {
	Fake bool
}

type contextMeta struct {
	Name          string
	Env           string
	Protected     bool
	Database      string
	TicketPattern string
	Roles         map[string]string
}

var newFakeBackend = func() dbbackend.Backend { return fake.New() }

func buildBackend(f *cliFlags, opts backendOptions) (dbbackend.Backend, contextMeta, error) {
	if opts.Fake {
		meta := contextMeta{Name: "fake", Database: "fake"}
		if ctx, name := selectedContext(f); ctx != nil {
			meta = contextMeta{Name: name, Env: ctx.Env, Protected: ctx.Protected, Database: ctx.Database, TicketPattern: ctx.TicketPattern, Roles: ctx.Roles}
		}
		return newFakeBackend(), meta, nil
	}
	ctx, name := selectedContext(f)
	if ctx == nil {
		return nil, contextMeta{}, errNoContext()
	}
	if ctx.Engine != "mysql" {
		return nil, contextMeta{}, errUnsupportedEngine(ctx.Engine)
	}
	password, err := ctx.ResolvePasswordContext(commandContext(f), name)
	if err != nil {
		return nil, contextMeta{}, err
	}
	dsn := ctx.Username + ":" + password + "@tcp(" + ctx.Host + ":" + itoa(ctx.Port) + ")/" + ctx.Database
	backend, err := mysql.New(dsn, ctx.Database)
	if err != nil {
		return nil, contextMeta{}, err
	}
	return backend, contextMeta{Name: name, Env: ctx.Env, Protected: ctx.Protected, Database: ctx.Database, TicketPattern: ctx.TicketPattern, Roles: ctx.Roles}, nil
}

func authorizeRead(f *cliFlags) error {
	return safety.Authorize(safety.R0, safety.Options{Operator: currentOperator(f)})
}

func authorizeWrite(f *cliFlags, base safety.Risk, meta contextMeta, requiredAllows []safety.AllowFlag, granted map[safety.AllowFlag]bool) error {
	effective := safety.EffectiveRisk(base, safety.ContextMeta{
		Name:          meta.Name,
		Env:           meta.Env,
		Protected:     meta.Protected,
		TicketPattern: meta.TicketPattern,
		Roles:         meta.Roles,
	})
	// External ticket validators are intentionally not wired in this phase;
	// only the context regex TicketPattern is passed to core safety.
	return safety.Authorize(effective, safety.Options{
		Yes:                f.Yes,
		NonInteractive:     f.NonInteractive,
		Ticket:             f.Ticket,
		TicketPattern:      meta.TicketPattern,
		RequiredAllowFlags: requiredAllows,
		GrantedAllowFlags:  granted,
		Roles:              meta.Roles,
		Operator:           currentOperator(f),
	})
}

func effectiveRiskLabel(baseLabel string, meta contextMeta) string {
	return safetyRiskLabel(safety.EffectiveRisk(safetyRisk(baseLabel), safety.ContextMeta{
		Name:          meta.Name,
		Env:           meta.Env,
		Protected:     meta.Protected,
		TicketPattern: meta.TicketPattern,
		Roles:         meta.Roles,
	}))
}

func emitAudit(f *cliFlags, event dbgaudit.Event, opErr error) {
	event.Statement = redactSQL(event.Statement)
	event.FailedStatement = redactSQL(event.FailedStatement)
	if opErr != nil {
		event.Status = dbgaudit.StatusFailed
		appErr := apperrors.AsAppError(opErr)
		event.Error = &dbgaudit.ErrorInfo{Code: string(appErr.Code), Message: appErr.Message}
	}
	if event.Ticket == "" {
		event.Ticket = f.Ticket
	}
	path, err := coreaudit.DefaultPath()
	if err != nil {
		warnAuditFailure(f, err)
		return
	}
	if err := coreaudit.AppendRecord(path, event, coreaudit.Options{}); err != nil {
		warnAuditFailure(f, err)
	}
}

func snapshotBaseDir() (string, error) {
	path, err := coreaudit.DefaultPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(path), "snapshots"), nil
}

func captureSchemaSnapshot(f *cliFlags, b interface {
	TableDDL(context.Context, string) (string, error)
}, current schema.Schema, meta contextMeta, command string,
) (string, error) {
	baseDir, err := snapshotBaseDir()
	if err != nil {
		return "", err
	}
	tables := make(map[string]string, len(current.Tables))
	for _, table := range sortedTableNames(current) {
		ddl, err := b.TableDDL(commandContext(f), table)
		if err != nil {
			return "", err
		}
		tables[table] = ddl
	}
	return dbgsnapshot.Capture(baseDir, dbgsnapshot.Meta{
		Operator: currentOperator(f),
		Command:  command,
		Ticket:   f.Ticket,
		Context:  meta.Name,
	}, tables)
}

func auditContext(meta contextMeta) dbgaudit.Context {
	return dbgaudit.Context{Name: meta.Name, Env: meta.Env, Protected: meta.Protected}
}

func auditTarget(meta contextMeta, objectType, object string) dbgaudit.Target {
	return dbgaudit.Target{Database: meta.Database, ObjectType: objectType, Object: object}
}

func ping(ctx context.Context, backend dbbackend.Backend) error {
	return backend.Ping(ctx)
}
