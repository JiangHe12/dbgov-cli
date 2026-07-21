package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/JiangHe12/opskit-core/v2/apperrors"

	dbgaudit "github.com/JiangHe12/dbgov-cli/internal/audit"
	"github.com/JiangHe12/dbgov-cli/internal/dbgovctx"
	"github.com/JiangHe12/dbgov-cli/internal/safety"
)

type roleOptions struct {
	targetOperator string
	role           string
	dryRun         bool
}

type roleItem struct {
	Operator string `json:"operator"`
	Role     string `json:"role"`
}

func ctxRoleCmd(f *cliFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "role",
		Short: "Manage context RBAC roles",
	}
	cmd.AddCommand(ctxRoleSetCmd(f), ctxRoleUnsetCmd(f), ctxRoleListCmd(f))
	return cmd
}

func ctxRoleSetCmd(f *cliFlags) *cobra.Command {
	var opts roleOptions
	cmd := &cobra.Command{
		Use:   "set <context>",
		Short: "Assign an operator role for a context",
		Args:  requireExactArgs("ctx role set"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCtxRoleSet(f, args[0], opts)
		},
	}
	cmd.Flags().StringVar(&opts.targetOperator, "target-operator", "", "Operator identity to assign")
	cmd.Flags().StringVar(&opts.role, "role", "", "Role: reader, writer, admin")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "Preview the role change without authorization or writes")
	return cmd
}

func ctxRoleUnsetCmd(f *cliFlags) *cobra.Command {
	var opts roleOptions
	cmd := &cobra.Command{
		Use:   "unset <context>",
		Short: "Remove an operator role from a context",
		Args:  requireExactArgs("ctx role unset"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCtxRoleUnset(f, args[0], opts)
		},
	}
	cmd.Flags().StringVar(&opts.targetOperator, "target-operator", "", "Operator identity to remove")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "Preview the role change without authorization or writes")
	return cmd
}

func ctxRoleListCmd(f *cliFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "list <context>",
		Short: "List operator roles for a context",
		Args:  requireExactArgs("ctx role list"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCtxRoleList(f, args[0])
		},
	}
}

func runCtxRoleSet(f *cliFlags, contextName string, opts roleOptions) error {
	opts.targetOperator = strings.TrimSpace(opts.targetOperator)
	if opts.targetOperator == "" {
		return apperrors.New(apperrors.CodeUsageError, "--target-operator is required", nil)
	}
	if !validRole(opts.role) {
		return apperrors.New(apperrors.CodeUsageError, "--role must be reader, writer, or admin", nil)
	}
	ctx, err := loadContextForRole(contextName)
	if err != nil {
		return err
	}
	if opts.dryRun {
		return printControlChangePreview(f, controlChangePreview{
			Action:         "role.set",
			Context:        contextName,
			TargetOperator: opts.targetOperator,
			Role:           opts.role,
		})
	}
	if err := authorizeContextControl(f, contextName, ctx, safety.AllowRoleChange, f.AllowRoleChange); err != nil {
		return err
	}
	event := roleAuditEvent(f, dbgaudit.EventTypeRoleAssign, contextName, opts.targetOperator)
	event.Role = opts.role
	event.Risk = "R3"
	metadata := mutationValueMetadata(string(dbgaudit.EventTypeRoleAssign), map[string]string{
		"targetOperator": opts.targetOperator,
		"role":           opts.role,
	})
	metadata.Items = 1
	handle, err := beginMutationAudit(f, mutationAuditSpec{
		Action:   string(dbgaudit.EventTypeRoleAssign),
		Event:    event,
		Metadata: metadata,
	})
	if err != nil {
		return err
	}
	attempted := 0
	if err := dbgovctx.Update(func(current *dbgovctx.Config) error {
		if err := verifyContextPreChangePolicy(current, contextName, contextName, ctx); err != nil {
			return err
		}
		attempted = 1
		updated := current.Contexts[contextName]
		if updated.Roles == nil {
			updated.Roles = map[string]string{}
		}
		updated.Roles[opts.targetOperator] = opts.role
		current.Contexts[contextName] = updated
		return nil
	}); err != nil {
		return finishMutationAuditProgress(handle, 1, 0, attempted, err)
	}
	if err := finishBatchMutationAudit(handle, 1, 1, nil); err != nil {
		return err
	}
	return newPrinter(f).Success(fmt.Sprintf("role %q assigned to %q in context %q", opts.role, opts.targetOperator, contextName))
}

func runCtxRoleUnset(f *cliFlags, contextName string, opts roleOptions) error {
	opts.targetOperator = strings.TrimSpace(opts.targetOperator)
	if opts.targetOperator == "" {
		return apperrors.New(apperrors.CodeUsageError, "--target-operator is required", nil)
	}
	ctx, err := loadContextForRole(contextName)
	if err != nil {
		return err
	}
	if opts.dryRun {
		return printControlChangePreview(f, controlChangePreview{
			Action:         "role.unset",
			Context:        contextName,
			TargetOperator: opts.targetOperator,
		})
	}
	if err := authorizeContextControl(f, contextName, ctx, safety.AllowRoleChange, f.AllowRoleChange); err != nil {
		return err
	}
	event := roleAuditEvent(f, dbgaudit.EventTypeRoleRevoke, contextName, opts.targetOperator)
	event.Risk = "R3"
	metadata := mutationValueMetadata(string(dbgaudit.EventTypeRoleRevoke), opts.targetOperator)
	metadata.Items = 1
	metadata.Deletes = 1
	handle, err := beginMutationAudit(f, mutationAuditSpec{
		Action:   string(dbgaudit.EventTypeRoleRevoke),
		Event:    event,
		Metadata: metadata,
	})
	if err != nil {
		return err
	}
	attempted := 0
	if err := dbgovctx.Update(func(current *dbgovctx.Config) error {
		if err := verifyContextPreChangePolicy(current, contextName, contextName, ctx); err != nil {
			return err
		}
		attempted = 1
		updated := current.Contexts[contextName]
		if updated.Roles != nil {
			delete(updated.Roles, opts.targetOperator)
			if len(updated.Roles) == 0 {
				updated.Roles = nil
			}
		}
		current.Contexts[contextName] = updated
		return nil
	}); err != nil {
		return finishMutationAuditProgress(handle, 1, 0, attempted, err)
	}
	if err := finishBatchMutationAudit(handle, 1, 1, nil); err != nil {
		return err
	}
	return newPrinter(f).Success(fmt.Sprintf("role removed from %q in context %q", opts.targetOperator, contextName))
}

func runCtxRoleList(f *cliFlags, contextName string) error {
	ctx, err := loadContextForRole(contextName)
	if err != nil {
		return err
	}
	items := roleItems(ctx.Roles)
	p := newPrinter(f)
	if f.Output == "json" {
		return p.JSONList("RoleList", items, len(items), 1, len(items), false)
	}
	if len(items) == 0 {
		return p.Info("(no roles assigned)")
	}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		rows = append(rows, []string{item.Operator, item.Role})
	}
	return p.Table([]string{"OPERATOR", "ROLE"}, rows)
}

func loadContextForRole(name string) (dbgovctx.Context, error) {
	cfg, err := dbgovctx.LoadReadOnly()
	if err != nil {
		return dbgovctx.Context{}, err
	}
	ctx, ok := cfg.Contexts[name]
	if !ok {
		return dbgovctx.Context{}, apperrors.New(apperrors.CodeUsageError, fmt.Sprintf("context %q not found", name), nil)
	}
	return ctx, nil
}

func validRole(role string) bool {
	return role == safety.RoleReader || role == safety.RoleWriter || role == safety.RoleAdmin
}

func roleItems(roles map[string]string) []roleItem {
	operators := make([]string, 0, len(roles))
	for operator := range roles {
		operators = append(operators, operator)
	}
	sort.Strings(operators)
	items := make([]roleItem, 0, len(operators))
	for _, operator := range operators {
		items = append(items, roleItem{Operator: operator, Role: roles[operator]})
	}
	return items
}

func roleAuditEvent(f *cliFlags, eventType dbgaudit.EventType, contextName, operator string) dbgaudit.Event {
	return dbgaudit.New(eventType, currentOperator(f), dbgaudit.Context{Name: contextName}, dbgaudit.Target{ObjectType: "role", Object: operator})
}
