package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/JiangHe12/opskit-core/v2/apperrors"
	coreaudit "github.com/JiangHe12/opskit-core/v2/audit"
	"github.com/JiangHe12/opskit-core/v2/printer"

	dbgaudit "github.com/JiangHe12/dbgov-cli/internal/audit"
	"github.com/JiangHe12/dbgov-cli/internal/dbgovctx"
	"github.com/JiangHe12/dbgov-cli/internal/safety"
)

type auditPruneOptions struct {
	path      string
	before    string
	keepLast  int
	dryRun    bool
	dryRunSet bool
	confirm   bool
}

type auditPruneResult struct {
	DryRun          bool     `json:"dryRun"`
	Files           []string `json:"files"`
	Count           int      `json:"count"`
	CheckpointState string   `json:"checkpointState,omitempty"`
}

type auditPrunePolicy struct {
	ContextName string
	Context     dbgovctx.Context
}

func auditPruneCmd(f *cliFlags) *cobra.Command {
	opts := auditPruneOptions{keepLast: -1, dryRun: true}
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Prune rotated audit logs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.dryRunSet = cmd.Flags().Changed("dry-run")
			return runAuditPrune(f, opts)
		},
	}
	cmd.Flags().StringVar(&opts.path, "path", "", "Audit log path")
	cmd.Flags().StringVar(&opts.before, "before", "", "Prune rotated logs before this time (30d / RFC3339 / YYYY-MM-DD)")
	cmd.Flags().IntVar(&opts.keepLast, "keep-last", -1, "Keep the newest N rotated logs (0 = delete all rotated logs)")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", true, "Preview matched rotated logs without deleting")
	cmd.Flags().BoolVar(&opts.confirm, "confirm", false, "Confirm deletion; R3 authorization is also required")
	return cmd
}

func runAuditPrune(f *cliFlags, opts auditPruneOptions) error { //nolint:gocyclo // Validation, preview, authorization, and governed mutation stay in command order.
	if opts.before == "" && opts.keepLast < 0 {
		return apperrors.New(apperrors.CodeUsageError, "audit prune requires --before or --keep-last", nil)
	}
	if opts.before != "" && opts.keepLast >= 0 {
		return apperrors.New(apperrors.CodeUsageError, "audit prune accepts only one of --before or --keep-last", nil)
	}
	if opts.keepLast < -1 {
		return apperrors.New(apperrors.CodeUsageError, "--keep-last must be >= 0", nil)
	}
	path, err := auditPath(opts.path)
	if err != nil {
		return err
	}
	path, err = absoluteCleanPath(path)
	if err != nil {
		return err
	}
	if err := validateAuditEvidencePath(path); err != nil {
		return err
	}
	expectedRotated, err := strictAuditRotatedFiles(path)
	if err != nil {
		return err
	}
	candidates, err := auditPruneCandidatesFrom(path, opts, expectedRotated, time.Now().UTC())
	if err != nil {
		return err
	}
	preview := !opts.confirm || (opts.dryRunSet && opts.dryRun)
	if preview {
		return printAuditPrune(f, auditPruneResult{
			DryRun: true,
			Files:  candidates,
			Count:  len(candidates),
		})
	}
	policy, err := authorizeAuditPrune(f)
	if err != nil {
		return err
	}
	configPath, err := dbgovctx.ConfigPath()
	if err != nil {
		return err
	}
	if err := ensurePrivateMutationDirectory(filepath.Dir(configPath)); err != nil {
		return err
	}
	if err := verifyPrivateMutationFileIfExists(filepath.Join(filepath.Dir(configPath), "config.lock")); err != nil {
		return err
	}
	checkpointState := ""
	if err := dbgovctx.WithLockedRead(func(cfg *dbgovctx.Config) error {
		if err := verifyAuditPrunePolicy(cfg, policy); err != nil {
			return err
		}
		if err := ensurePrivateMutationDirectory(filepath.Dir(path)); err != nil {
			return err
		}
		rotated, err := strictAuditRotatedFiles(path)
		if err != nil {
			return err
		}
		lockedCandidates, err := auditPruneCandidatesFrom(path, opts, rotated, time.Now().UTC())
		if err != nil {
			return err
		}
		if !slices.Equal(candidates, lockedCandidates) {
			return apperrors.New(
				apperrors.CodeConflict,
				"audit prune candidates changed after authorization; preview again before confirming",
				nil,
			)
		}
		event := auditCommandEvent(f, dbgaudit.EventTypeAuditPrune)
		event.Target = dbgaudit.Target{ObjectType: "audit", Object: path}
		event.Statement = fmt.Sprintf("pruned %d rotated audit logs", len(candidates))
		event.Risk = "R3"
		metadata := mutationValueMetadata(string(dbgaudit.EventTypeAuditPrune), candidates)
		metadata.Items = len(candidates)
		metadata.Deletes = len(candidates)
		handle, err := beginMutationAudit(f, mutationAuditSpec{
			Action:    string(dbgaudit.EventTypeAuditPrune),
			Event:     event,
			Metadata:  metadata,
			AuditPath: auditControlPath(path),
		})
		if err != nil {
			return err
		}
		pruneResult, pruneErr := coreaudit.PruneRotatedFiles(path, candidates, coreaudit.PruneOptions{
			Confirm:              true,
			ExpectedRotatedFiles: expectedRotated,
		})
		checkpointState = string(pruneResult.CheckpointState)
		deleted := len(pruneResult.DeletedFiles)
		attempted := deleted
		if pruneErr != nil && pruneResult.Started && attempted < len(candidates) {
			attempted++
		}
		return finishMutationAuditProgress(handle, len(candidates), deleted, attempted, pruneErr)
	}); err != nil {
		return err
	}
	return printAuditPrune(f, auditPruneResult{
		DryRun:          false,
		Files:           candidates,
		Count:           len(candidates),
		CheckpointState: checkpointState,
	})
}

func auditControlPath(path string) string {
	return filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+"-control")
}

// pruneAuditUnderLock is retained as a focused test seam. Locking, integrity
// verification, checkpoint advancement, deletion, and directory durability are
// all owned by the core v2 prune transaction.
func pruneAuditUnderLock(
	path string,
	_ auditPruneOptions,
	previewCandidates []string,
) error {
	_, err := coreaudit.PruneRotatedFiles(
		path,
		previewCandidates,
		coreaudit.PruneOptions{Confirm: true, ExpectedRotatedFiles: previewCandidates},
	)
	return err
}

func authorizeAuditPrune(f *cliFlags) (auditPrunePolicy, error) {
	cfg, err := dbgovctx.LoadReadOnly()
	if err != nil {
		return auditPrunePolicy{}, err
	}
	contextName := strings.TrimSpace(cfg.CurrentContext)
	policy := dbgovctx.Context{}
	if contextName != "" {
		var ok bool
		policy, ok = cfg.Contexts[contextName]
		if !ok {
			return auditPrunePolicy{}, apperrors.New(
				apperrors.CodeAuthorizationRequired,
				fmt.Sprintf("current context %q has no persisted policy; refusing audit evidence mutation", contextName),
				nil,
			)
		}
	}
	if err := authorizeContextControl(f, contextName, policy, safety.AllowAuditPrune, f.AllowAuditPrune); err != nil {
		return auditPrunePolicy{}, err
	}
	return auditPrunePolicy{ContextName: contextName, Context: policy}, nil
}

func verifyAuditPrunePolicy(cfg *dbgovctx.Config, expected auditPrunePolicy) error {
	contextName := strings.TrimSpace(cfg.CurrentContext)
	policy := dbgovctx.Context{}
	if contextName != "" {
		var ok bool
		policy, ok = cfg.Contexts[contextName]
		if !ok {
			return contextPolicyChangedError()
		}
	}
	if contextName != expected.ContextName || !reflect.DeepEqual(policy, expected.Context) {
		return contextPolicyChangedError()
	}
	return nil
}

func auditPruneCandidates(path string, opts auditPruneOptions) ([]string, error) {
	rotated, err := strictAuditRotatedFiles(path)
	if err != nil {
		return nil, err
	}
	return auditPruneCandidatesFrom(path, opts, rotated, time.Now().UTC())
}

func auditPruneCandidatesFrom(
	path string,
	opts auditPruneOptions,
	rotated []string,
	now time.Time,
) ([]string, error) {
	if opts.keepLast >= 0 {
		if opts.keepLast >= len(rotated) {
			return []string{}, nil
		}
		return append([]string{}, rotated[:len(rotated)-opts.keepLast]...), nil
	}
	cutoff, err := parseAuditPruneBefore(opts.before, now)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rotated))
	for _, filePath := range rotated {
		ts, _, ok := parseStrictRotatedAuditPath(path, filePath)
		if ok && ts.Before(cutoff) {
			out = append(out, filePath)
		}
	}
	return out, nil
}

func strictAuditRotatedFiles(activePath string) ([]string, error) {
	dir := filepath.Dir(activePath)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []string{}, nil
	}
	if err != nil {
		return nil, apperrors.New(apperrors.CodeLocalIOError, "failed to list rotated audit logs", err)
	}
	files := make([]string, 0)
	for _, entry := range entries {
		candidate := filepath.Join(dir, entry.Name())
		if _, _, ok := parseStrictRotatedAuditPath(activePath, candidate); ok {
			files = append(files, candidate)
		}
	}
	sort.Slice(files, func(i, j int) bool {
		leftTime, leftOrdinal, _ := parseStrictRotatedAuditPath(activePath, files[i])
		rightTime, rightOrdinal, _ := parseStrictRotatedAuditPath(activePath, files[j])
		if !leftTime.Equal(rightTime) {
			return leftTime.Before(rightTime)
		}
		if leftOrdinal != rightOrdinal {
			return leftOrdinal < rightOrdinal
		}
		return mutationPathKey(files[i]) < mutationPathKey(files[j])
	})
	return files, nil
}

func parseStrictRotatedAuditPath(activePath, candidate string) (time.Time, uint64, bool) { //nolint:gocyclo // One fail-closed parser validates every path, timestamp, and ordinal constraint.
	if !samePath(filepath.Dir(activePath), filepath.Dir(candidate)) {
		return time.Time{}, 0, false
	}
	activeName := filepath.Base(activePath)
	candidateName := filepath.Base(candidate)
	prefix := activeName + "."
	candidateKey := mutationPathKey(candidateName)
	prefixKey := mutationPathKey(prefix)
	if !strings.HasPrefix(candidateKey, prefixKey) || !strings.HasSuffix(candidateKey, ".log") {
		return time.Time{}, 0, false
	}
	body := candidateName[len(prefix) : len(candidateName)-len(".log")]
	parts := strings.Split(body, ".")
	if len(parts) < 1 || len(parts) > 2 {
		return time.Time{}, 0, false
	}
	timestamp, err := time.Parse("20060102-150405", parts[0])
	if err != nil || timestamp.UTC().Format("20060102-150405") != parts[0] {
		return time.Time{}, 0, false
	}
	var ordinal uint64
	if len(parts) == 2 {
		if parts[1] == "" || parts[1] == "0" || (len(parts[1]) > 1 && parts[1][0] == '0') {
			return time.Time{}, 0, false
		}
		for _, char := range parts[1] {
			if char < '0' || char > '9' {
				return time.Time{}, 0, false
			}
		}
		ordinal, err = strconv.ParseUint(parts[1], 10, 64)
		if err != nil || ordinal == 0 || ordinal > uint64(^uint(0)>>1) {
			return time.Time{}, 0, false
		}
	}
	return timestamp.UTC(), ordinal, true
}

func looksLikeStrictRotatedAuditPath(activePath, candidate string) bool {
	_, _, ok := parseStrictRotatedAuditPath(activePath, candidate)
	return ok
}

func parseAuditPruneBefore(value string, now time.Time) (time.Time, error) {
	if t, err := coreaudit.ParseTime(value, now); err == nil {
		return t, nil
	}
	t, err := time.Parse("2006-01-02", value)
	if err != nil {
		return time.Time{}, apperrors.New(apperrors.CodeUsageError, "invalid --before: expected relative (30d), RFC3339, or YYYY-MM-DD", nil)
	}
	return t, nil
}

func printAuditPrune(f *cliFlags, result auditPruneResult) error {
	if result.Files == nil {
		result.Files = []string{}
	}
	p := newPrinter(f)
	if f.Output == "json" {
		return p.JSONDataEnvelope(printer.JSONDataEnvelope{Kind: "AuditPruneResult", Data: result})
	}
	if f.Output == "plain" {
		for _, filePath := range result.Files {
			if err := p.Info(filePath); err != nil {
				return err
			}
		}
		return nil
	}
	action := "would-delete"
	if !result.DryRun {
		action = "deleted"
	}
	rows := make([][]string, 0, len(result.Files))
	for _, filePath := range result.Files {
		rows = append(rows, []string{action, filepath.Base(filePath), filePath})
	}
	if len(rows) == 0 {
		return p.Info("(no rotated audit logs matched)")
	}
	if err := p.Table([]string{"ACTION", "FILE", "PATH"}, rows); err != nil {
		return err
	}
	if result.DryRun {
		return p.Info(fmt.Sprintf("(dry-run: deleting %d rotated audit logs requires --confirm --yes --ticket <ticket> --allow-audit-prune)", result.Count))
	}
	return nil
}
