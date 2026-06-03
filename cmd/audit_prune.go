package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/spf13/cobra"

	dbgaudit "github.com/JiangHe12/dbgov-cli/internal/audit"
	"github.com/JiangHe12/opskit-core/apperrors"
	coreaudit "github.com/JiangHe12/opskit-core/audit"
	"github.com/JiangHe12/opskit-core/printer"
)

type auditPruneOptions struct {
	path     string
	before   string
	keepLast int
	dryRun   bool
	confirm  bool
}

type auditPruneResult struct {
	DryRun bool     `json:"dryRun"`
	Files  []string `json:"files"`
	Count  int      `json:"count"`
}

func auditPruneCmd(f *cliFlags) *cobra.Command {
	opts := auditPruneOptions{keepLast: -1, dryRun: true}
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Prune rotated audit logs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAuditPrune(f, opts)
		},
	}
	cmd.Flags().StringVar(&opts.path, "path", "", "Audit log path")
	cmd.Flags().StringVar(&opts.before, "before", "", "Prune rotated logs before this time (30d / RFC3339 / YYYY-MM-DD)")
	cmd.Flags().IntVar(&opts.keepLast, "keep-last", -1, "Keep the newest N rotated logs (0 = delete all rotated logs)")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", true, "Preview matched rotated logs without deleting")
	cmd.Flags().BoolVar(&opts.confirm, "confirm", false, "Actually delete matched rotated logs")
	return cmd
}

func runAuditPrune(f *cliFlags, opts auditPruneOptions) error {
	if err := authorizeRead(f); err != nil {
		return err
	}
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
	candidates, err := auditPruneCandidates(path, opts)
	if err != nil {
		return err
	}
	if opts.confirm {
		event := auditCommandEvent(f, dbgaudit.EventTypeAuditPrune)
		event.Target = dbgaudit.Target{ObjectType: "audit", Object: "prune"}
		event.Statement = fmt.Sprintf("pruned %d rotated audit logs", len(candidates))
		for _, filePath := range candidates {
			if err := os.Remove(filePath); err != nil {
				opErr := apperrors.New(apperrors.CodeLocalIOError, "failed to delete rotated audit log", err)
				emitAudit(f, event, opErr)
				return opErr
			}
		}
		emitAudit(f, event, nil)
	}
	return printAuditPrune(f, auditPruneResult{
		DryRun: !opts.confirm,
		Files:  candidates,
		Count:  len(candidates),
	})
}

func auditPruneCandidates(path string, opts auditPruneOptions) ([]string, error) {
	rotated, err := coreaudit.RotatedFiles(path)
	if err != nil {
		return nil, err
	}
	sortRotatedAuditFiles(path, rotated)
	if opts.keepLast >= 0 {
		if opts.keepLast >= len(rotated) {
			return []string{}, nil
		}
		return append([]string{}, rotated[:len(rotated)-opts.keepLast]...), nil
	}
	cutoff, err := parseAuditPruneBefore(opts.before, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rotated))
	for _, filePath := range rotated {
		ts, ok := coreaudit.RotatedFileTimestamp(path, filePath)
		if ok && ts.Before(cutoff) {
			out = append(out, filePath)
		}
	}
	return out, nil
}

func sortRotatedAuditFiles(activePath string, files []string) {
	sort.SliceStable(files, func(i, j int) bool {
		ti, iok := coreaudit.RotatedFileTimestamp(activePath, files[i])
		tj, jok := coreaudit.RotatedFileTimestamp(activePath, files[j])
		if iok && jok && !ti.Equal(tj) {
			return ti.Before(tj)
		}
		return files[i] < files[j]
	})
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
	files := append([]string{}, result.Files...)
	sort.Strings(files)
	if f.Output == "plain" {
		for _, filePath := range files {
			p.Info(filePath)
		}
		return nil
	}
	action := "would-delete"
	if !result.DryRun {
		action = "deleted"
	}
	rows := make([][]string, 0, len(files))
	for _, filePath := range files {
		rows = append(rows, []string{action, filepath.Base(filePath), filePath})
	}
	if len(rows) == 0 {
		p.Info("(no rotated audit logs matched)")
		return nil
	}
	p.Table([]string{"ACTION", "FILE", "PATH"}, rows)
	if result.DryRun {
		p.Info(fmt.Sprintf("(dry-run: pass --confirm to delete %d rotated audit logs)", result.Count))
	}
	return nil
}
