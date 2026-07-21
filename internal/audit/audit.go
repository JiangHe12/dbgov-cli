// Package audit defines dbgov audit records that are written by the shared core audit engine.
package audit

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/JiangHe12/opskit-core/v2/apperrors"
	coreaudit "github.com/JiangHe12/opskit-core/v2/audit"
)

const (
	APIVersion     = "dbgov-cli.io/audit/v1"
	KindAuditEvent = "AuditEvent"

	maxFingerprintBytes = 1024 * 1024 * 1024
	maxMutationCount    = 1_000_000_000
	maxErrorCodeBytes   = 64
)

type EventType string

const (
	EventTypeQuery             EventType = "query"
	EventTypeExplain           EventType = "explain"
	EventTypeSchemaDump        EventType = "schema.dump"
	EventTypeSchemaList        EventType = "schema.list"
	EventTypeSchemaDescribe    EventType = "schema.describe"
	EventTypeSchemaPlan        EventType = "schema.plan"
	EventTypeSchemaDiff        EventType = "schema.diff"
	EventTypeSchemaApply       EventType = "schema.apply"
	EventTypeDataExec          EventType = "data.exec"
	EventTypeExport            EventType = "export"
	EventTypeImport            EventType = "import"
	EventTypeReconcile         EventType = "reconcile"
	EventTypeRollback          EventType = "rollback"
	EventTypeDoctor            EventType = "doctor"
	EventTypeAuditQuery        EventType = "audit.query"
	EventTypeAuditVerify       EventType = "audit.verify"
	EventTypeAuditPrune        EventType = "audit.prune"
	EventTypeContextSet        EventType = "context.set"
	EventTypeContextUse        EventType = "context.use"
	EventTypeContextDelete     EventType = "context.delete"
	EventTypeContextExport     EventType = "context.export"
	EventTypeContextImport     EventType = "context.import"
	EventTypeRoleAssign        EventType = "role.assign"
	EventTypeRoleRevoke        EventType = "role.revoke"
	EventTypeCredentialMigrate EventType = "credential.migrate" //nolint:gosec // Audit event type name, not a credential.
	EventTypeInstallSkill      EventType = "install.skill"
)

const (
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
	StatusDenied    = "denied"
	StatusPending   = "pending"
	StatusPartial   = "partial_failed"
)

type Event struct {
	APIVersion                 string            `json:"apiVersion"`
	Kind                       string            `json:"kind"`
	EventID                    string            `json:"eventId"`
	Timestamp                  time.Time         `json:"timestamp"`
	EventType                  EventType         `json:"eventType"`
	Operator                   string            `json:"operator"`
	Context                    Context           `json:"context"`
	Ticket                     string            `json:"ticket,omitempty"` // Legacy read compatibility; cleared before persistence/output.
	TicketFingerprint          string            `json:"ticketFingerprint,omitempty"`
	TicketBytes                int               `json:"ticketBytes,omitempty"`
	Reason                     string            `json:"reason,omitempty"` // Legacy read compatibility; cleared before persistence/output.
	ReasonFingerprint          string            `json:"reasonFingerprint,omitempty"`
	ReasonBytes                int               `json:"reasonBytes,omitempty"`
	Target                     Target            `json:"target"`
	Role                       string            `json:"role,omitempty"`
	Statement                  string            `json:"statement,omitempty"` // Legacy read compatibility; cleared before persistence/output.
	StatementFingerprint       string            `json:"statementFingerprint,omitempty"`
	StatementBytes             int               `json:"statementBytes,omitempty"`
	Risk                       string            `json:"risk"`
	ImpactRows                 *int              `json:"impactRows,omitempty"`
	AffectedRows               *int64            `json:"affectedRows,omitempty"`
	SnapshotID                 string            `json:"snapshotId,omitempty"` // Legacy read compatibility; cleared before persistence/output.
	SnapshotFingerprint        string            `json:"snapshotFingerprint,omitempty"`
	SnapshotBytes              int               `json:"snapshotBytes,omitempty"`
	Executed                   int               `json:"executed,omitempty"`
	FailedStatement            string            `json:"failedStatement,omitempty"` // Legacy read compatibility; cleared before persistence/output.
	FailedStatementFingerprint string            `json:"failedStatementFingerprint,omitempty"`
	FailedStatementBytes       int               `json:"failedStatementBytes,omitempty"`
	Destructive                bool              `json:"destructive,omitempty"`
	DryRun                     bool              `json:"dryRun,omitempty"`
	Status                     string            `json:"status"`
	Error                      *ErrorInfo        `json:"error,omitempty"`
	ErrorFingerprint           string            `json:"errorFingerprint,omitempty"`
	ErrorBytes                 int               `json:"errorBytes,omitempty"`
	MutationID                 string            `json:"mutationId,omitempty"`
	Phase                      string            `json:"phase,omitempty"`
	Action                     string            `json:"action,omitempty"`
	Metadata                   *MutationMetadata `json:"metadata,omitempty"`
	Outcome                    *MutationOutcome  `json:"outcome,omitempty"`
}

type Context struct {
	Name      string `json:"name,omitempty"`
	Env       string `json:"env,omitempty"`
	Protected bool   `json:"protected,omitempty"`
}

type Target struct {
	Database    string `json:"database,omitempty"` // Legacy read compatibility; cleared before persistence/output.
	ObjectType  string `json:"objectType,omitempty"`
	Object      string `json:"object,omitempty"` // Legacy read compatibility; cleared before persistence/output.
	Fingerprint string `json:"fingerprint,omitempty"`
	Bytes       int    `json:"bytes,omitempty"`
}

type ErrorInfo struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"` // Legacy read compatibility; cleared before persistence/output.
}

// MutationMetadata contains bounded, non-secret descriptors of a mutation.
type MutationMetadata struct {
	PayloadFingerprint string `json:"payloadFingerprint,omitempty"`
	PayloadBytes       int    `json:"payloadBytes,omitempty"`
	Items              int    `json:"items,omitempty"`
	Creates            int    `json:"creates,omitempty"`
	Updates            int    `json:"updates,omitempty"`
	Deletes            int    `json:"deletes,omitempty"`
}

// MutationOutcome contains bounded mutation results without raw backend text.
type MutationOutcome struct {
	Status       string `json:"status"`
	ErrorCode    string `json:"errorCode,omitempty"`
	Succeeded    int    `json:"succeeded,omitempty"`
	Failed       int    `json:"failed,omitempty"`
	Uncertain    int    `json:"uncertain,omitempty"`
	Skipped      int    `json:"skipped,omitempty"`
	Executed     int    `json:"executed,omitempty"`
	AffectedRows *int64 `json:"affectedRows,omitempty"`
}

func New(eventType EventType, operator string, context Context, target Target) Event {
	event := Event{
		EventType: eventType,
		Operator:  operator,
		Context:   context,
		Target:    target,
		Risk:      "R0",
		Status:    StatusSucceeded,
	}
	event.fillEnvelope()
	return event
}

func (e Event) MarshalJSON() ([]byte, error) {
	type alias Event
	e.fillEnvelope()
	return json.Marshal(alias(e))
}

// Append sanitizes legacy raw fields and writes through the shared core engine.
func Append(path string, event Event, opts coreaudit.Options) error {
	_, err := AppendWithResult(path, event, opts)
	return err
}

// AppendWithResult sanitizes legacy raw fields and reports the durable commit
// state returned by the shared core engine.
func AppendWithResult(path string, event Event, opts coreaudit.Options) (coreaudit.AppendResult, error) {
	return coreaudit.AppendRecordWithResult(path, Sanitize(event), opts)
}

// Sanitize replaces raw free text with domain-separated SHA-256 fingerprints
// and byte lengths. Fingerprints are correlation aids, not secret storage.
func Sanitize(event Event) Event { //nolint:gocyclo // Every legacy raw field and persisted derived field is sanitized in one boundary.
	event.TicketFingerprint, event.TicketBytes = mergeFingerprint(
		"ticket", event.Ticket, event.TicketFingerprint, event.TicketBytes,
	)
	event.Ticket = ""
	event.ReasonFingerprint, event.ReasonBytes = mergeFingerprint(
		"reason", event.Reason, event.ReasonFingerprint, event.ReasonBytes,
	)
	event.Reason = ""
	event.StatementFingerprint, event.StatementBytes = mergeFingerprint(
		"statement", event.Statement, event.StatementFingerprint, event.StatementBytes,
	)
	event.Statement = ""
	event.FailedStatementFingerprint, event.FailedStatementBytes = mergeFingerprint(
		"failed-statement", event.FailedStatement, event.FailedStatementFingerprint, event.FailedStatementBytes,
	)
	event.FailedStatement = ""
	event.SnapshotFingerprint, event.SnapshotBytes = mergeFingerprint(
		"snapshot", event.SnapshotID, event.SnapshotFingerprint, event.SnapshotBytes,
	)
	event.SnapshotID = ""
	rawTarget := strings.Join([]string{event.Target.Database, event.Target.ObjectType, event.Target.Object}, "\x00")
	if event.Target.Database != "" || event.Target.Object != "" {
		event.Target.Fingerprint, event.Target.Bytes = mergeFingerprint(
			"target", rawTarget, event.Target.Fingerprint, event.Target.Bytes,
		)
	}
	event.Target.Database = ""
	event.Target.Object = ""
	if event.Error != nil {
		cloned := *event.Error
		event.ErrorFingerprint, event.ErrorBytes = mergeFingerprint(
			"error", cloned.Message, event.ErrorFingerprint, event.ErrorBytes,
		)
		cloned.Message = ""
		if cloned.Code == "" {
			event.Error = nil
		} else {
			event.Error = &cloned
		}
	}
	sanitizeFingerprintPair(&event.TicketFingerprint, &event.TicketBytes)
	sanitizeFingerprintPair(&event.ReasonFingerprint, &event.ReasonBytes)
	sanitizeFingerprintPair(&event.StatementFingerprint, &event.StatementBytes)
	sanitizeFingerprintPair(&event.FailedStatementFingerprint, &event.FailedStatementBytes)
	sanitizeFingerprintPair(&event.SnapshotFingerprint, &event.SnapshotBytes)
	sanitizeFingerprintPair(&event.Target.Fingerprint, &event.Target.Bytes)
	sanitizeFingerprintPair(&event.ErrorFingerprint, &event.ErrorBytes)
	if event.Error != nil && !ValidErrorCode(event.Error.Code) {
		event.Error = nil
	}
	if event.Executed < 0 || event.Executed > maxMutationCount {
		event.Executed = 0
	}
	if event.ImpactRows != nil && (*event.ImpactRows < 0 || *event.ImpactRows > maxMutationCount) {
		event.ImpactRows = nil
	}
	if event.AffectedRows != nil && *event.AffectedRows < 0 {
		event.AffectedRows = nil
	}
	if event.Metadata != nil {
		cloned := *event.Metadata
		if ValidMutationMetadata(&cloned) {
			event.Metadata = &cloned
		} else {
			event.Metadata = nil
		}
	}
	if event.Outcome != nil {
		cloned := *event.Outcome
		items := -1
		if event.Metadata != nil {
			items = event.Metadata.Items
		}
		if items >= 0 && ValidMutationOutcome(&cloned, items) {
			event.Outcome = &cloned
		} else {
			event.Outcome = nil
		}
	}
	return event
}

// Fingerprint returns a deterministic domain-separated digest and byte length.
func Fingerprint(domain, value string) (string, int) {
	if value == "" {
		return "", 0
	}
	hash := sha256.New()
	_, _ = io.WriteString(hash, "dbgov-cli.io/audit-fingerprint/v1")
	_, _ = hash.Write([]byte{0})
	_, _ = io.WriteString(hash, domain)
	_, _ = hash.Write([]byte{0})
	_, _ = io.WriteString(hash, value)
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), len([]byte(value))
}

func mergeFingerprint(domain, raw, fingerprint string, size int) (string, int) {
	if raw != "" {
		return Fingerprint(domain, raw)
	}
	return fingerprint, size
}

// ValidFingerprint reports whether a fingerprint and byte length form a
// canonical, bounded audit correlation value.
func ValidFingerprint(fingerprint string, size int) bool {
	if fingerprint == "" {
		return size == 0
	}
	if size <= 0 || size > maxFingerprintBytes ||
		len(fingerprint) != len("sha256:")+sha256.Size*2 ||
		!strings.HasPrefix(fingerprint, "sha256:") ||
		fingerprint != strings.ToLower(fingerprint) {
		return false
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(fingerprint, "sha256:"))
	return err == nil && len(decoded) == sha256.Size
}

// ValidMutationMetadata validates bounded mutation descriptors.
func ValidMutationMetadata(metadata *MutationMetadata) bool {
	if metadata == nil || !ValidFingerprint(metadata.PayloadFingerprint, metadata.PayloadBytes) {
		return false
	}
	counts := []int{metadata.Items, metadata.Creates, metadata.Updates, metadata.Deletes}
	for _, count := range counts {
		if count < 0 || count > maxMutationCount {
			return false
		}
	}
	return int64(metadata.Creates)+int64(metadata.Updates)+int64(metadata.Deletes) <= int64(metadata.Items)
}

// ValidMutationOutcome validates an outcome against the intent item count.
func ValidMutationOutcome(outcome *MutationOutcome, items int) bool {
	if outcome == nil || items < 0 || items > maxMutationCount {
		return false
	}
	switch outcome.Status {
	case StatusSucceeded:
		if outcome.ErrorCode != "" || outcome.Uncertain != 0 {
			return false
		}
	case StatusFailed, StatusPartial:
		if !ValidErrorCode(outcome.ErrorCode) {
			return false
		}
	default:
		return false
	}
	counts := []int{outcome.Succeeded, outcome.Failed, outcome.Uncertain, outcome.Skipped, outcome.Executed}
	for _, count := range counts {
		if count < 0 || count > maxMutationCount {
			return false
		}
	}
	if int64(outcome.Succeeded)+int64(outcome.Failed)+int64(outcome.Uncertain)+int64(outcome.Skipped) != int64(items) ||
		outcome.Executed > items {
		return false
	}
	return outcome.AffectedRows == nil || *outcome.AffectedRows >= 0
}

// ValidErrorCode accepts only bounded symbolic error codes.
func ValidErrorCode(code string) bool {
	if code == "" || len(code) > maxErrorCodeBytes {
		return false
	}
	for _, char := range code {
		if (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '_' {
			return false
		}
	}
	switch apperrors.ErrorCode(code) {
	case apperrors.CodeUsageError,
		apperrors.CodeNetworkError,
		apperrors.CodeAuthFailed,
		apperrors.CodeResourceNotFound,
		apperrors.CodeResourceAlreadyExists,
		apperrors.CodeLocalIOError,
		apperrors.CodeServerError,
		apperrors.CodeBackendUnreachable,
		apperrors.CodeBackendError,
		apperrors.CodeAuthorizationRequired,
		apperrors.CodeValidationFailed,
		apperrors.CodeConflict,
		apperrors.CodePartialFailure,
		apperrors.CodeNoChangeRequired,
		apperrors.CodeUnsupportedProtocol,
		apperrors.CodeNotImplemented,
		apperrors.CodeCredentialStoreError,
		apperrors.CodeCredentialStoreMissing,
		apperrors.ErrorCode("AUDIT_INCOMPLETE"):
		return true
	default:
		return false
	}
}

func sanitizeFingerprintPair(fingerprint *string, size *int) {
	if !ValidFingerprint(*fingerprint, *size) {
		*fingerprint = ""
		*size = 0
	}
}

func (e *Event) fillEnvelope() {
	if e.APIVersion == "" {
		e.APIVersion = APIVersion
	}
	if e.Kind == "" {
		e.Kind = KindAuditEvent
	}
	if e.EventID == "" {
		e.EventID = newEventID()
	}
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
	if e.Status == "" {
		e.Status = StatusSucceeded
	}
	if e.Risk == "" {
		e.Risk = "R0"
	}
}

func newEventID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err == nil {
		return "evt-" + hex.EncodeToString(b[:])
	}
	return fmt.Sprintf("evt-%d", time.Now().UTC().UnixNano())
}
