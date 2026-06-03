package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/user"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/JiangHe12/opskit-core/printer"
	"github.com/JiangHe12/opskit-core/telemetry"

	"github.com/JiangHe12/dbgov-cli/internal/dbgovctx"
)

type cliFlags struct {
	Output         string
	Config         string
	Context        string
	Operator       string
	Ticket         string
	Yes            bool
	NonInteractive bool
	Out            io.Writer
	Err            io.Writer
	commandContext context.Context
}

func NewRootCmd() *cobra.Command {
	return newRootCmdWith(&cliFlags{Output: "table"})
}

func newRootCmdWith(f *cliFlags) *cobra.Command {
	root := &cobra.Command{
		Use:           "dbgov",
		Short:         "Governed database operations for AI agents",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			f.commandContext = cmd.Context()
			f.Out = cmd.OutOrStdout()
			f.Err = cmd.ErrOrStderr()
			if f.Config != "" {
				dbgovctx.SetConfigPath(f.Config)
			}
			_, span := telemetry.Tracer().Start(cmd.Context(), strings.ReplaceAll(cmd.CommandPath(), " ", "."))
			ctxMeta, _ := selectedContext(f)
			contextName := ""
			env := ""
			protected := false
			if ctxMeta != nil {
				env = ctxMeta.Env
				protected = ctxMeta.Protected
			}
			if f.Context != "" {
				contextName = f.Context
			}
			span.SetAttributes(telemetry.SpanAttributes(currentOperator(f), contextName, env, "", f.Ticket, protected, true, "")...)
			defer span.End()
			f.commandContext = cmd.Context()
			return nil
		},
		PersistentPostRun: func(cmd *cobra.Command, _ []string) {
			_ = cmd
		},
	}
	root.PersistentFlags().StringVarP(&f.Output, "output", "o", "table", "Output format: table | json | plain")
	root.PersistentFlags().StringVar(&f.Config, "config", "", "Temporarily override config file path")
	root.PersistentFlags().StringVar(&f.Context, "context", "", "Temporarily override current context")
	root.PersistentFlags().StringVar(&f.Operator, "operator", "", "Operator identity")
	root.PersistentFlags().StringVar(&f.Ticket, "ticket", "", "Change ticket number")
	root.PersistentFlags().BoolVar(&f.Yes, "yes", false, "Confirm authorized operation")
	root.PersistentFlags().BoolVar(&f.NonInteractive, "non-interactive", false, "Disable interactive confirmation")
	root.AddCommand(newContextCmd(f), newSchemaCmd(f), newDataCmd(f), newExportCmd(f), newImportCmd(f), newReconcileCmd(f), newRollbackCmd(f), newAuditCmd(f), newInstallCmd(f), newVersionCmd(f), newCapabilitiesCmd(f), newDoctorCmd(f), newQueryCmd(f), newExplainCmd(f))
	return root
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

func selectedContext(f *cliFlags) (*dbgovctx.Context, string) {
	cfg, err := dbgovctx.Load()
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
	if f.Operator != "" {
		return f.Operator
	}
	if env := os.Getenv("DBGOV_OPERATOR"); env != "" {
		return env
	}
	u, err := user.Current()
	if err == nil && u.Username != "" {
		return u.Username
	}
	return "unknown"
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
			return fmt.Errorf("%s requires 1 argument(s)", name)
		}
		return nil
	}
}
