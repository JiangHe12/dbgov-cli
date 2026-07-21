package cmd

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/JiangHe12/opskit-core/v2/apperrors"
	coreaudit "github.com/JiangHe12/opskit-core/v2/audit"
	"github.com/JiangHe12/opskit-core/v2/printer"

	dbgaudit "github.com/JiangHe12/dbgov-cli/internal/audit"
)

type auditQueryOptions struct {
	since    string
	until    string
	operator string
	event    string
	status   string
	risk     string
	context  string
	limit    int
	reverse  bool
	path     string
}

type auditVerifyOptions struct {
	strict bool
	path   string
}

type auditQueryResult struct {
	Events    []dbgaudit.Event `json:"events"`
	Malformed int              `json:"malformed"`
}

func newAuditCmd(f *cliFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "audit",
		Short: "Query and verify audit logs",
	}
	cmd.AddCommand(auditQueryCmd(f), auditPruneCmd(f), auditVerifyCmd(f))
	return cmd
}

func auditQueryCmd(f *cliFlags) *cobra.Command {
	var opts auditQueryOptions
	cmd := &cobra.Command{
		Use:   "query",
		Short: "Query audit events",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAuditQuery(f, opts)
		},
	}
	cmd.Flags().StringVar(&opts.since, "since", "", "Only include events at or after this time")
	cmd.Flags().StringVar(&opts.until, "until", "", "Only include events at or before this time")
	cmd.Flags().StringVar(&opts.operator, "operator", "", "Filter by operator")
	cmd.Flags().StringVar(&opts.event, "type", "", "Filter by event type")
	cmd.Flags().StringVar(&opts.status, "status", "", "Filter by dbgov event status")
	cmd.Flags().StringVar(&opts.risk, "risk", "", "Filter by dbgov risk label")
	cmd.Flags().StringVar(&opts.context, "context", "", "Filter by dbgov context name")
	cmd.Flags().IntVar(&opts.limit, "limit", 0, "Limit results after all filters")
	cmd.Flags().BoolVar(&opts.reverse, "reverse", false, "Return newest matching events first")
	cmd.Flags().StringVar(&opts.path, "path", "", "Audit log path")
	return cmd
}

func auditVerifyCmd(f *cliFlags) *cobra.Command {
	var opts auditVerifyOptions
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Verify audit log integrity",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAuditVerify(f, opts)
		},
	}
	cmd.Flags().BoolVar(&opts.strict, "strict", false, "Fail when verification reports malformed entries or invariant violations")
	cmd.Flags().StringVar(&opts.path, "path", "", "Audit log path")
	return cmd
}

func runAuditQuery(f *cliFlags, opts auditQueryOptions) error {
	if err := authorizeRead(f); err != nil {
		return err
	}
	path, err := auditPath(opts.path)
	if err != nil {
		event := auditCommandEvent(f, dbgaudit.EventTypeAuditQuery)
		emitAudit(f, event, err)
		return err
	}
	filter, err := auditRawFilter(opts)
	if err != nil {
		event := auditCommandEvent(f, dbgaudit.EventTypeAuditQuery)
		emitAudit(f, event, err)
		return err
	}
	raw, err := coreaudit.QueryRaw(path, filter)
	if err != nil {
		event := auditCommandEvent(f, dbgaudit.EventTypeAuditQuery)
		emitAudit(f, event, err)
		return err
	}
	result := auditQueryResult{Events: []dbgaudit.Event{}, Malformed: raw.MalformedEntries}
	for _, record := range raw.Records {
		var event dbgaudit.Event
		if err := json.Unmarshal([]byte(record.Line), &event); err != nil {
			result.Malformed++
			continue
		}
		event = dbgaudit.Sanitize(event)
		if !matchesDbgovAuditFilter(event, opts) {
			continue
		}
		result.Events = append(result.Events, event)
	}
	if opts.reverse {
		reverseAuditEvents(result.Events)
	}
	if opts.limit > 0 && len(result.Events) > opts.limit {
		result.Events = result.Events[:opts.limit]
	}
	event := auditCommandEvent(f, dbgaudit.EventTypeAuditQuery)
	emitAudit(f, event, nil)
	return printAuditQuery(f, result)
}

func runAuditVerify(f *cliFlags, opts auditVerifyOptions) error {
	if err := authorizeRead(f); err != nil {
		return err
	}
	path, err := auditPath(opts.path)
	if err != nil {
		event := auditCommandEvent(f, dbgaudit.EventTypeAuditVerify)
		emitAudit(f, event, err)
		return err
	}
	result, err := coreaudit.Verify(path, coreaudit.VerifyOptions{})
	strictErr := strictVerifyError(result, opts.strict)
	event := auditCommandEvent(f, dbgaudit.EventTypeAuditVerify)
	if err != nil {
		emitAudit(f, event, err)
		return err
	}
	emitAudit(f, event, strictErr)
	if printErr := printAuditVerify(f, result); printErr != nil {
		return printErr
	}
	return strictErr
}

func auditPath(path string) (string, error) {
	if path != "" {
		return path, nil
	}
	return coreaudit.DefaultPath()
}

func auditRawFilter(opts auditQueryOptions) (coreaudit.Filter, error) {
	now := time.Now().UTC()
	filter := coreaudit.Filter{EventType: opts.event, Operator: opts.operator}
	if opts.since != "" {
		since, err := coreaudit.ParseTime(opts.since, now)
		if err != nil {
			return filter, err
		}
		filter.Since = &since
	}
	if opts.until != "" {
		until, err := coreaudit.ParseTime(opts.until, now)
		if err != nil {
			return filter, err
		}
		filter.Until = &until
	}
	return filter, nil
}

func matchesDbgovAuditFilter(event dbgaudit.Event, opts auditQueryOptions) bool {
	if opts.status != "" && event.Status != opts.status {
		return false
	}
	if opts.risk != "" && event.Risk != opts.risk {
		return false
	}
	if opts.context != "" && event.Context.Name != opts.context {
		return false
	}
	return true
}

func reverseAuditEvents(events []dbgaudit.Event) {
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}
}

func strictVerifyError(result coreaudit.VerifyResult, strict bool) error {
	if !strict {
		return nil
	}
	if result.HasProblems() {
		return apperrors.New(apperrors.CodeValidationFailed, "audit verification failed", nil)
	}
	return nil
}

func auditCommandEvent(f *cliFlags, eventType dbgaudit.EventType) dbgaudit.Event {
	return dbgaudit.New(eventType, currentOperator(f), dbgaudit.Context{}, dbgaudit.Target{ObjectType: "audit", Object: string(eventType)})
}

func printAuditQuery(f *cliFlags, result auditQueryResult) error {
	if result.Events == nil {
		result.Events = []dbgaudit.Event{}
	}
	p := newPrinter(f)
	if f.Output == "json" {
		return p.JSONDataEnvelope(printer.JSONDataEnvelope{Kind: "AuditQueryResult", Data: result})
	}
	rows := make([][]string, 0, len(result.Events))
	for _, event := range result.Events {
		rows = append(rows, []string{
			event.Timestamp.Format(time.RFC3339),
			string(event.EventType),
			event.Operator,
			event.Risk,
			event.Status,
			event.Context.Name,
			event.TicketFingerprint,
			event.Target.Fingerprint,
		})
	}
	if err := p.Table([]string{"TIMESTAMP", "TYPE", "OPERATOR", "RISK", "STATUS", "CONTEXT", "TICKET_FINGERPRINT", "TARGET_FINGERPRINT"}, rows); err != nil {
		return err
	}
	if result.Malformed > 0 {
		return p.Info(fmt.Sprintf("\nMalformed entries skipped: %d", result.Malformed))
	}
	return nil
}

func printAuditVerify(f *cliFlags, result coreaudit.VerifyResult) error {
	p := newPrinter(f)
	if f.Output == "json" {
		return p.JSONDataEnvelope(printer.JSONDataEnvelope{Kind: "AuditVerifyResult", Data: result})
	}
	return p.Table([]string{
		"TOTAL",
		"VALID",
		"MALFORMED",
		"SCHEMA_ERRORS",
		"TIMESTAMP_ORDER_VIOLATIONS",
		"AUTHENTICATED",
		"LEGACY_UNAUTHENTICATED",
		"ENCRYPTED_OPAQUE",
		"INTEGRITY_ERRORS",
		"SEQUENCE_VIOLATIONS",
		"CHECKPOINT_VIOLATIONS",
		"TRUNCATION_DETECTED",
		"LOCK_PRESENT",
	}, [][]string{{
		fmt.Sprint(result.Total),
		fmt.Sprint(result.Valid),
		fmt.Sprint(result.Malformed),
		fmt.Sprint(result.SchemaErrors),
		fmt.Sprint(result.TimestampOrderViolations),
		fmt.Sprint(result.Authenticated),
		fmt.Sprint(result.LegacyUnauthenticated),
		fmt.Sprint(result.EncryptedOpaque),
		fmt.Sprint(result.IntegrityErrors),
		fmt.Sprint(result.SequenceViolations),
		fmt.Sprint(result.CheckpointViolations),
		fmt.Sprint(result.TruncationDetected),
		fmt.Sprint(result.Lock.Present),
	}})
}
