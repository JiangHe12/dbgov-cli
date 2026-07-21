package snapshot

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/JiangHe12/opskit-core/v2/apperrors"
)

type Meta struct {
	ID         string    `json:"id"`
	Timestamp  time.Time `json:"timestamp"`
	Operator   string    `json:"operator,omitempty"`
	Command    string    `json:"command"`
	Ticket     string    `json:"ticket,omitempty"`
	Context    string    `json:"context,omitempty"`
	Target     *Target   `json:"target,omitempty"`
	TableCount int       `json:"tableCount"`
}

// Target binds a snapshot to the physical database and schema it describes.
// Context is also retained so a snapshot cannot silently cross governance
// boundaries even when two contexts currently point at the same database.
type Target struct {
	Context  string `json:"context"`
	Engine   string `json:"engine"`
	Host     string `json:"host"`
	Port     int    `json:"port,omitempty"`
	Database string `json:"database"`
	Schema   string `json:"schema,omitempty"`
}

type Snapshot struct {
	Meta   Meta              `json:"meta"`
	Tables map[string]string `json:"tables"`
}

const maxSnapshotBytes int64 = 1024 * 1024 * 1024

// Prepare validates and serializes a snapshot for a caller-provided durable writer.
func Prepare(meta Meta, tables map[string]string) (string, []byte, error) {
	if meta.Timestamp.IsZero() {
		meta.Timestamp = time.Now().UTC()
	} else {
		meta.Timestamp = meta.Timestamp.UTC()
	}
	if meta.ID == "" {
		meta.ID = newID(meta.Timestamp)
	}
	if err := validateID(meta.ID); err != nil {
		return "", nil, err
	}
	if meta.Target != nil {
		if err := validateTarget(*meta.Target); err != nil {
			return "", nil, err
		}
		if strings.TrimSpace(meta.Context) != strings.TrimSpace(meta.Target.Context) {
			return "", nil, apperrors.New(apperrors.CodeValidationFailed, "snapshot context does not match its target binding", nil)
		}
	}
	meta.TableCount = len(tables)
	snap := Snapshot{Meta: meta, Tables: tables}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return "", nil, err
	}
	return meta.ID, data, nil
}

func List(baseDir string) ([]Meta, error) {
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []Meta{}, nil
		}
		return nil, err
	}
	metas := []Meta{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			continue
		}
		snap, err := Load(baseDir, strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())))
		if err != nil {
			return nil, err
		}
		metas = append(metas, snap.Meta)
	}
	sort.Slice(metas, func(i, j int) bool {
		if metas[i].Timestamp.Equal(metas[j].Timestamp) {
			return metas[i].ID > metas[j].ID
		}
		return metas[i].Timestamp.After(metas[j].Timestamp)
	})
	return metas, nil
}

func Load(baseDir, id string) (Snapshot, error) { //nolint:gocyclo // Stable-file identity and complete snapshot-binding validation stay in one fail-closed read boundary.
	if err := validateID(id); err != nil {
		return Snapshot{}, err
	}
	path := filepath.Join(baseDir, id+".json")
	before, err := os.Lstat(path)
	if err != nil {
		return Snapshot{}, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() || !os.SameFile(before, before) {
		return Snapshot{}, apperrors.New(apperrors.CodeLocalIOError, "snapshot must be a stable regular file", nil)
	}
	file, err := os.Open(path) //nolint:gosec // id is validated as a base filename before joining with the snapshot directory.
	if err != nil {
		return Snapshot{}, err
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return Snapshot{}, apperrors.New(apperrors.CodeLocalIOError, "snapshot changed while opening", nil)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxSnapshotBytes+1))
	if err != nil || int64(len(data)) > maxSnapshotBytes {
		return Snapshot{}, apperrors.New(apperrors.CodeLocalIOError, "failed to read bounded snapshot", err)
	}
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return Snapshot{}, err
	}
	if snap.Meta.ID != id || snap.Meta.TableCount != len(snap.Tables) {
		return Snapshot{}, apperrors.New(apperrors.CodeValidationFailed, "snapshot metadata does not match its contents", nil)
	}
	if snap.Meta.Target != nil {
		if err := validateTarget(*snap.Meta.Target); err != nil {
			return Snapshot{}, err
		}
		if strings.TrimSpace(snap.Meta.Context) != strings.TrimSpace(snap.Meta.Target.Context) {
			return Snapshot{}, apperrors.New(apperrors.CodeValidationFailed, "snapshot context does not match its target binding", nil)
		}
	}
	return snap, nil
}

func validateTarget(target Target) error {
	engine := strings.ToLower(strings.TrimSpace(target.Engine))
	valid := strings.TrimSpace(target.Context) != "" &&
		strings.TrimSpace(target.Host) != "" &&
		strings.TrimSpace(target.Database) != "" &&
		target.Port >= 0 &&
		target.Port <= 65535 &&
		(engine == "mysql" || engine == "postgres")
	if engine == "mysql" && target.Schema != "" {
		valid = false
	}
	if engine == "postgres" && strings.TrimSpace(target.Schema) == "" {
		valid = false
	}
	if !valid {
		return apperrors.New(apperrors.CodeValidationFailed, "invalid snapshot target binding", nil)
	}
	return nil
}

func validateID(id string) error {
	const (
		prefixLength = len("snap-")
		stampLength  = len("20060102T150405Z")
		baseLength   = prefixLength + stampLength
	)
	valid := strings.HasPrefix(id, "snap-") && (len(id) == baseLength || len(id) == baseLength+1+8)
	if valid {
		stamp := id[prefixLength:baseLength]
		parsed, err := time.Parse("20060102T150405Z", stamp)
		valid = err == nil && parsed.UTC().Format("20060102T150405Z") == stamp
	}
	if valid && len(id) > baseLength {
		suffix := id[baseLength+1:]
		decoded, err := hex.DecodeString(suffix)
		valid = id[baseLength] == '-' && suffix == strings.ToLower(suffix) && err == nil && len(decoded) == 4
	}
	if !valid {
		return apperrors.New(apperrors.CodeValidationFailed, "invalid snapshot id: "+id, nil)
	}
	return nil
}

func newID(ts time.Time) string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "snap-" + ts.UTC().Format("20060102T150405Z")
	}
	return "snap-" + ts.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(b[:])
}
