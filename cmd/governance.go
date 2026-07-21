package cmd

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/JiangHe12/opskit-core/v2/apperrors"
	coreaudit "github.com/JiangHe12/opskit-core/v2/audit"

	dbgaudit "github.com/JiangHe12/dbgov-cli/internal/audit"
	dbbackend "github.com/JiangHe12/dbgov-cli/internal/backend"
	"github.com/JiangHe12/dbgov-cli/internal/backend/fake"
	"github.com/JiangHe12/dbgov-cli/internal/backend/mysql"
	"github.com/JiangHe12/dbgov-cli/internal/backend/postgres"
	"github.com/JiangHe12/dbgov-cli/internal/dbgovctx"
	"github.com/JiangHe12/dbgov-cli/internal/safety"
	"github.com/JiangHe12/dbgov-cli/internal/schema"
	dbgsnapshot "github.com/JiangHe12/dbgov-cli/internal/snapshot"
)

type backendOptions struct {
	Fake bool
}

type backendCloseSemantics bool

const (
	backendCloseRead     backendCloseSemantics = false
	backendCloseMutation backendCloseSemantics = true
)

type contextMeta struct {
	Name          string
	Env           string
	Protected     bool
	Engine        string
	Host          string
	Port          int
	Database      string
	TicketPattern string
	Roles         map[string]string
}

var (
	newFakeBackend     = func() dbbackend.Backend { return fake.New() }
	newPostgresBackend = func(dsn, database string) (dbbackend.Backend, error) {
		return postgres.New(dsn, database)
	}
)

func buildBackend(f *cliFlags, opts backendOptions) (dbbackend.Backend, contextMeta, error) {
	if opts.Fake {
		meta := contextMeta{Name: "fake", Engine: "mysql", Host: "fake", Database: "fake"}
		if ctx, name := selectedContext(f); ctx != nil {
			meta = contextMeta{Name: name, Env: ctx.Env, Protected: ctx.Protected, Engine: ctx.Engine, Host: ctx.Host, Port: ctx.Port, Database: ctx.Database, TicketPattern: ctx.TicketPattern, Roles: ctx.Roles}
		}
		return newFakeBackend(), meta, nil
	}
	ctx, name := selectedContext(f)
	if ctx == nil {
		return nil, contextMeta{}, errNoContext()
	}
	password, err := ctx.ResolvePasswordContext(commandContext(f), name)
	if err != nil {
		return nil, contextMeta{}, err
	}
	var backend dbbackend.Backend
	switch ctx.Engine {
	case "mysql":
		dsn := ctx.Username + ":" + password + "@tcp(" + ctx.Host + ":" + itoa(ctx.Port) + ")/" + ctx.Database
		backend, err = mysql.New(dsn, ctx.Database)
	case "postgres":
		backend, err = newPostgresBackend(postgresDSN(ctx.Username, password, ctx.Host, ctx.Port, ctx.Database), ctx.Database)
	default:
		return nil, contextMeta{}, errUnsupportedEngine(ctx.Engine)
	}
	if err != nil {
		return nil, contextMeta{}, err
	}
	return backend, contextMeta{Name: name, Env: ctx.Env, Protected: ctx.Protected, Engine: ctx.Engine, Host: ctx.Host, Port: ctx.Port, Database: ctx.Database, TicketPattern: ctx.TicketPattern, Roles: ctx.Roles}, nil
}

func finishBackendClose(backend dbbackend.Backend, resultErr *error, semantics backendCloseSemantics) {
	if backend == nil {
		return
	}
	closeErr := backend.Close()
	if closeErr == nil || resultErr == nil || *resultErr != nil {
		return
	}
	if semantics == backendCloseMutation {
		*resultErr = apperrors.New(
			apperrors.CodePartialFailure,
			"database operation completed, but closing the database backend failed",
			closeErr,
		).WithSuggestion("Do not retry the mutation; verify the completed operation and investigate the local connection cleanup failure.")
		return
	}
	*resultErr = apperrors.New(apperrors.CodeBackendError, "close database backend", closeErr)
}

func postgresDSN(username, password, host string, port int, database string) string {
	dsn := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(username, password),
		Host:   net.JoinHostPort(host, itoa(port)),
		Path:   "/" + database,
	}
	return dsn.String()
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

func authorizeContextControl(f *cliFlags, policyName string, policy dbgovctx.Context, required safety.AllowFlag, granted bool) error {
	return authorizeWrite(f, safety.R3, contextMeta{
		Name:          policyName,
		Env:           policy.Env,
		Protected:     policy.Protected,
		TicketPattern: policy.TicketPattern,
		Roles:         policy.Roles,
	}, []safety.AllowFlag{required}, map[safety.AllowFlag]bool{required: granted})
}

func contextPreChangePolicy(cfg *dbgovctx.Config, target string) (string, dbgovctx.Context, error) {
	if policy, ok := cfg.Contexts[target]; ok {
		return target, policy, nil
	}
	if cfg.CurrentContext == "" {
		return "", dbgovctx.Context{}, nil
	}
	policy, ok := cfg.Contexts[cfg.CurrentContext]
	if !ok {
		return "", dbgovctx.Context{}, apperrors.New(
			apperrors.CodeAuthorizationRequired,
			fmt.Sprintf("current context %q has no persisted policy; refusing control-plane change", cfg.CurrentContext),
			nil,
		)
	}
	return cfg.CurrentContext, policy, nil
}

func verifyContextPreChangePolicy(cfg *dbgovctx.Config, target, expectedName string, expected dbgovctx.Context) error {
	actualName, actual, err := contextPreChangePolicy(cfg, target)
	if err != nil {
		return err
	}
	if actualName != expectedName || !reflect.DeepEqual(actual, expected) {
		return contextPolicyChangedError()
	}
	return nil
}

func verifyPersistedContext(cfg *dbgovctx.Config, name string, expected dbgovctx.Context) error {
	actual, ok := cfg.Contexts[name]
	if !ok || !reflect.DeepEqual(actual, expected) {
		return contextPolicyChangedError()
	}
	return nil
}

func contextPolicyChangedError() error {
	return apperrors.New(
		apperrors.CodeAuthorizationRequired,
		"context policy changed during authorization; review the new policy and retry",
		nil,
	)
}

type controlChangePreview struct {
	Action         string   `json:"action"`
	Contexts       []string `json:"contexts,omitempty"`
	Context        string   `json:"context,omitempty"`
	TargetOperator string   `json:"targetOperator,omitempty"`
	Role           string   `json:"role,omitempty"`
	Backend        string   `json:"backend,omitempty"`
	DryRun         bool     `json:"dryRun"`
}

func printControlChangePreview(f *cliFlags, preview controlChangePreview) error {
	preview.DryRun = true
	p := newPrinter(f)
	if f.Output == "json" {
		return p.JSONData("ControlChangePreview", preview)
	}
	target := preview.Context
	if target == "" && len(preview.Contexts) > 0 {
		target = fmt.Sprintf("%d context(s)", len(preview.Contexts))
	}
	return p.Info(fmt.Sprintf("(dry-run) %s %s", preview.Action, target))
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
	path, err = absoluteCleanPath(path)
	if err != nil {
		warnAuditFailure(f, err)
		return
	}
	if err := validateAuditEvidencePath(path); err != nil {
		warnAuditFailure(f, err)
		return
	}
	if err := ensurePrivateMutationDirectory(filepath.Dir(path)); err != nil {
		warnAuditFailure(f, err)
		return
	}
	if err := appendQueuedAuditEvent(f, path, event); err != nil {
		warnAuditFailure(f, err)
	}
}

func snapshotBaseDir() (string, error) {
	path, err := coreaudit.DefaultPath()
	if err != nil {
		return "", err
	}
	path, err = absoluteCleanPath(path)
	if err != nil {
		return "", err
	}
	if err := validateAuditEvidencePath(path); err != nil {
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
	snapshotID, data, err := dbgsnapshot.Prepare(dbgsnapshot.Meta{
		Operator: currentOperator(f),
		Command:  command,
		Ticket:   f.Ticket,
		Context:  meta.Name,
		Target:   snapshotTarget(meta),
	}, tables)
	if err != nil {
		return "", err
	}
	if err := ensurePrivateMutationDirectory(baseDir); err != nil {
		return "", err
	}
	if _, err := writePrivateEvidenceFile(baseDir, snapshotID+".json", data); err != nil {
		return "", err
	}
	return snapshotID, nil
}

func snapshotTarget(meta contextMeta) *dbgsnapshot.Target {
	schemaName := ""
	if strings.EqualFold(strings.TrimSpace(meta.Engine), "postgres") {
		schemaName = "public"
	}
	return &dbgsnapshot.Target{
		Context:  strings.TrimSpace(meta.Name),
		Engine:   strings.ToLower(strings.TrimSpace(meta.Engine)),
		Host:     strings.ToLower(strings.TrimSpace(meta.Host)),
		Port:     meta.Port,
		Database: strings.TrimSpace(meta.Database),
		Schema:   schemaName,
	}
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
