package cmd

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/JiangHe12/opskit-core/v2/apperrors"
	coreaudit "github.com/JiangHe12/opskit-core/v2/audit"
	"github.com/JiangHe12/opskit-core/v2/lockfile"

	dbgaudit "github.com/JiangHe12/dbgov-cli/internal/audit"
)

const (
	codeAuditIncomplete         apperrors.ErrorCode = "AUDIT_INCOMPLETE"
	mutationAuditAPIVersion                         = "dbgov-cli.io/mutation-audit/v1"
	mutationAuditKind                               = "MutationAuditRecord"
	mutationAuditPhaseIntent                        = "intent"
	mutationAuditPhaseOutcome                       = "outcome"
	mutationAuditSpoolSuffix                        = ".outcome-spool"
	mutationAuditUncertainMark                      = ".indeterminate"
	maxMutationSpoolRecordBytes                     = 1024 * 1024
)

type mutationAuditSpec struct {
	Action    string
	Event     dbgaudit.Event
	Metadata  dbgaudit.MutationMetadata
	AuditPath string
}

type mutationAuditHandle struct {
	f    *cliFlags
	id   string
	path string
	spec mutationAuditSpec
}

type mutationAuditRuntime struct {
	appendRecord           func(string, dbgaudit.Event, coreaudit.Options) error
	appendRecordWithResult func(string, dbgaudit.Event, coreaudit.Options) (coreaudit.AppendResult, error)
	now                    func() time.Time
	random                 io.Reader
}

var productionMutationAuditRuntime = mutationAuditRuntime{
	appendRecordWithResult: dbgaudit.AppendWithResult,
	now:                    func() time.Time { return time.Now().UTC() },
	random:                 rand.Reader,
}

var (
	mutationAuditSpoolMu   sync.Mutex
	mutationAuditPathLocks sync.Map
)

func beginMutationAudit(f *cliFlags, spec mutationAuditSpec) (*mutationAuditHandle, error) { //nolint:gocyclo // Intent validation and durable pre-side-effect persistence form one boundary.
	spec.Action = strings.TrimSpace(spec.Action)
	if spec.Action == "" {
		return nil, apperrors.New(apperrors.CodeValidationFailed, "mutation audit action is required", nil)
	}
	if !knownMutationAuditAction(spec.Action) ||
		string(spec.Event.EventType) != spec.Action ||
		!dbgaudit.ValidMutationMetadata(&spec.Metadata) {
		return nil, apperrors.New(apperrors.CodeValidationFailed, "invalid mutation audit intent schema", nil)
	}
	path := strings.TrimSpace(spec.AuditPath)
	if path == "" && f != nil {
		path = strings.TrimSpace(f.mutationAuditPath)
	}
	if path == "" {
		var err error
		path, err = coreaudit.DefaultPath()
		if err != nil {
			return nil, apperrors.New(apperrors.CodeLocalIOError, "failed to resolve mutation audit path", nil)
		}
	}
	path, err := absoluteCleanPath(path)
	if err != nil {
		return nil, err
	}
	if err := validateAuditEvidencePath(path); err != nil {
		return nil, err
	}
	if err := ensurePrivateMutationDirectory(filepath.Dir(path)); err != nil {
		return nil, err
	}
	var handle *mutationAuditHandle
	if err := withMutationAuditPathLock(path, func() error {
		if err := ensureMutationSpoolDirectory(mutationAuditSpoolPath(path)); err != nil {
			return auditIncompleteError("", true)
		}
		if err := replayMutationAuditSpool(f, path); err != nil {
			return auditIncompleteError("", false)
		}
		id, err := newMutationID(mutationAuditRuntimeFor(f).random)
		if err != nil {
			return err
		}
		handle = &mutationAuditHandle{f: f, id: id, path: path, spec: spec}
		record := dbgaudit.Sanitize(handle.record(
			mutationAuditPhaseIntent,
			nil,
			mutationAuditRuntimeFor(f).now(),
		))
		appendResult, appendErr := appendMutationAuditRecord(f, path, record)
		if appendResult.State == coreaudit.AppendCommitCommitted && appendErr == nil {
			return nil
		}
		if appendResult.State == coreaudit.AppendCommitNotCommitted {
			return apperrors.New(apperrors.CodeLocalIOError, "failed to persist mutation intent", appendErr)
		}
		outcome := dbgaudit.MutationOutcome{
			Status:    dbgaudit.StatusFailed,
			ErrorCode: string(codeAuditIncomplete),
			Skipped:   skippedMutationItems(spec.Metadata.Items),
		}
		fallback := dbgaudit.Sanitize(handle.record(
			mutationAuditPhaseOutcome,
			&outcome,
			mutationAuditRuntimeFor(f).now(),
		))
		spoolErr := spoolMutationAuditOutcome(f, path, fallback)
		return auditIncompleteError(handle.id, spoolErr != nil)
	}); err != nil {
		return nil, err
	}
	return handle, nil
}

func skippedMutationItems(items int) int {
	if items < 0 {
		return 0
	}
	return items
}

func finishMutationAudit(
	handle *mutationAuditHandle,
	outcome dbgaudit.MutationOutcome,
	operationErr error,
) error {
	if handle == nil {
		return apperrors.New(apperrors.CodeValidationFailed, "mutation audit handle is required", nil)
	}
	if outcome.Status == "" {
		if operationErr == nil {
			outcome.Status = dbgaudit.StatusSucceeded
		} else {
			outcome.Status = dbgaudit.StatusFailed
		}
	}
	if operationErr != nil && outcome.ErrorCode == "" {
		outcome.ErrorCode = string(apperrors.AsAppError(operationErr).Code)
	}
	if !dbgaudit.ValidMutationOutcome(&outcome, handle.spec.Metadata.Items) {
		return auditIncompleteError(handle.id, true)
	}
	var record dbgaudit.Event
	recordReady := false
	appendAttempted := false
	appendState := coreaudit.AppendCommitNotCommitted
	var finishErr error
	if err := withMutationAuditPathLock(handle.path, func() error {
		if err := prepareMutationAuditQueue(handle.f, handle.path); err != nil {
			return err
		}
		record = dbgaudit.Sanitize(handle.record(
			mutationAuditPhaseOutcome,
			&outcome,
			mutationAuditRuntimeFor(handle.f).now(),
		))
		recordReady = true
		appendAttempted = true
		appendState, finishErr = appendOrSpoolMutationAuditOutcome(handle, record)
		if finishErr == nil {
			finishErr = operationErr
		}
		return nil //nolint:nilerr // Outcome persistence errors are returned after the path lock is released.
	}); err != nil {
		if appendAttempted {
			if appendState == coreaudit.AppendCommitNotCommitted && finishErr != nil {
				return finishErr
			}
			return auditStateIncompleteError(handle.id, appendState)
		}
		if !recordReady {
			record = dbgaudit.Sanitize(handle.record(
				mutationAuditPhaseOutcome,
				&outcome,
				mutationAuditRuntimeFor(handle.f).now(),
			))
		}
		spoolErr := spoolMutationAuditOutcome(handle.f, handle.path, record)
		return auditIncompleteError(handle.id, spoolErr != nil)
	}
	return finishErr
}

func appendOrSpoolMutationAuditOutcome(
	handle *mutationAuditHandle,
	record dbgaudit.Event,
) (coreaudit.AppendCommitState, error) {
	appendResult, appendErr := appendMutationAuditRecord(handle.f, handle.path, record)
	if appendResult.State == coreaudit.AppendCommitCommitted && appendErr == nil {
		return appendResult.State, nil
	}
	if appendResult.State != coreaudit.AppendCommitNotCommitted {
		return appendResult.State, auditStateIncompleteError(handle.id, appendResult.State)
	}
	spoolErr := spoolMutationAuditOutcome(handle.f, handle.path, record)
	return appendResult.State, auditIncompleteError(handle.id, spoolErr != nil)
}

func finishBatchMutationAudit(
	handle *mutationAuditHandle,
	total int,
	succeeded int,
	operationErr error,
) error {
	attempted := succeeded
	if operationErr != nil && succeeded < total {
		attempted++
	}
	return finishMutationAuditProgress(handle, total, succeeded, attempted, operationErr)
}

func finishSkippedMutationAudit(
	handle *mutationAuditHandle,
	total int,
	operationErr error,
) error {
	return finishMutationAuditProgress(handle, total, 0, 0, operationErr)
}

func finishMutationAuditProgress(
	handle *mutationAuditHandle,
	total int,
	succeeded int,
	attempted int,
	operationErr error,
) error {
	if handle == nil {
		return apperrors.New(apperrors.CodeValidationFailed, "mutation audit handle is required", nil)
	}
	if total < 0 || succeeded < 0 || attempted < succeeded || attempted > total {
		return auditIncompleteError(handle.id, true)
	}
	if operationErr == nil && (succeeded != total || attempted != total) {
		return auditIncompleteError(handle.id, true)
	}
	failed := attempted - succeeded
	skipped := total - attempted
	status := dbgaudit.StatusSucceeded
	if operationErr != nil {
		status = dbgaudit.StatusFailed
		if succeeded > 0 {
			status = dbgaudit.StatusPartial
		}
	}
	return finishMutationAudit(handle, dbgaudit.MutationOutcome{
		Status:    status,
		Succeeded: succeeded,
		Failed:    failed,
		Skipped:   skipped,
		Executed:  succeeded,
	}, operationErr)
}

func (handle *mutationAuditHandle) record(
	phase string,
	outcome *dbgaudit.MutationOutcome,
	timestamp time.Time,
) dbgaudit.Event {
	event := handle.spec.Event
	event.APIVersion = mutationAuditAPIVersion
	event.Kind = mutationAuditKind
	event.EventID = ""
	event.Timestamp = timestamp.UTC()
	event.MutationID = handle.id
	event.Phase = phase
	event.Action = handle.spec.Action
	event.Metadata = &handle.spec.Metadata
	event.Outcome = outcome
	event.Ticket = ""
	if handle.f != nil {
		event.Ticket = handle.f.Ticket
	}
	event.DryRun = false
	event.Status = dbgaudit.StatusPending
	event.Error = nil
	event.ErrorFingerprint = ""
	event.ErrorBytes = 0
	if outcome != nil {
		event.Status = outcome.Status
		event.Executed = outcome.Executed
		event.AffectedRows = outcome.AffectedRows
		if outcome.ErrorCode != "" {
			event.Error = &dbgaudit.ErrorInfo{Code: outcome.ErrorCode}
		}
	}
	return event
}

func mutationValueMetadata(action string, value any) dbgaudit.MutationMetadata {
	payload, err := json.Marshal(value)
	if err != nil {
		return dbgaudit.MutationMetadata{}
	}
	fingerprint, size := dbgaudit.Fingerprint("payload:"+action, string(payload))
	return dbgaudit.MutationMetadata{
		PayloadFingerprint: fingerprint,
		PayloadBytes:       size,
	}
}

func newMutationID(random io.Reader) (string, error) {
	value := make([]byte, 16)
	if _, err := io.ReadFull(random, value); err != nil {
		return "", apperrors.New(apperrors.CodeLocalIOError, "failed to generate mutation id", nil)
	}
	return hex.EncodeToString(value), nil
}

func mutationAuditRuntimeFor(f *cliFlags) *mutationAuditRuntime {
	if f != nil && f.mutationAudit != nil {
		return f.mutationAudit
	}
	return &productionMutationAuditRuntime
}

func appendMutationAuditRecord(
	f *cliFlags,
	path string,
	record dbgaudit.Event,
) (coreaudit.AppendResult, error) {
	runtime := mutationAuditRuntimeFor(f)
	if runtime.appendRecordWithResult != nil {
		return runtime.appendRecordWithResult(path, record, coreaudit.Options{})
	}
	if runtime.appendRecord == nil {
		return coreaudit.AppendResult{State: coreaudit.AppendCommitNotCommitted},
			apperrors.New(apperrors.CodeLocalIOError, "audit append runtime is unavailable", nil)
	}
	if err := runtime.appendRecord(path, record, coreaudit.Options{}); err != nil {
		return coreaudit.AppendResult{State: coreaudit.AppendCommitNotCommitted}, err
	}
	return coreaudit.AppendResult{State: coreaudit.AppendCommitCommitted}, nil
}

// appendQueuedAuditEvent serializes ordinary and mutation audit records through
// the same per-path queue. The timestamp is assigned only after pending mutation
// outcomes have been replayed and while the append-order lock is held.
func appendQueuedAuditEvent(f *cliFlags, path string, event dbgaudit.Event) error {
	return withMutationAuditPathLock(path, func() error {
		if err := prepareMutationAuditQueue(f, path); err != nil {
			return err
		}
		event.Timestamp = mutationAuditRuntimeFor(f).now().UTC()
		result, err := appendMutationAuditRecord(f, path, dbgaudit.Sanitize(event))
		if err != nil {
			return err
		}
		if result.State != coreaudit.AppendCommitCommitted {
			return apperrors.New(apperrors.CodeLocalIOError, "audit append did not reach a clean committed state", nil)
		}
		return nil
	})
}

// prepareMutationAuditQueue must be called with the audit path lock held.
func prepareMutationAuditQueue(f *cliFlags, path string) error {
	if err := ensureMutationSpoolDirectory(mutationAuditSpoolPath(path)); err != nil {
		return err
	}
	return replayMutationAuditSpool(f, path)
}

func withMutationAuditPathLock(path string, fn func() error) (retErr error) {
	value, _ := mutationAuditPathLocks.LoadOrStore(filepath.Clean(path), &sync.Mutex{})
	processLock, ok := value.(*sync.Mutex)
	if !ok {
		return apperrors.New(apperrors.CodeLocalIOError, "invalid mutation audit path lock state", nil)
	}
	processLock.Lock()
	defer processLock.Unlock()

	lockBase := path + ".mutation-audit"
	if err := verifyPrivateMutationFileIfExists(lockBase + ".lock"); err != nil {
		return err
	}
	lock := lockfile.New(lockBase)
	if err := lock.Acquire(); err != nil {
		return err
	}
	defer func() {
		if err := lock.Release(); err != nil && retErr == nil {
			retErr = apperrors.New(apperrors.CodeLocalIOError, "failed to release mutation audit path lock", nil)
		}
	}()
	return fn()
}

func mutationAuditSpoolPath(auditPath string) string {
	return auditPath + mutationAuditSpoolSuffix
}

func spoolMutationAuditOutcome(f *cliFlags, auditPath string, record dbgaudit.Event) (retErr error) {
	mutationAuditSpoolMu.Lock()
	defer mutationAuditSpoolMu.Unlock()

	spoolPath := mutationAuditSpoolPath(auditPath)
	parent := filepath.Dir(spoolPath)
	if err := verifyMutationSpoolParent(parent); err != nil {
		return err
	}
	if err := verifyPrivateMutationFileIfExists(spoolPath + ".lock"); err != nil {
		return err
	}
	lock := lockfile.New(spoolPath)
	if err := lock.Acquire(); err != nil {
		return err
	}
	defer func() {
		if err := lock.Release(); err != nil && retErr == nil {
			retErr = apperrors.New(apperrors.CodeLocalIOError, "failed to release mutation outcome spool lock", nil)
		}
	}()
	if err := ensureMutationSpoolDirectory(spoolPath); err != nil {
		return err
	}
	if err := verifyMutationSpoolDirectory(spoolPath); err != nil {
		return err
	}
	return writeMutationSpoolRecord(f, spoolPath, record)
}

func writeMutationSpoolRecord(f *cliFlags, spoolPath string, record dbgaudit.Event) error {
	data, err := json.Marshal(record)
	if err != nil || len(data) > maxMutationSpoolRecordBytes {
		return apperrors.New(apperrors.CodeLocalIOError, "failed to encode mutation outcome spool", nil)
	}
	var persisted dbgaudit.Event
	if err := json.Unmarshal(data, &persisted); err != nil || persisted.MutationID != record.MutationID {
		return apperrors.New(apperrors.CodeLocalIOError, "failed to validate mutation outcome spool", nil)
	}
	if err := validateMutationSpoolRecord(persisted); err != nil {
		return err
	}
	sequence, err := nextMutationSpoolSequence(spoolPath, mutationAuditRuntimeFor(f).now().UTC().UnixNano())
	if err != nil {
		return err
	}
	name := fmt.Sprintf("%020d-%s.json", sequence, record.MutationID)
	finalPath := filepath.Join(spoolPath, name)
	tempPath := finalPath + ".tmp"
	file, err := os.OpenFile(tempPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // Path is inside the validated private spool.
	if err != nil {
		return apperrors.New(apperrors.CodeLocalIOError, "failed to create mutation outcome spool", nil)
	}
	complete := false
	defer func() {
		_ = file.Close()
		if !complete {
			_ = os.Remove(tempPath)
		}
	}()
	if err := secureMutationSpoolFile(tempPath); err != nil {
		return err
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		return apperrors.New(apperrors.CodeLocalIOError, "failed to write mutation outcome spool", nil)
	}
	if err := file.Sync(); err != nil {
		return apperrors.New(apperrors.CodeLocalIOError, "failed to sync mutation outcome spool", nil)
	}
	if err := file.Close(); err != nil {
		return apperrors.New(apperrors.CodeLocalIOError, "failed to close mutation outcome spool", nil)
	}
	if err := commitMutationSpoolFile(tempPath, finalPath); err != nil {
		return apperrors.New(apperrors.CodeLocalIOError, "failed to commit mutation outcome spool", nil)
	}
	if err := syncMutationSpoolDirectory(spoolPath); err != nil {
		return err
	}
	complete = true
	return nil
}

func nextMutationSpoolSequence(spoolPath string, now int64) (int64, error) {
	entries, err := os.ReadDir(spoolPath)
	if err != nil {
		return 0, apperrors.New(apperrors.CodeLocalIOError, "failed to list mutation outcome spool", nil)
	}
	sequence := now
	if sequence < 1 {
		sequence = 1
	}
	for _, entry := range entries {
		name := entry.Name()
		pendingName := strings.TrimSuffix(name, mutationAuditUncertainMark)
		if entry.IsDir() || !validMutationSpoolName(pendingName) {
			return 0, apperrors.New(apperrors.CodeLocalIOError, "mutation outcome spool contains an unexpected entry", nil)
		}
		prefix := strings.SplitN(pendingName, "-", 2)[0]
		value, parseErr := strconv.ParseInt(prefix, 10, 64)
		if parseErr != nil {
			return 0, apperrors.New(apperrors.CodeLocalIOError, "invalid mutation outcome spool sequence", nil)
		}
		if value >= sequence {
			if value == int64(^uint64(0)>>1) {
				return 0, apperrors.New(apperrors.CodeLocalIOError, "mutation outcome spool sequence exhausted", nil)
			}
			sequence = value + 1
		}
	}
	return sequence, nil
}

//nolint:gocyclo // Locking, strict validation, ordered replay, removal, and durability stay together.
func replayMutationAuditSpool(f *cliFlags, auditPath string) (retErr error) {
	mutationAuditSpoolMu.Lock()
	defer mutationAuditSpoolMu.Unlock()

	spoolPath := mutationAuditSpoolPath(auditPath)
	if _, err := os.Lstat(spoolPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return apperrors.New(apperrors.CodeLocalIOError, "failed to inspect mutation outcome spool", nil)
	}
	parent := filepath.Dir(spoolPath)
	if err := verifyMutationSpoolParent(parent); err != nil {
		return err
	}
	if err := verifyPrivateMutationFileIfExists(spoolPath + ".lock"); err != nil {
		return err
	}
	lock := lockfile.New(spoolPath)
	if err := lock.Acquire(); err != nil {
		return err
	}
	defer func() {
		if err := lock.Release(); err != nil && retErr == nil {
			retErr = apperrors.New(apperrors.CodeLocalIOError, "failed to release mutation outcome spool lock", nil)
		}
	}()
	if err := verifyMutationSpoolDirectory(spoolPath); err != nil {
		return err
	}
	entries, err := os.ReadDir(spoolPath)
	if err != nil {
		return apperrors.New(apperrors.CodeLocalIOError, "failed to list mutation outcome spool", nil)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasSuffix(name, mutationAuditUncertainMark) {
			pendingName := strings.TrimSuffix(name, mutationAuditUncertainMark)
			if entry.IsDir() || !validMutationSpoolName(pendingName) {
				return apperrors.New(apperrors.CodeLocalIOError, "mutation outcome spool contains an unexpected entry", nil)
			}
			return auditStateIncompleteError(mutationIDFromSpoolName(pendingName), coreaudit.AppendCommitIndeterminate)
		}
		if entry.IsDir() || !validMutationSpoolName(name) {
			return apperrors.New(apperrors.CodeLocalIOError, "mutation outcome spool contains an unexpected entry", nil)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		path := filepath.Join(spoolPath, name)
		record, err := readMutationSpoolRecord(path)
		if err != nil {
			return err
		}
		appendResult, appendErr := appendMutationAuditRecord(f, auditPath, record)
		switch appendResult.State {
		case coreaudit.AppendCommitCommitted:
			if err := removeReplayedMutationSpool(path, spoolPath); err != nil {
				return err
			}
			if appendErr != nil {
				return auditStateIncompleteError(record.MutationID, appendResult.State)
			}
		case coreaudit.AppendCommitCommittedPostCommitError:
			if err := removeReplayedMutationSpool(path, spoolPath); err != nil {
				return err
			}
			return auditStateIncompleteError(record.MutationID, appendResult.State)
		case coreaudit.AppendCommitNotCommitted:
			if appendErr != nil {
				return appendErr
			}
			return apperrors.New(apperrors.CodeLocalIOError, "replayed audit outcome did not commit", nil)
		case coreaudit.AppendCommitIndeterminate:
			if err := markMutationSpoolIndeterminate(path, spoolPath); err != nil {
				return auditIncompleteError(record.MutationID, true)
			}
			return auditStateIncompleteError(record.MutationID, appendResult.State)
		default:
			if err := markMutationSpoolIndeterminate(path, spoolPath); err != nil {
				return auditIncompleteError(record.MutationID, true)
			}
			return auditStateIncompleteError(record.MutationID, appendResult.State)
		}
	}
	return nil
}

func removeReplayedMutationSpool(path, spoolPath string) error {
	if err := os.Remove(path); err != nil {
		return apperrors.New(apperrors.CodeLocalIOError, "failed to remove replayed mutation outcome spool", nil)
	}
	return syncMutationSpoolDirectory(spoolPath)
}

func markMutationSpoolIndeterminate(path, spoolPath string) error {
	if err := os.Rename(path, path+mutationAuditUncertainMark); err != nil {
		return apperrors.New(apperrors.CodeLocalIOError, "failed to quarantine indeterminate mutation outcome spool", nil)
	}
	return syncMutationSpoolDirectory(spoolPath)
}

func mutationIDFromSpoolName(name string) string {
	separator := strings.IndexByte(name, '-')
	if separator < 0 {
		return ""
	}
	return strings.TrimSuffix(name[separator+1:], ".json")
}

func validMutationSpoolName(name string) bool {
	if !strings.HasSuffix(name, ".json") {
		return false
	}
	parts := strings.Split(strings.TrimSuffix(name, ".json"), "-")
	if len(parts) != 2 || len(parts[0]) != 20 || len(parts[1]) != 32 {
		return false
	}
	for _, char := range parts[0] {
		if char < '0' || char > '9' {
			return false
		}
	}
	sequence, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || sequence <= 0 {
		return false
	}
	if parts[1] != strings.ToLower(parts[1]) {
		return false
	}
	_, err = hex.DecodeString(parts[1])
	return err == nil
}

func hasDuplicateJSONKey(data []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(data))
	duplicate, err := scanJSONValueForDuplicateKeys(decoder)
	return err == nil && duplicate
}

func scanJSONValueForDuplicateKeys(decoder *json.Decoder) (bool, error) {
	token, err := decoder.Token()
	if err != nil {
		return false, err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return false, nil
	}
	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return false, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return false, apperrors.New(apperrors.CodeValidationFailed, "JSON object key is not a string", nil)
			}
			if _, exists := seen[key]; exists {
				return true, nil
			}
			seen[key] = struct{}{}
			duplicate, err := scanJSONValueForDuplicateKeys(decoder)
			if err != nil || duplicate {
				return duplicate, err
			}
		}
		_, err := decoder.Token()
		return false, err
	case '[':
		for decoder.More() {
			duplicate, err := scanJSONValueForDuplicateKeys(decoder)
			if err != nil || duplicate {
				return duplicate, err
			}
		}
		_, err := decoder.Token()
		return false, err
	default:
		return false, apperrors.New(apperrors.CodeValidationFailed, "unexpected JSON delimiter", nil)
	}
}

func readMutationSpoolRecord(path string) (dbgaudit.Event, error) { //nolint:gocyclo // Stable-file reads and the complete persisted schema are checked fail-closed.
	var record dbgaudit.Event
	if !validMutationSpoolName(filepath.Base(path)) {
		return record, apperrors.New(apperrors.CodeLocalIOError, "invalid mutation outcome spool filename", nil)
	}
	if err := verifyMutationSpoolFile(path); err != nil {
		return record, err
	}
	before, err := os.Lstat(path)
	if err != nil {
		return record, apperrors.New(apperrors.CodeLocalIOError, "failed to inspect mutation outcome spool file", nil)
	}
	// Force lazy file identity loading on Windows before opening the path.
	if !os.SameFile(before, before) {
		return record, apperrors.New(apperrors.CodeLocalIOError, "failed to identify mutation outcome spool file", nil)
	}
	file, err := os.Open(path) //nolint:gosec // Strict name and private parent were already validated.
	if err != nil {
		return record, apperrors.New(apperrors.CodeLocalIOError, "failed to open mutation outcome spool file", nil)
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return record, apperrors.New(apperrors.CodeLocalIOError, "mutation outcome spool file changed while opening", nil)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxMutationSpoolRecordBytes+1))
	if err != nil || len(data) > maxMutationSpoolRecordBytes {
		return record, apperrors.New(apperrors.CodeLocalIOError, "failed to read mutation outcome spool file", nil)
	}
	if hasDuplicateJSONKey(bytes.TrimSpace(data)) {
		return record, apperrors.New(apperrors.CodeLocalIOError, "mutation outcome spool has duplicate fields", nil)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return record, apperrors.New(apperrors.CodeLocalIOError, "invalid mutation outcome spool record", nil)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return record, apperrors.New(apperrors.CodeLocalIOError, "mutation outcome spool contains trailing data", nil)
	}
	if err := validateMutationSpoolRecord(record); err != nil {
		return dbgaudit.Event{}, err
	}
	nameParts := strings.Split(strings.TrimSuffix(filepath.Base(path), ".json"), "-")
	if len(nameParts) != 2 || record.MutationID != nameParts[1] {
		return dbgaudit.Event{}, apperrors.New(apperrors.CodeLocalIOError, "mutation outcome spool identity does not match its filename", nil)
	}
	return record, nil
}

func validateMutationSpoolRecord(record dbgaudit.Event) error { //nolint:gocyclo // The complete fail-closed record shape is checked in one predicate.
	if record.APIVersion != mutationAuditAPIVersion ||
		record.Kind != mutationAuditKind ||
		!validMutationEventID(record.EventID) ||
		record.Timestamp.IsZero() ||
		!validMutationSpoolText(record.Operator, true, 512) ||
		!validMutationSpoolText(record.Context.Name, false, 512) ||
		!validMutationSpoolText(record.Context.Env, false, 128) ||
		!validMutationSpoolText(record.Role, false, 256) ||
		!validMutationSpoolToken(record.Target.ObjectType, 64) ||
		record.Phase != mutationAuditPhaseOutcome ||
		!knownMutationAuditAction(record.Action) ||
		string(record.EventType) != record.Action ||
		len(record.MutationID) != 32 ||
		record.MutationID != strings.ToLower(record.MutationID) ||
		record.Metadata == nil ||
		record.Outcome == nil ||
		record.Status != record.Outcome.Status ||
		record.Executed != record.Outcome.Executed ||
		!sameOptionalInt64(record.AffectedRows, record.Outcome.AffectedRows) ||
		record.DryRun ||
		record.Ticket != "" ||
		record.Reason != "" ||
		record.Statement != "" ||
		record.FailedStatement != "" ||
		record.SnapshotID != "" ||
		record.Target.Database != "" ||
		record.Target.Object != "" ||
		(record.Error != nil && record.Error.Message != "") {
		return apperrors.New(apperrors.CodeLocalIOError, "invalid mutation outcome spool record", nil)
	}
	if record.ImpactRows != nil && (*record.ImpactRows < 0 || *record.ImpactRows > 1_000_000_000) {
		return apperrors.New(apperrors.CodeLocalIOError, "invalid mutation outcome spool impact rows", nil)
	}
	if _, err := hex.DecodeString(record.MutationID); err != nil {
		return apperrors.New(apperrors.CodeLocalIOError, "invalid mutation outcome spool mutation id", nil)
	}
	if !dbgaudit.ValidFingerprint(record.TicketFingerprint, record.TicketBytes) ||
		!dbgaudit.ValidFingerprint(record.ReasonFingerprint, record.ReasonBytes) ||
		!dbgaudit.ValidFingerprint(record.StatementFingerprint, record.StatementBytes) ||
		!dbgaudit.ValidFingerprint(record.FailedStatementFingerprint, record.FailedStatementBytes) ||
		!dbgaudit.ValidFingerprint(record.SnapshotFingerprint, record.SnapshotBytes) ||
		!dbgaudit.ValidFingerprint(record.Target.Fingerprint, record.Target.Bytes) ||
		!dbgaudit.ValidFingerprint(record.ErrorFingerprint, record.ErrorBytes) ||
		!dbgaudit.ValidMutationMetadata(record.Metadata) ||
		!dbgaudit.ValidMutationOutcome(record.Outcome, record.Metadata.Items) {
		return apperrors.New(apperrors.CodeLocalIOError, "invalid mutation outcome spool metadata", nil)
	}
	if record.Outcome.ErrorCode == "" {
		if record.Error != nil {
			return apperrors.New(apperrors.CodeLocalIOError, "invalid mutation outcome spool error", nil)
		}
	} else if record.Error == nil || record.Error.Code != record.Outcome.ErrorCode ||
		!dbgaudit.ValidErrorCode(record.Error.Code) {
		return apperrors.New(apperrors.CodeLocalIOError, "invalid mutation outcome spool error", nil)
	}
	if record.Risk != "R0" && record.Risk != "R1" && record.Risk != "R2" && record.Risk != "R3" {
		return apperrors.New(apperrors.CodeLocalIOError, "invalid mutation outcome spool risk", nil)
	}
	return nil
}

func knownMutationAuditAction(action string) bool {
	switch action {
	case string(dbgaudit.EventTypeSchemaApply),
		string(dbgaudit.EventTypeSchemaDump),
		string(dbgaudit.EventTypeDataExec),
		string(dbgaudit.EventTypeExport),
		string(dbgaudit.EventTypeImport),
		string(dbgaudit.EventTypeReconcile),
		string(dbgaudit.EventTypeRollback),
		string(dbgaudit.EventTypeAuditPrune),
		string(dbgaudit.EventTypeContextSet),
		string(dbgaudit.EventTypeContextUse),
		string(dbgaudit.EventTypeContextDelete),
		string(dbgaudit.EventTypeContextImport),
		string(dbgaudit.EventTypeRoleAssign),
		string(dbgaudit.EventTypeRoleRevoke),
		string(dbgaudit.EventTypeCredentialMigrate),
		string(dbgaudit.EventTypeInstallSkill):
		return true
	default:
		return false
	}
}

func sameOptionalInt64(first, second *int64) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return *first == *second
}

func validMutationSpoolText(value string, required bool, maxBytes int) bool {
	if (required && value == "") || len(value) > maxBytes {
		return false
	}
	for _, char := range value {
		if char < 0x20 || char == 0x7f {
			return false
		}
	}
	return true
}

func validMutationEventID(value string) bool {
	if !strings.HasPrefix(value, "evt-") {
		return false
	}
	suffix := strings.TrimPrefix(value, "evt-")
	if len(suffix) == 24 {
		for _, char := range suffix {
			if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
				return false
			}
		}
		return true
	}
	if suffix == "" || len(suffix) > 20 {
		return false
	}
	for _, char := range suffix {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func validMutationSpoolToken(value string, maxBytes int) bool {
	if value == "" || len(value) > maxBytes {
		return false
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') &&
			(char < '0' || char > '9') &&
			char != '.' &&
			char != '_' &&
			char != '-' {
			return false
		}
	}
	return true
}

func auditIncompleteError(mutationID string, spoolFailed bool) error {
	message := "mutation outcome audit is incomplete"
	if spoolFailed {
		message = "mutation outcome audit is incomplete and durable spooling failed"
	}
	suggestion := "Resolve audit storage before another mutation; a later mutation replays durable outcomes automatically."
	if mutationID != "" {
		suggestion = fmt.Sprintf(
			"Do not retry blindly. Check mutationId %s, resolve audit storage, then run a mutation to replay durable outcomes.",
			mutationID,
		)
	}
	return apperrors.New(codeAuditIncomplete, message, nil).WithSuggestion(suggestion)
}

func auditStateIncompleteError(mutationID string, state coreaudit.AppendCommitState) error {
	message := fmt.Sprintf("mutation audit append state is %q", state)
	if state == "" {
		message = "mutation audit append returned an unknown commit state"
	}
	suggestion := "Resolve audit integrity or lock cleanup before another mutation; no automatic replay will occur because the record may already exist."
	if mutationID != "" {
		suggestion = fmt.Sprintf(
			"Do not retry blindly. Check mutationId %s and resolve audit integrity or lock cleanup; no automatic replay will occur because the record may already exist.",
			mutationID,
		)
	}
	return apperrors.New(codeAuditIncomplete, message, nil).WithSuggestion(suggestion)
}
