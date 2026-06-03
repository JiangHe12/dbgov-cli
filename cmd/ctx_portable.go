package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/JiangHe12/opskit-core/apperrors"
	"github.com/JiangHe12/opskit-core/credstore"

	dbgaudit "github.com/JiangHe12/dbgov-cli/internal/audit"
	"github.com/JiangHe12/dbgov-cli/internal/dbgovctx"
)

const (
	ctxExportAPIVersion = "dbgov.io/ctx-export/v1"
	redactedCredential  = "<REDACTED>"
)

type contextExportDocument struct {
	APIVersion string           `yaml:"apiVersion"`
	Name       string           `yaml:"name"`
	Context    dbgovctx.Context `yaml:"context"`
}

type ctxExportOptions struct {
	includeCredentials bool
}

type ctxImportOptions struct {
	file   string
	force  bool
	rename string
}

type contextImportResult struct {
	Name               string `json:"name"`
	CredentialRedacted bool   `json:"credentialRedacted"`
}

func ctxExportCmd(f *cliFlags) *cobra.Command {
	opts := ctxExportOptions{}
	cmd := &cobra.Command{
		Use:   "export <name>",
		Short: "Export a portable context document",
		Args:  requireExactArgs("ctx export"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCtxExport(f, args[0], opts)
		},
	}
	cmd.Flags().BoolVar(&opts.includeCredentials, "include-credentials", false, "Include plaintext credentials when stored as plain-yaml")
	return cmd
}

func ctxImportCmd(f *cliFlags) *cobra.Command {
	opts := ctxImportOptions{}
	cmd := &cobra.Command{
		Use:   "import -f <file>",
		Short: "Import a portable context document",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 0 {
				return apperrors.New(apperrors.CodeUsageError, "ctx import accepts no positional arguments", nil)
			}
			return runCtxImport(f, opts)
		},
	}
	cmd.Flags().StringVarP(&opts.file, "file", "f", "", "Portable context document to import")
	cmd.Flags().BoolVar(&opts.force, "force", false, "Overwrite an existing context")
	cmd.Flags().StringVar(&opts.rename, "rename", "", "Import under a different context name")
	return cmd
}

func runCtxExport(f *cliFlags, name string, opts ctxExportOptions) error {
	cfg, err := dbgovctx.Load()
	if err != nil {
		return err
	}
	ctx, ok := cfg.Contexts[name]
	if !ok {
		return apperrors.New(apperrors.CodeUsageError, fmt.Sprintf("context %q not found", name), nil)
	}
	if opts.includeCredentials {
		if ctx.CredentialBackend != "" && ctx.CredentialBackend != "plain-yaml" {
			return apperrors.New(apperrors.CodeCredentialStoreError, "cannot export credentials from secure backend; migrate to plain-yaml first or share out-of-band", nil)
		}
	} else if ctx.Password != "" {
		ctx.Password = redactedCredential
	}
	doc := contextExportDocument{
		APIVersion: ctxExportAPIVersion,
		Name:       name,
		Context:    ctx,
	}
	data, err := yaml.Marshal(doc)
	if err != nil {
		return apperrors.New(apperrors.CodeLocalIOError, "failed to marshal context export", err)
	}
	if _, err := f.Out.Write(data); err != nil {
		return apperrors.New(apperrors.CodeLocalIOError, "failed to write context export", err)
	}
	emitAudit(f, dbgaudit.New(dbgaudit.EventTypeContextExport, currentOperator(f), dbgaudit.Context{
		Name:      name,
		Env:       ctx.Env,
		Protected: ctx.Protected,
	}, dbgaudit.Target{ObjectType: "context", Object: name}), nil)
	return nil
}

func runCtxImport(f *cliFlags, opts ctxImportOptions) error {
	if f.NonInteractive && !f.Yes {
		return apperrors.New(apperrors.CodeAuthorizationRequired, "ctx import requires --yes in non-interactive mode", nil)
	}
	if opts.file == "" {
		return apperrors.New(apperrors.CodeUsageError, "-f/--file is required", nil)
	}
	doc, err := readContextExportDocument(opts.file)
	if err != nil {
		return err
	}
	name, err := contextImportName(doc.Name, opts.rename)
	if err != nil {
		return err
	}
	credentialRedacted := doc.Context.Password == redactedCredential
	if credentialRedacted {
		doc.Context.Password = ""
	} else if ref := credstore.ParseRef(doc.Context.Password); ref.IsRef {
		doc.Context.CredentialBackend = ref.BackendName
	}
	cfg, err := dbgovctx.Load()
	if err != nil {
		return err
	}
	if _, exists := cfg.Contexts[name]; exists && !opts.force {
		return apperrors.New(apperrors.CodeUsageError, fmt.Sprintf("context %q already exists; use --force to overwrite", name), nil)
	}
	if err := dbgovctx.SetContext(name, doc.Context); err != nil {
		return err
	}
	emitAudit(f, dbgaudit.New(dbgaudit.EventTypeContextImport, currentOperator(f), dbgaudit.Context{
		Name:      name,
		Env:       doc.Context.Env,
		Protected: doc.Context.Protected,
	}, dbgaudit.Target{ObjectType: "context", Object: name}), nil)

	result := contextImportResult{Name: name, CredentialRedacted: credentialRedacted}
	p := newPrinter(f)
	if f.Output == "json" {
		return p.JSONData("ContextImportResult", result)
	}
	p.Success(fmt.Sprintf("context %q imported", name))
	if credentialRedacted {
		p.Warn(fmt.Sprintf("credential is redacted; run: dbgov ctx set %s --password=...", name))
	}
	return nil
}

func readContextExportDocument(path string) (contextExportDocument, error) {
	data, err := os.ReadFile(path) //nolint:gosec // User supplied context import path.
	if err != nil {
		return contextExportDocument{}, apperrors.New(apperrors.CodeLocalIOError, "failed to read context import file", err)
	}
	var doc contextExportDocument
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return contextExportDocument{}, apperrors.New(apperrors.CodeUsageError, "failed to parse context import file", err)
	}
	if doc.APIVersion != ctxExportAPIVersion {
		return contextExportDocument{}, apperrors.New(apperrors.CodeUnsupportedProtocol, fmt.Sprintf("unsupported context export apiVersion %q", doc.APIVersion), nil)
	}
	return doc, nil
}

func contextImportName(original, rename string) (string, error) {
	name := original
	if rename != "" {
		name = rename
	}
	if name == "" {
		return "", apperrors.New(apperrors.CodeUsageError, "context name is required", nil)
	}
	return name, nil
}
