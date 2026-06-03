package snapshot

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Meta struct {
	ID         string    `json:"id"`
	Timestamp  time.Time `json:"timestamp"`
	Operator   string    `json:"operator,omitempty"`
	Command    string    `json:"command"`
	Ticket     string    `json:"ticket,omitempty"`
	Context    string    `json:"context,omitempty"`
	TableCount int       `json:"tableCount"`
}

type Snapshot struct {
	Meta   Meta              `json:"meta"`
	Tables map[string]string `json:"tables"`
}

func Capture(baseDir string, meta Meta, tables map[string]string) (string, error) {
	if meta.Timestamp.IsZero() {
		meta.Timestamp = time.Now().UTC()
	} else {
		meta.Timestamp = meta.Timestamp.UTC()
	}
	if meta.ID == "" {
		meta.ID = newID(meta.Timestamp)
	}
	meta.TableCount = len(tables)
	snap := Snapshot{Meta: meta, Tables: tables}
	if err := os.MkdirAll(baseDir, 0o700); err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(baseDir, meta.ID+".json"), data, 0o600); err != nil {
		return "", err
	}
	return meta.ID, nil
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

func Load(baseDir, id string) (Snapshot, error) {
	if id == "" || id != filepath.Base(id) || filepath.Ext(id) != "" {
		return Snapshot{}, fmt.Errorf("invalid snapshot id: %s", id)
	}
	data, err := os.ReadFile(filepath.Join(baseDir, id+".json"))
	if err != nil {
		return Snapshot{}, err
	}
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return Snapshot{}, err
	}
	return snap, nil
}

func newID(ts time.Time) string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "snap-" + ts.UTC().Format("20060102T150405Z")
	}
	return "snap-" + ts.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(b[:])
}
