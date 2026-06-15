package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/JiangHe12/opskit-core/apperrors"

	"github.com/JiangHe12/dbgov-cli/internal/dbgovctx"
)

type contextView struct {
	Name      string `json:"name"`
	Engine    string `json:"engine"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	Database  string `json:"database,omitempty"`
	Env       string `json:"env,omitempty"`
	Protected bool   `json:"protected"`
	Current   bool   `json:"current"`
}

func newContextCmd(f *cliFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "ctx",
		Aliases: []string{"context"},
		Short:   "Manage database contexts",
	}
	cmd.AddCommand(ctxSetCmd(f), ctxUseCmd(f), ctxListCmd(f), ctxCurrentCmd(f), ctxDeleteCmd(f), ctxExportCmd(f), ctxImportCmd(f), ctxRoleCmd(f), ctxMigrateCredentialsCmd(f))
	return cmd
}

func ctxSetCmd(f *cliFlags) *cobra.Command {
	var req dbgovctx.Context
	cmd := &cobra.Command{
		Use:   "set <name>",
		Short: "Add or update a context",
		Args:  requireExactArgs("ctx set"),
		RunE: func(cmd *cobra.Command, args []string) error {
			req.Engine = strings.ToLower(strings.TrimSpace(req.Engine))
			switch req.Engine {
			case "":
				req.Engine = "mysql"
			case "mysql", "postgres":
			default:
				return apperrors.New(apperrors.CodeUsageError, "--engine must be mysql or postgres", nil)
			}
			if req.Host == "" {
				return apperrors.New(apperrors.CodeUsageError, "--host is required", nil)
			}
			if !cmd.Flags().Changed("port") && req.Engine == "postgres" {
				req.Port = 5432
			} else if req.Port == 0 {
				req.Port = 3306
			}
			if req.Server == "" {
				req.Server = fmt.Sprintf("%s://%s:%d", req.Engine, req.Host, req.Port)
			}
			if err := dbgovctx.SetContext(args[0], req); err != nil {
				return err
			}
			p := newPrinter(f)
			if f.Output == "json" {
				return p.JSONData("ContextItem", map[string]string{"name": args[0]})
			}
			p.Success(fmt.Sprintf("context %q saved", args[0]))
			return nil
		},
	}
	cmd.Flags().StringVar(&req.Engine, "engine", "mysql", "Engine: mysql | postgres")
	cmd.Flags().StringVar(&req.Host, "host", "", "Database host")
	cmd.Flags().IntVar(&req.Port, "port", 3306, "Database port")
	cmd.Flags().StringVar(&req.Database, "database", "", "Default database")
	cmd.Flags().StringVar(&req.Server, "server", "", "Connection server URI")
	cmd.Flags().StringVar(&req.Username, "username", "", "Username")
	cmd.Flags().StringVar(&req.Password, "password", "", "Password. Prefer DBGOV_PASSWORD.")
	cmd.Flags().StringVar(&req.Env, "env", "", "Environment label")
	cmd.Flags().BoolVar(&req.Protected, "protected", false, "Enable protection")
	cmd.Flags().StringVar(&req.TicketPattern, "ticket-pattern", "", "Ticket regex pattern")
	cmd.Flags().StringVar(&req.CredentialBackend, "credential-backend", "plain-yaml", "Credential backend")
	cmd.Flags().StringVar(&req.OTLPEndpoint, "otel-endpoint", "", "OTLP trace endpoint")
	cmd.Flags().BoolVar(&req.OTLPInsecure, "otel-insecure", false, "Disable TLS for OTLP exporter")
	cmd.Flags().BoolVar(&req.OTLPRedact, "otel-redact", true, "Redact sensitive OTel attributes")
	return cmd
}

func ctxUseCmd(f *cliFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "use <name>",
		Short: "Switch current context",
		Args:  requireExactArgs("ctx use"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := dbgovctx.UseContext(args[0]); err != nil {
				return err
			}
			newPrinter(f).Success(fmt.Sprintf("current context is %q", args[0]))
			return nil
		},
	}
}

func ctxListCmd(f *cliFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List contexts",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := dbgovctx.Load()
			if err != nil {
				return err
			}
			names := make([]string, 0, len(cfg.Contexts))
			for name := range cfg.Contexts {
				names = append(names, name)
			}
			sort.Strings(names)
			views := make([]contextView, 0, len(names))
			rows := make([][]string, 0, len(names))
			for _, name := range names {
				view := makeContextView(name, cfg.Contexts[name], name == cfg.CurrentContext)
				views = append(views, view)
				rows = append(rows, []string{name, view.Engine, view.Host, fmt.Sprintf("%d", view.Port), view.Database, fmt.Sprintf("%t", view.Current)})
			}
			p := newPrinter(f)
			if f.Output == "json" {
				return p.JSONList("ContextList", views, len(views), 1, len(views), false)
			}
			p.Table([]string{"NAME", "ENGINE", "HOST", "PORT", "DATABASE", "CURRENT"}, rows)
			return nil
		},
	}
}

func ctxCurrentCmd(f *cliFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "current",
		Short: "Show current context",
		RunE: func(cmd *cobra.Command, args []string) error {
			current, name, err := dbgovctx.Current()
			if err != nil {
				return err
			}
			view := makeContextView(name, *current, true)
			p := newPrinter(f)
			if f.Output == "json" {
				return p.JSONData("ContextItem", view)
			}
			p.KV([][2]string{
				{"Name", view.Name},
				{"Engine", view.Engine},
				{"Host", view.Host},
				{"Port", fmt.Sprintf("%d", view.Port)},
				{"Database", view.Database},
			})
			return nil
		},
	}
}

func ctxDeleteCmd(f *cliFlags) *cobra.Command {
	return &cobra.Command{
		Use:     "delete <name>",
		Aliases: []string{"rm", "remove"},
		Short:   "Delete a context",
		Args:    requireExactArgs("ctx delete"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := dbgovctx.DeleteContext(args[0]); err != nil {
				return err
			}
			newPrinter(f).Success(fmt.Sprintf("context %q deleted", args[0]))
			return nil
		},
	}
}

func makeContextView(name string, ctx dbgovctx.Context, current bool) contextView {
	return contextView{
		Name:      name,
		Engine:    ctx.Engine,
		Host:      ctx.Host,
		Port:      ctx.Port,
		Database:  ctx.Database,
		Env:       ctx.Env,
		Protected: ctx.Protected,
		Current:   current,
	}
}
