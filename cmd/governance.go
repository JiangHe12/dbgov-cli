package cmd

import (
	"context"

	dbgaudit "github.com/JiangHe12/dbgov-cli/internal/audit"
	dbbackend "github.com/JiangHe12/dbgov-cli/internal/backend"
	"github.com/JiangHe12/dbgov-cli/internal/backend/fake"
	"github.com/JiangHe12/dbgov-cli/internal/backend/mysql"
	"github.com/JiangHe12/dbgov-cli/internal/safety"
	"github.com/JiangHe12/opskit-core/apperrors"
	coreaudit "github.com/JiangHe12/opskit-core/audit"
)

type backendOptions struct {
	Fake bool
}

type contextMeta struct {
	Name      string
	Env       string
	Protected bool
	Database  string
}

func buildBackend(f *cliFlags, opts backendOptions) (dbbackend.Backend, contextMeta, error) {
	if opts.Fake {
		return fake.New(), contextMeta{Name: "fake", Database: "fake"}, nil
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
	return backend, contextMeta{Name: name, Env: ctx.Env, Protected: ctx.Protected, Database: ctx.Database}, nil
}

func authorizeRead(f *cliFlags) error {
	return safety.Authorize(safety.R0, safety.Options{Operator: currentOperator(f)})
}

func emitAudit(f *cliFlags, event dbgaudit.Event, opErr error) {
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
		return
	}
	_ = coreaudit.AppendRecord(path, event, coreaudit.Options{})
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
