package cmd

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/JiangHe12/opskit-core/v2/apperrors"
	"github.com/JiangHe12/opskit-core/v2/credstore"

	dbgaudit "github.com/JiangHe12/dbgov-cli/internal/audit"
	"github.com/JiangHe12/dbgov-cli/internal/dbgovctx"
	"github.com/JiangHe12/dbgov-cli/internal/safety"
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

type contextSetOptions struct {
	dryRun bool
}

type contextDeleteOptions struct {
	dryRun bool
}

type contextUseOptions struct {
	dryRun bool
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
	var opts contextSetOptions
	cmd := &cobra.Command{
		Use:   "set <name>",
		Short: "Add or update a context",
		Args:  requireExactArgs("ctx set"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCtxSet(f, cmd, args[0], req, opts)
		},
	}
	cmd.Flags().StringVar(&req.Engine, "engine", "mysql", "Engine: mysql | postgres")
	cmd.Flags().StringVar(&req.Host, "host", "", "Database host")
	cmd.Flags().IntVar(&req.Port, "port", 3306, "Database port")
	cmd.Flags().StringVar(&req.Database, "database", "", "Default database")
	cmd.Flags().StringVar(&req.Server, "server", "", "Connection server URI")
	cmd.Flags().StringVar(&req.Username, "username", "", "Username")
	cmd.Flags().StringVar(&req.Password, "password", "", "Password to store in credstore; prefer DBGOV_PASSWORD for non-interactive runs")
	cmd.Flags().StringVar(&req.Env, "env", "", "Environment label")
	cmd.Flags().BoolVar(&req.Protected, "protected", false, "Enable protection")
	cmd.Flags().StringVar(&req.TicketPattern, "ticket-pattern", "", "Ticket regex pattern")
	cmd.Flags().StringVar(&req.CredentialBackend, "credential-backend", "plain-yaml", "Credential backend")
	cmd.Flags().StringVar(&req.OTLPEndpoint, "otel-endpoint", "", "OTLP trace endpoint")
	cmd.Flags().BoolVar(&req.OTLPInsecure, "otel-insecure", false, "Disable TLS for OTLP exporter")
	cmd.Flags().BoolVar(&req.OTLPRedact, "otel-redact", true, "Redact sensitive OTel attributes")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "Preview the context change without authorization or writes")
	return cmd
}

func runCtxSet(f *cliFlags, cmd *cobra.Command, name string, req dbgovctx.Context, opts contextSetOptions) error {
	req, err := normalizeContextSetRequest(cmd, req)
	if err != nil {
		return err
	}
	if err := validateContextCredentialRequest(req); err != nil {
		return err
	}
	cfg, err := dbgovctx.LoadReadOnly()
	if err != nil {
		return err
	}
	policyName, policy, err := contextPreChangePolicy(cfg, name)
	if err != nil {
		return err
	}
	if opts.dryRun {
		return printControlChangePreview(f, controlChangePreview{Action: "context.set", Context: name})
	}
	if err := authorizeContextControl(f, policyName, policy, safety.AllowContextChange, f.AllowContextChange); err != nil {
		return err
	}
	event := dbgaudit.New(dbgaudit.EventTypeContextSet, currentOperator(f), dbgaudit.Context{
		Name:      name,
		Env:       req.Env,
		Protected: req.Protected,
	}, dbgaudit.Target{ObjectType: "context", Object: name})
	event.Risk = "R3"
	metadata := mutationValueMetadata(string(dbgaudit.EventTypeContextSet), req)
	metadata.Items = 1
	handle, err := beginMutationAudit(f, mutationAuditSpec{
		Action:   string(dbgaudit.EventTypeContextSet),
		Event:    event,
		Metadata: metadata,
	})
	if err != nil {
		return err
	}
	stored, attempted, err := saveAuthorizedContext(cmd, name, req, policyName, policy)
	if err != nil {
		return finishMutationAuditProgress(handle, 1, 0, attempted, err)
	}
	handle.spec.Event.Context.Env = stored.Env
	handle.spec.Event.Context.Protected = stored.Protected
	if err := finishBatchMutationAudit(handle, 1, 1, nil); err != nil {
		return err
	}
	p := newPrinter(f)
	if f.Output == "json" {
		return p.JSONData("ContextItem", map[string]string{"name": name})
	}
	return p.Success(fmt.Sprintf("context %q saved", name))
}

func normalizeContextSetRequest(cmd *cobra.Command, req dbgovctx.Context) (dbgovctx.Context, error) {
	req.Engine = strings.ToLower(strings.TrimSpace(req.Engine))
	switch req.Engine {
	case "":
		req.Engine = "mysql"
	case "mysql", "postgres":
	default:
		return dbgovctx.Context{}, apperrors.New(apperrors.CodeUsageError, "--engine must be mysql or postgres", nil)
	}
	req.Host = strings.TrimSpace(req.Host)
	if req.Host == "" {
		return dbgovctx.Context{}, apperrors.New(apperrors.CodeUsageError, "--host is required", nil)
	}
	if !cmd.Flags().Changed("port") && req.Engine == "postgres" {
		req.Port = 5432
	} else if req.Port == 0 {
		req.Port = 3306
	}
	if req.Port < 1 || req.Port > 65535 {
		return dbgovctx.Context{}, apperrors.New(apperrors.CodeUsageError, "--port must be between 1 and 65535", nil)
	}
	if req.TicketPattern != "" {
		if _, err := regexp.Compile(req.TicketPattern); err != nil {
			return dbgovctx.Context{}, apperrors.New(apperrors.CodeUsageError, "--ticket-pattern must be a valid regular expression", err)
		}
	}
	if req.Server == "" {
		req.Server = fmt.Sprintf("%s://%s:%d", req.Engine, req.Host, req.Port)
	}
	return req, nil
}

func validateContextCredentialRequest(req dbgovctx.Context) error {
	if err := credstore.RequireSecureBackend(req.CredentialBackend, req.Password != ""); err != nil {
		return err
	}
	if req.Password == "" {
		return nil
	}
	backend, err := credstore.New(req.CredentialBackend)
	if err != nil {
		return apperrors.New(apperrors.CodeCredentialStoreError, "failed to store credential", err)
	}
	if err := backend.Available(); err != nil {
		return apperrors.New(apperrors.CodeCredentialStoreError, "failed to store credential", err)
	}
	return nil
}

func saveAuthorizedContext(
	cmd *cobra.Command,
	name string,
	req dbgovctx.Context,
	policyName string,
	policy dbgovctx.Context,
) (dbgovctx.Context, int, error) {
	stored := req
	attempted := 0
	err := dbgovctx.Update(func(current *dbgovctx.Config) error {
		if err := verifyContextPreChangePolicy(current, name, policyName, policy); err != nil {
			return err
		}
		attempted = 1
		next, err := dbgovctx.StoreCredential(cmd.Context(), name, req.CredentialBackend, req.Password, req)
		if err != nil {
			return apperrors.New(apperrors.CodeCredentialStoreError, "failed to store credential", err)
		}
		stored = next
		current.Contexts[name] = next
		return nil
	})
	return stored, attempted, err
}

func ctxUseCmd(f *cliFlags) *cobra.Command {
	var opts contextUseOptions
	cmd := &cobra.Command{
		Use:   "use <name>",
		Short: "Switch current context",
		Args:  requireExactArgs("ctx use"),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := dbgovctx.LoadReadOnly()
			if err != nil {
				return err
			}
			target, ok := cfg.Contexts[args[0]]
			if !ok {
				return apperrors.New(apperrors.CodeUsageError, fmt.Sprintf("context %q not found", args[0]), nil)
			}
			policyName, policy, err := currentContextChangePolicy(cfg, args[0], target)
			if err != nil {
				return err
			}
			if opts.dryRun {
				return printControlChangePreview(f, controlChangePreview{
					Action:  "context.use",
					Context: args[0],
				})
			}
			if err := authorizeContextControl(f, policyName, policy, safety.AllowContextChange, f.AllowContextChange); err != nil {
				return err
			}
			event := dbgaudit.New(dbgaudit.EventTypeContextUse, currentOperator(f), dbgaudit.Context{
				Name:      args[0],
				Env:       target.Env,
				Protected: target.Protected,
			}, dbgaudit.Target{ObjectType: "context", Object: args[0]})
			event.Risk = "R3"
			metadata := mutationValueMetadata(string(dbgaudit.EventTypeContextUse), map[string]string{
				"from": cfg.CurrentContext,
				"to":   args[0],
			})
			metadata.Items = 1
			handle, err := beginMutationAudit(f, mutationAuditSpec{
				Action:   string(dbgaudit.EventTypeContextUse),
				Event:    event,
				Metadata: metadata,
			})
			if err != nil {
				return err
			}
			attempted, err := switchCurrentContext(args[0], target, cfg.CurrentContext, policy)
			if err != nil {
				return finishMutationAuditProgress(handle, 1, 0, attempted, err)
			}
			if err := finishBatchMutationAudit(handle, 1, 1, nil); err != nil {
				return err
			}
			return newPrinter(f).Success(fmt.Sprintf("current context is %q", args[0]))
		},
	}
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "Preview the current-context switch without authorization or writes")
	return cmd
}

func currentContextChangePolicy(
	cfg *dbgovctx.Config,
	targetName string,
	target dbgovctx.Context,
) (string, dbgovctx.Context, error) {
	if cfg.CurrentContext == "" {
		return targetName, target, nil
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

func switchCurrentContext(targetName string, target dbgovctx.Context, previousName string, previous dbgovctx.Context) (int, error) {
	attempted := 0
	err := dbgovctx.Update(func(current *dbgovctx.Config) error {
		if current.CurrentContext != previousName {
			return contextPolicyChangedError()
		}
		if err := verifyPersistedContext(current, targetName, target); err != nil {
			return err
		}
		if previousName != "" {
			if err := verifyPersistedContext(current, previousName, previous); err != nil {
				return err
			}
		}
		attempted = 1
		current.CurrentContext = targetName
		return nil
	})
	return attempted, err
}

func ctxListCmd(f *cliFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List contexts",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := dbgovctx.LoadReadOnly()
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
			return p.Table([]string{"NAME", "ENGINE", "HOST", "PORT", "DATABASE", "CURRENT"}, rows)
		},
	}
}

func ctxCurrentCmd(f *cliFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "current",
		Short: "Show current context",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := dbgovctx.LoadReadOnly()
			if err != nil {
				return err
			}
			name := cfg.CurrentContext
			current, ok := cfg.Contexts[name]
			if name == "" || !ok {
				return errNoContext()
			}
			view := makeContextView(name, current, true)
			p := newPrinter(f)
			if f.Output == "json" {
				return p.JSONData("ContextItem", view)
			}
			return p.KV([][2]string{
				{"Name", view.Name},
				{"Engine", view.Engine},
				{"Host", view.Host},
				{"Port", fmt.Sprintf("%d", view.Port)},
				{"Database", view.Database},
			})
		},
	}
}

func ctxDeleteCmd(f *cliFlags) *cobra.Command {
	var opts contextDeleteOptions
	cmd := &cobra.Command{
		Use:     "delete <name>",
		Aliases: []string{"rm", "remove"},
		Short:   "Delete a context",
		Args:    requireExactArgs("ctx delete"),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := dbgovctx.LoadReadOnly()
			if err != nil {
				return err
			}
			policy, ok := cfg.Contexts[args[0]]
			if !ok {
				return apperrors.New(apperrors.CodeUsageError, fmt.Sprintf("context %q not found", args[0]), nil)
			}
			if opts.dryRun {
				return printControlChangePreview(f, controlChangePreview{
					Action:  "context.delete",
					Context: args[0],
				})
			}
			if err := authorizeContextControl(f, args[0], policy, safety.AllowContextDelete, f.AllowContextDelete); err != nil {
				return err
			}
			event := dbgaudit.New(dbgaudit.EventTypeContextDelete, currentOperator(f), dbgaudit.Context{
				Name:      args[0],
				Env:       policy.Env,
				Protected: policy.Protected,
			}, dbgaudit.Target{ObjectType: "context", Object: args[0]})
			event.Risk = "R3"
			metadata := mutationValueMetadata(string(dbgaudit.EventTypeContextDelete), policy)
			metadata.Items = 1
			metadata.Deletes = 1
			handle, err := beginMutationAudit(f, mutationAuditSpec{
				Action:   string(dbgaudit.EventTypeContextDelete),
				Event:    event,
				Metadata: metadata,
			})
			if err != nil {
				return err
			}
			attempted := 0
			if err := dbgovctx.Update(func(current *dbgovctx.Config) error {
				if err := verifyContextPreChangePolicy(current, args[0], args[0], policy); err != nil {
					return err
				}
				attempted = 1
				delete(current.Contexts, args[0])
				if current.CurrentContext == args[0] {
					current.CurrentContext = ""
				}
				return nil
			}); err != nil {
				return finishMutationAuditProgress(handle, 1, 0, attempted, err)
			}
			if err := finishBatchMutationAudit(handle, 1, 1, nil); err != nil {
				return err
			}
			return newPrinter(f).Success(fmt.Sprintf("context %q deleted", args[0]))
		},
	}
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "Preview the context deletion without authorization or writes")
	return cmd
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
