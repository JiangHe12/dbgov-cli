// Package audit defines dbgov audit records that are written by the shared core audit engine.
package audit

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

const (
	APIVersion     = "dbgov.io/audit/v1"
	KindAuditEvent = "AuditEvent"
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
	EventTypeRoleAssign        EventType = "role.assign"
	EventTypeRoleRevoke        EventType = "role.revoke"
	EventTypeCredentialMigrate EventType = "credential.migrate"
)

const (
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
	StatusDenied    = "denied"
)

type Event struct {
	APIVersion      string     `json:"apiVersion"`
	Kind            string     `json:"kind"`
	EventID         string     `json:"eventId"`
	Timestamp       time.Time  `json:"timestamp"`
	EventType       EventType  `json:"eventType"`
	Operator        string     `json:"operator"`
	Context         Context    `json:"context"`
	Ticket          string     `json:"ticket,omitempty"`
	Reason          string     `json:"reason,omitempty"`
	Target          Target     `json:"target"`
	Role            string     `json:"role,omitempty"`
	Statement       string     `json:"statement,omitempty"`
	Risk            string     `json:"risk"`
	ImpactRows      *int       `json:"impactRows,omitempty"`
	AffectedRows    *int64     `json:"affectedRows,omitempty"`
	SnapshotID      string     `json:"snapshotId,omitempty"`
	Executed        int        `json:"executed,omitempty"`
	FailedStatement string     `json:"failedStatement,omitempty"`
	Destructive     bool       `json:"destructive,omitempty"`
	DryRun          bool       `json:"dryRun,omitempty"`
	Status          string     `json:"status"`
	Error           *ErrorInfo `json:"error,omitempty"`
}

type Context struct {
	Name      string `json:"name,omitempty"`
	Env       string `json:"env,omitempty"`
	Protected bool   `json:"protected,omitempty"`
}

type Target struct {
	Database   string `json:"database,omitempty"`
	ObjectType string `json:"objectType,omitempty"`
	Object     string `json:"object,omitempty"`
}

type ErrorInfo struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
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
