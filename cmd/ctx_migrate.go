package cmd

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/JiangHe12/opskit-core/apperrors"
	"github.com/JiangHe12/opskit-core/credstore"

	dbgaudit "github.com/JiangHe12/dbgov-cli/internal/audit"
	"github.com/JiangHe12/dbgov-cli/internal/dbgovctx"
)

const (
	credentialBackendEncryptedFile = "encrypted-file"
	credentialBackendKeychain      = "keychain"
)

type migrateCredentialsOptions struct {
	toBackend   string
	contextName string
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
	return cmd
}

func runCtxMigrateCredentials(f *cliFlags, opts migrateCredentialsOptions) error {
	if !validCredentialMigrationBackend(opts.toBackend) {
		return apperrors.New(apperrors.CodeUsageError, "--to must be encrypted-file or keychain", nil)
	}
	cfg, err := dbgovctx.Load()
	if err != nil {
		return err
	}
	candidates, err := credentialMigrationCandidates(cfg, opts.contextName)
	if err != nil {
		return err
	}
	backend, err := credstore.New(opts.toBackend)
	if err != nil {
		return apperrors.New(apperrors.CodeUsageError, err.Error(), err)
	}
	if err := backend.Available(); err != nil {
		return apperrors.New(apperrors.CodeUsageError, fmt.Sprintf("backend %q not available", opts.toBackend), err)
	}
	for _, candidate := range candidates {
		if err := backend.Put(commandContext(f), candidate.name, candidate.password); err != nil {
			return apperrors.New(apperrors.CodeLocalIOError, fmt.Sprintf("store credential for context %q failed", candidate.name), err)
		}
	}
	for _, candidate := range candidates {
		ctx := candidate.context
		ctx.Password = credstore.EncodeRef(opts.toBackend)
		ctx.CredentialBackend = opts.toBackend
		if err := dbgovctx.SetContext(candidate.name, ctx); err != nil {
			return err
		}
		emitAudit(f, dbgaudit.New(
			dbgaudit.EventTypeCredentialMigrate,
			currentOperator(f),
			dbgaudit.Context{Name: candidate.name},
			dbgaudit.Target{ObjectType: "credential", Object: opts.toBackend},
		), nil)
	}
	result := map[string]any{"migrated": len(candidates), "backend": opts.toBackend}
	p := newPrinter(f)
	if f.Output == "json" {
		return p.JSONData("CredentialMigration", result)
	}
	p.Success(fmt.Sprintf("migrated %d context credential(s) to %s", len(candidates), opts.toBackend))
	return nil
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
