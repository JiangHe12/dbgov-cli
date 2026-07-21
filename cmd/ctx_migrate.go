package cmd

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/JiangHe12/opskit-core/v2/apperrors"
	"github.com/JiangHe12/opskit-core/v2/credstore"

	dbgaudit "github.com/JiangHe12/dbgov-cli/internal/audit"
	"github.com/JiangHe12/dbgov-cli/internal/dbgovctx"
	"github.com/JiangHe12/dbgov-cli/internal/safety"
)

const (
	credentialBackendEncryptedFile = "encrypted-file"
	credentialBackendKeychain      = "keychain"
)

type migrateCredentialsOptions struct {
	toBackend   string
	contextName string
	dryRun      bool
}

type migrateCredentialCandidate struct {
	name     string
	context  dbgovctx.Context
	password string
}

func ctxMigrateCredentialsCmd(f *cliFlags) *cobra.Command {
	var opts migrateCredentialsOptions
	cmd := &cobra.Command{
		Use:   "migrate-credentials",
		Short: "Move literal context passwords to a credential backend",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCtxMigrateCredentials(f, opts)
		},
	}
	cmd.Flags().StringVar(&opts.toBackend, "to", "", "Target backend: encrypted-file or keychain")
	cmd.Flags().StringVar(&opts.contextName, "context", "", "Context to migrate")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "Preview credential migration without authorization or writes")
	return cmd
}

func runCtxMigrateCredentials(f *cliFlags, opts migrateCredentialsOptions) error {
	candidates, backend, err := prepareCredentialMigration(opts)
	if err != nil {
		return err
	}
	if opts.dryRun {
		return printControlChangePreview(f, controlChangePreview{
			Action:   "credential.migrate",
			Contexts: credentialMigrationContextNames(candidates),
			Backend:  opts.toBackend,
		})
	}
	if len(candidates) == 0 {
		return printCredentialMigrationResult(f, opts.toBackend, 0)
	}
	if err := authorizeCredentialMigration(f, candidates); err != nil {
		return err
	}
	contextName := ""
	if len(candidates) == 1 {
		contextName = candidates[0].name
	}
	event := dbgaudit.New(
		dbgaudit.EventTypeCredentialMigrate,
		currentOperator(f),
		dbgaudit.Context{Name: contextName},
		dbgaudit.Target{ObjectType: "credential", Object: opts.toBackend},
	)
	event.Risk = "R3"
	metadata := mutationValueMetadata(string(dbgaudit.EventTypeCredentialMigrate), map[string]any{
		"backend":  opts.toBackend,
		"contexts": credentialMigrationContextNames(candidates),
	})
	metadata.Items = len(candidates)
	handle, err := beginMutationAudit(f, mutationAuditSpec{
		Action:   string(dbgaudit.EventTypeCredentialMigrate),
		Event:    event,
		Metadata: metadata,
	})
	if err != nil {
		return err
	}
	stored, attempted, opErr := applyCredentialMigration(f, opts, candidates, backend)
	if opErr == nil {
		stored = len(candidates)
		attempted = len(candidates)
	}
	if auditErr := finishMutationAuditProgress(handle, len(candidates), stored, attempted, opErr); auditErr != nil {
		return auditErr
	}
	return printCredentialMigrationResult(f, opts.toBackend, len(candidates))
}

func prepareCredentialMigration(opts migrateCredentialsOptions) ([]migrateCredentialCandidate, credstore.Backend, error) {
	if !validCredentialMigrationBackend(opts.toBackend) {
		return nil, nil, apperrors.New(apperrors.CodeUsageError, "--to must be encrypted-file or keychain", nil)
	}
	cfg, err := dbgovctx.LoadReadOnly()
	if err != nil {
		return nil, nil, err
	}
	candidates, err := credentialMigrationCandidates(cfg, opts.contextName)
	if err != nil {
		return nil, nil, err
	}
	backend, err := credstore.New(opts.toBackend)
	if err != nil {
		return nil, nil, apperrors.New(apperrors.CodeUsageError, err.Error(), err)
	}
	if err := backend.Available(); err != nil {
		return nil, nil, apperrors.New(apperrors.CodeUsageError, fmt.Sprintf("backend %q not available", opts.toBackend), err)
	}
	return candidates, backend, nil
}

func credentialMigrationContextNames(candidates []migrateCredentialCandidate) []string {
	contexts := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		contexts = append(contexts, candidate.name)
	}
	return contexts
}

func authorizeCredentialMigration(f *cliFlags, candidates []migrateCredentialCandidate) error {
	for _, candidate := range candidates {
		if err := authorizeContextControl(f, candidate.name, candidate.context, safety.AllowContextChange, f.AllowContextChange); err != nil {
			return err
		}
	}
	return nil
}

func applyCredentialMigration(
	f *cliFlags,
	opts migrateCredentialsOptions,
	candidates []migrateCredentialCandidate,
	backend credstore.Backend,
) (int, int, error) {
	stored := 0
	attempted := 0
	err := dbgovctx.Update(func(current *dbgovctx.Config) error {
		fresh, err := credentialMigrationCandidates(current, opts.contextName)
		if err != nil {
			return err
		}
		if len(fresh) != len(candidates) {
			return apperrors.New(apperrors.CodeAuthorizationRequired, "context policy changed during authorization; review the new policy and retry", nil)
		}
		for index, candidate := range candidates {
			if fresh[index].name != candidate.name {
				return apperrors.New(apperrors.CodeAuthorizationRequired, "context policy changed during authorization; review the new policy and retry", nil)
			}
			if err := verifyContextPreChangePolicy(current, candidate.name, candidate.name, candidate.context); err != nil {
				return err
			}
		}
		for _, candidate := range candidates {
			attempted++
			if err := backend.Put(commandContext(f), candidate.name, candidate.password); err != nil {
				return apperrors.New(apperrors.CodeLocalIOError, fmt.Sprintf("store credential for context %q failed", candidate.name), err)
			}
			stored++
		}
		for _, candidate := range candidates {
			updated := current.Contexts[candidate.name]
			updated.Password = credstore.EncodeRef(opts.toBackend)
			updated.CredentialBackend = opts.toBackend
			current.Contexts[candidate.name] = updated
		}
		return nil
	})
	return stored, attempted, err
}

func printCredentialMigrationResult(f *cliFlags, backend string, count int) error {
	result := map[string]any{"migrated": count, "backend": backend}
	p := newPrinter(f)
	if f.Output == "json" {
		return p.JSONData("CredentialMigration", result)
	}
	return p.Success(fmt.Sprintf("migrated %d context credential(s) to %s", count, backend))
}

func validCredentialMigrationBackend(name string) bool {
	return name == credentialBackendEncryptedFile || name == credentialBackendKeychain
}

func credentialMigrationCandidates(cfg *dbgovctx.Config, contextName string) ([]migrateCredentialCandidate, error) {
	if contextName != "" {
		ctx, ok := cfg.Contexts[contextName]
		if !ok {
			return nil, apperrors.New(apperrors.CodeUsageError, fmt.Sprintf("context %q not found", contextName), nil)
		}
		if isLiteralPassword(ctx.Password) {
			return []migrateCredentialCandidate{{name: contextName, context: ctx, password: ctx.Password}}, nil
		}
		return nil, nil
	}
	names := make([]string, 0, len(cfg.Contexts))
	for name := range cfg.Contexts {
		names = append(names, name)
	}
	sort.Strings(names)
	candidates := make([]migrateCredentialCandidate, 0, len(names))
	for _, name := range names {
		ctx := cfg.Contexts[name]
		if isLiteralPassword(ctx.Password) {
			candidates = append(candidates, migrateCredentialCandidate{name: name, context: ctx, password: ctx.Password})
		}
	}
	return candidates, nil
}

func isLiteralPassword(password string) bool {
	return password != "" && !credstore.ParseRef(password).IsRef
}
