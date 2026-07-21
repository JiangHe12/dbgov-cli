package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/JiangHe12/opskit-core/v2/apperrors"
	"github.com/JiangHe12/opskit-core/v2/credstore"

	dbgaudit "github.com/JiangHe12/dbgov-cli/internal/audit"
	"github.com/JiangHe12/dbgov-cli/internal/dbgovctx"
	"github.com/JiangHe12/dbgov-cli/internal/safety"
)

const (
	ctxExportAPIVersion       = "dbgov-cli.io/ctx-export/v1"
	legacyCtxExportAPIVersion = "dbgov.io/ctx-export/v1"
	redactedCredential        = "<REDACTED>"
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
	dryRun bool
}

type contextImportResult struct {
	Name               string `json:"name"`
	CredentialRedacted bool   `json:"credentialRedacted"`
}

type preparedContextImport struct {
	document           contextExportDocument
	name               string
	credentialRedacted bool
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
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "Preview the context import without authorization or writes")
	return cmd
}

func runCtxExport(f *cliFlags, name string, opts ctxExportOptions) error {
	cfg, err := dbgovctx.LoadReadOnly()
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
	prepared, err := prepareContextImport(opts)
	if err != nil {
		return err
	}
	cfg, err := dbgovctx.LoadReadOnly()
	if err != nil {
		return err
	}
	if _, exists := cfg.Contexts[prepared.name]; exists && !opts.force {
		return apperrors.New(apperrors.CodeUsageError, fmt.Sprintf("context %q already exists; use --force to overwrite", prepared.name), nil)
	}
	policyName, policy, err := contextPreChangePolicy(cfg, prepared.name)
	if err != nil {
		return err
	}
	if opts.dryRun {
		return printControlChangePreview(f, controlChangePreview{Action: "context.import", Context: prepared.name})
	}
	if err := authorizeContextControl(f, policyName, policy, safety.AllowContextChange, f.AllowContextChange); err != nil {
		return err
	}
	event := dbgaudit.New(dbgaudit.EventTypeContextImport, currentOperator(f), dbgaudit.Context{
		Name:      prepared.name,
		Env:       prepared.document.Context.Env,
		Protected: prepared.document.Context.Protected,
	}, dbgaudit.Target{ObjectType: "context", Object: prepared.name})
	event.Risk = "R3"
	metadata := mutationValueMetadata(string(dbgaudit.EventTypeContextImport), prepared.document)
	metadata.Items = 1
	handle, err := beginMutationAudit(f, mutationAuditSpec{
		Action:   string(dbgaudit.EventTypeContextImport),
		Event:    event,
		Metadata: metadata,
	})
	if err != nil {
		return err
	}
	attempted, err := applyContextImport(prepared, opts.force, policyName, policy)
	if err != nil {
		return finishMutationAuditProgress(handle, 1, 0, attempted, err)
	}
	if err := finishBatchMutationAudit(handle, 1, 1, nil); err != nil {
		return err
	}

	result := contextImportResult{Name: prepared.name, CredentialRedacted: prepared.credentialRedacted}
	p := newPrinter(f)
	if f.Output == "json" {
		return p.JSONData("ContextImportResult", result)
	}
	if err := p.Success(fmt.Sprintf("context %q imported", prepared.name)); err != nil {
		return err
	}
	if prepared.credentialRedacted {
		return p.Warn(fmt.Sprintf("credential is redacted; run: dbgov ctx set %s --password=...", prepared.name))
	}
	return nil
}

func prepareContextImport(opts ctxImportOptions) (preparedContextImport, error) {
	if opts.file == "" {
		return preparedContextImport{}, apperrors.New(apperrors.CodeUsageError, "-f/--file is required", nil)
	}
	doc, err := readContextExportDocument(opts.file)
	if err != nil {
		return preparedContextImport{}, err
	}
	name, err := contextImportName(doc.Name, opts.rename)
	if err != nil {
		return preparedContextImport{}, err
	}
	doc.Context.Engine = strings.ToLower(strings.TrimSpace(doc.Context.Engine))
	if err := validateImportedContext(doc.Context); err != nil {
		return preparedContextImport{}, err
	}
	credentialRedacted := doc.Context.Password == redactedCredential
	if credentialRedacted {
		doc.Context.Password = ""
	} else if ref := credstore.ParseRef(doc.Context.Password); ref.IsRef {
		doc.Context.CredentialBackend = ref.BackendName
	}
	return preparedContextImport{document: doc, name: name, credentialRedacted: credentialRedacted}, nil
}

func validateImportedContext(ctx dbgovctx.Context) error {
	if ctx.Engine != "mysql" && ctx.Engine != "postgres" {
		return apperrors.New(apperrors.CodeUsageError, "imported context engine must be mysql or postgres", nil)
	}
	if strings.TrimSpace(ctx.Host) == "" {
		return apperrors.New(apperrors.CodeUsageError, "imported context host is required", nil)
	}
	if ctx.Port < 1 || ctx.Port > 65535 {
		return apperrors.New(apperrors.CodeUsageError, "imported context port must be between 1 and 65535", nil)
	}
	if ctx.TicketPattern != "" {
		if _, err := regexp.Compile(ctx.TicketPattern); err != nil {
			return apperrors.New(apperrors.CodeUsageError, "imported context has invalid ticket pattern", err)
		}
	}
	for operator, role := range ctx.Roles {
		if strings.TrimSpace(operator) == "" || !validRole(role) {
			return apperrors.New(apperrors.CodeUsageError, "imported context has invalid role assignment", nil)
		}
	}
	return nil
}

func applyContextImport(
	prepared preparedContextImport,
	force bool,
	policyName string,
	policy dbgovctx.Context,
) (int, error) {
	attempted := 0
	err := dbgovctx.Update(func(current *dbgovctx.Config) error {
		if err := verifyContextPreChangePolicy(current, prepared.name, policyName, policy); err != nil {
			return err
		}
		if _, exists := current.Contexts[prepared.name]; exists && !force {
			return apperrors.New(apperrors.CodeUsageError, fmt.Sprintf("context %q already exists; use --force to overwrite", prepared.name), nil)
		}
		attempted = 1
		current.Contexts[prepared.name] = prepared.document.Context
		return nil
	})
	return attempted, err
}

func readContextExportDocument(path string) (contextExportDocument, error) {
	data, err := os.ReadFile(path) //nolint:gosec // User supplied context import path.
	if err != nil {
		return contextExportDocument{}, apperrors.New(apperrors.CodeLocalIOError, "failed to read context import file", err)
	}
	var doc contextExportDocument
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&doc); err != nil {
		return contextExportDocument{}, apperrors.New(apperrors.CodeUsageError, "failed to parse context import file", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return contextExportDocument{}, apperrors.New(apperrors.CodeUsageError, "multiple YAML documents are not allowed", nil)
	} else if !errors.Is(err, io.EOF) {
		return contextExportDocument{}, apperrors.New(apperrors.CodeUsageError, "failed to parse context import file", err)
	}
	if doc.APIVersion != ctxExportAPIVersion && doc.APIVersion != legacyCtxExportAPIVersion {
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
