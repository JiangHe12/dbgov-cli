package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/user"
	"strings"
	"sync"
	"time"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/JiangHe12/opskit-core/v2/apperrors"
	"github.com/JiangHe12/opskit-core/v2/printer"
	"github.com/JiangHe12/opskit-core/v2/telemetry"

	dbgaudit "github.com/JiangHe12/dbgov-cli/internal/audit"
	"github.com/JiangHe12/dbgov-cli/internal/dbgovctx"
)

var auditWarningMu sync.Mutex

type cliFlags struct {
	Output             string
	Debug              bool
	Trace              bool
	NoColor            bool
	Config             string
	Context            string
	Operator           string
	Ticket             string
	Yes                bool
	NonInteractive     bool
	AllowContextChange bool
	AllowContextDelete bool
	AllowRoleChange    bool
	AllowAuditPrune    bool
	Out                io.Writer
	Err                io.Writer
	commandContext     context.Context
	trustedOperator    string
	mutationAudit      *mutationAuditRuntime
	mutationAuditPath  string
	manageTelemetry    bool
	commandName        string
	commandStarted     time.Time
	activeSpan         trace.Span
	telemetryStop      telemetry.ShutdownFunc
	metricsStop        telemetry.ShutdownFunc
	metricAttrs        []attribute.KeyValue
}

var (
	currentOSUser = user.Current
	currentHost   = os.Hostname
	initTelemetry = telemetry.Init
	initMetrics   = telemetry.InitMetrics
	recordCommand = telemetry.RecordCommand
)

func NewRootCmd() *cobra.Command {
	return newRootCmdWith(&cliFlags{Output: "table"})
}

func newRootCmdWith(f *cliFlags) *cobra.Command {
	root := &cobra.Command{
		Use:           "dbgov-cli",
		Short:         "Governed database operations for AI agents",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if err := validateOutputFormat(f.Output); err != nil {
				return err
			}
			applyGlobalFlags(f)
			operator, err := resolveTrustedOperator()
			if err != nil {
				return err
			}
			f.trustedOperator = operator
			f.Out = cmd.OutOrStdout()
			f.Err = cmd.ErrOrStderr()
			if f.Config != "" {
				dbgovctx.SetConfigPath(f.Config)
			}
			ctxMeta, contextName := selectedContext(f)
			env := ""
			protected := false
			if ctxMeta != nil {
				env = ctxMeta.Env
				protected = ctxMeta.Protected
			}
			ticketFingerprint, _ := dbgaudit.Fingerprint("ticket", f.Ticket)
			f.commandContext = cmd.Context()
			if !f.manageTelemetry {
				return nil
			}
			traceEndpoint, metricsEndpoint, insecure := resolveTelemetryConfig(ctxMeta)
			f.telemetryStop = initTelemetry(cmd.Context(), traceEndpoint, insecure, version)
			f.metricsStop = initMetrics(cmd.Context(), metricsEndpoint, insecure, version)
			f.commandName = strings.ReplaceAll(cmd.CommandPath(), " ", ".")
			f.commandStarted = time.Now()
			spanContext, span := telemetry.Tracer().Start(cmd.Context(), f.commandName)
			f.metricAttrs = telemetry.SpanAttributes(currentOperator(f), contextName, env, "", ticketFingerprint, protected, true, "")
			span.SetAttributes(f.metricAttrs...)
			f.activeSpan = span
			f.commandContext = spanContext
			return nil
		},
		PersistentPostRun: func(cmd *cobra.Command, _ []string) {
			_ = cmd
		},
	}
	root.PersistentFlags().StringVarP(&f.Output, "output", "o", "table", "Output format: table | json | plain")
	root.PersistentFlags().BoolVar(&f.Debug, "debug", false, "Enable debug logging")
	root.PersistentFlags().BoolVar(&f.Trace, "trace", false, "Enable trace logging (implies --debug)")
	root.PersistentFlags().BoolVar(&f.NoColor, "no-color", false, "Disable colored output")
	root.PersistentFlags().StringVar(&f.Config, "config", "", "Temporarily override config file path")
	root.PersistentFlags().StringVar(&f.Context, "context", "", "Temporarily override current context")
	root.PersistentFlags().StringVar(&f.Operator, "operator", "", "Deprecated operator override (ignored)")
	root.PersistentFlags().StringVar(&f.Ticket, "ticket", "", "Change ticket number")
	root.PersistentFlags().BoolVar(&f.Yes, "yes", false, "Confirm authorized operation")
	root.PersistentFlags().BoolVar(&f.NonInteractive, "non-interactive", false, "Disable interactive confirmation")
	root.PersistentFlags().BoolVar(&f.AllowContextChange, "allow-context-change", false, "Allow an R3 context replacement or credential migration")
	root.PersistentFlags().BoolVar(&f.AllowContextDelete, "allow-context-delete", false, "Allow an R3 context deletion")
	root.PersistentFlags().BoolVar(&f.AllowRoleChange, "allow-role-change", false, "Allow an R3 context role change")
	root.PersistentFlags().BoolVar(&f.AllowAuditPrune, "allow-audit-prune", false, "Allow an R3 audit evidence pruning operation")
	_ = root.PersistentFlags().MarkDeprecated("operator", "operator identity is derived from the local OS user and hostname")
	root.AddCommand(newContextCmd(f), newSchemaCmd(f), newDataCmd(f), newExportCmd(f), newImportCmd(f), newReconcileCmd(f), newRollbackCmd(f), newAuditCmd(f), newInstallCmd(f), newVersionCmd(f), newCapabilitiesCmd(f), newDoctorCmd(f), newQueryCmd(f), newExplainCmd(f))
	return root
}

func validateOutputFormat(format string) error {
	switch format {
	case "table", "json", "plain":
		return nil
	default:
		return apperrors.New(
			apperrors.CodeUsageError,
			fmt.Sprintf("invalid output format %q; expected table, json, or plain", format),
			nil,
		)
	}
}

// ExecuteContext runs the production command lifecycle, including bounded
// telemetry flush. Tests and embedding callers can continue to use NewRootCmd.
func ExecuteContext(ctx context.Context) error {
	f := &cliFlags{Output: "table", manageTelemetry: true}
	err := newRootCmdWith(f).ExecuteContext(ctx)
	finishTelemetry(ctx, f, err)
	return err
}

func finishTelemetry(ctx context.Context, f *cliFlags, commandErr error) {
	if f == nil || !f.manageTelemetry {
		return
	}
	if f.activeSpan != nil {
		if commandErr != nil {
			code := string(apperrors.AsAppError(commandErr).Code)
			f.activeSpan.SetAttributes(attribute.String("dbgov.error_code", code))
			f.activeSpan.SetStatus(codes.Error, code)
		} else {
			f.activeSpan.SetStatus(codes.Ok, "")
		}
		f.activeSpan.End()
	}
	if !f.commandStarted.IsZero() {
		status := "success"
		if commandErr != nil {
			status = "error"
		}
		recordCommand(ctx, f.commandName, status, time.Since(f.commandStarted), f.metricAttrs)
	}
	if f.telemetryStop == nil && f.metricsStop == nil {
		return
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if f.telemetryStop != nil {
		f.telemetryStop(shutdownCtx)
	}
	if f.metricsStop != nil {
		f.metricsStop(shutdownCtx)
	}
}

func resolveTelemetryConfig(ctxMeta *dbgovctx.Context) (traceEndpoint, metricsEndpoint string, insecure bool) {
	if ctxMeta != nil {
		traceEndpoint = ctxMeta.OTLPEndpoint
		metricsEndpoint = ctxMeta.OTLPMetricsEndpoint
		insecure = ctxMeta.OTLPInsecure
	}
	if traceEndpoint == "" {
		traceEndpoint = firstNonEmpty(os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"), os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	}
	if metricsEndpoint == "" {
		metricsEndpoint = firstNonEmpty(os.Getenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"), os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	}
	return traceEndpoint, metricsEndpoint, insecure
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func applyGlobalFlags(f *cliFlags) {
	if f.Trace {
		f.Debug = true
	}
	if f.NoColor {
		_ = os.Setenv("NO_COLOR", "1")
		color.NoColor = true
	}
}

func newPrinter(f *cliFlags) *printer.Printer {
	out := f.Out
	errOut := f.Err
	if out == nil {
		out = os.Stdout
	}
	if errOut == nil {
		errOut = os.Stderr
	}
	return printer.NewWithWriters(printer.Format(f.Output), out, errOut)
}

func warnAuditFailure(f *cliFlags, err error) {
	writer := io.Writer(os.Stderr)
	if f != nil && f.Err != nil {
		writer = f.Err
	}
	auditWarningMu.Lock()
	defer auditWarningMu.Unlock()
	_, _ = fmt.Fprintf(writer, "warning: failed to write audit log: %v\n", err)
}

func selectedContext(f *cliFlags) (*dbgovctx.Context, string) {
	cfg, err := dbgovctx.LoadReadOnly()
	if err != nil {
		return nil, ""
	}
	name := f.Context
	if name == "" {
		name = cfg.CurrentContext
	}
	if name == "" {
		return nil, ""
	}
	ctx, ok := cfg.Contexts[name]
	if !ok {
		return nil, name
	}
	return &ctx, name
}

func currentOperator(f *cliFlags) string {
	if f == nil {
		return ""
	}
	return f.trustedOperator
}

func resolveTrustedOperator() (string, error) {
	u, err := currentOSUser()
	if err != nil {
		return "", apperrors.New(apperrors.CodeAuthFailed, "failed to resolve trusted local OS user", err)
	}
	if u == nil {
		return "", apperrors.New(apperrors.CodeAuthFailed, "trusted local OS user is unavailable", nil)
	}
	username := strings.TrimSpace(u.Username)
	if username == "" {
		return "", apperrors.New(apperrors.CodeAuthFailed, "trusted local OS user is empty", nil)
	}
	hostname, err := currentHost()
	if err != nil {
		return "", apperrors.New(apperrors.CodeAuthFailed, "failed to resolve trusted local hostname", err)
	}
	hostname = strings.ToLower(strings.TrimSpace(hostname))
	if hostname == "" {
		return "", apperrors.New(apperrors.CodeAuthFailed, "trusted local hostname is empty", nil)
	}
	return username + "@" + hostname, nil
}

func commandContext(f *cliFlags) context.Context {
	if f.commandContext != nil {
		return f.commandContext
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	go func() {
		<-ctx.Done()
		cancel()
	}()
	return ctx
}

func requireExactArgs(name string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) != 1 {
			return apperrors.New(apperrors.CodeUsageError, fmt.Sprintf("%s requires 1 argument(s)", name), nil)
		}
		return nil
	}
}
