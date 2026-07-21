package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteSchemaDumpRejectsTraversalBeforeMutation(t *testing.T) {
	target := filepath.Join(t.TempDir(), "schema")
	source := schemaDumpResult{Tables: []schemaDumpTable{
		{Name: filepath.Join("..", "config"), DDL: "CREATE TABLE x (id INT);"},
	}}
	result, attempted, err := writeSchemaDump(target, source)
	if err == nil {
		t.Fatal("writeSchemaDump() error = nil, want traversal rejection")
	}
	if attempted != 0 || len(result.Files) != 0 {
		t.Fatalf("writeSchemaDump() progress = attempted %d, files %v; want no mutation", attempted, result.Files)
	}
	if _, statErr := os.Lstat(target); !os.IsNotExist(statErr) {
		t.Fatalf("traversal rejection created target directory: %v", statErr)
	}
}

func TestWriteSchemaDumpRejectsAbsoluteTableNameBeforeMutation(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "schema")
	outside := filepath.Join(root, "outside")
	source := schemaDumpResult{Tables: []schemaDumpTable{
		{Name: outside, DDL: "CREATE TABLE x (id INT);"},
	}}
	result, attempted, err := writeSchemaDump(target, source)
	if err == nil {
		t.Fatal("writeSchemaDump() error = nil, want absolute-name rejection")
	}
	if attempted != 0 || len(result.Files) != 0 {
		t.Fatalf("writeSchemaDump() progress = attempted %d, files %v; want no mutation", attempted, result.Files)
	}
	if _, statErr := os.Lstat(target); !os.IsNotExist(statErr) {
		t.Fatalf("absolute-name rejection created target directory: %v", statErr)
	}
	if _, statErr := os.Lstat(outside + ".sql"); !os.IsNotExist(statErr) {
		t.Fatalf("absolute-name rejection wrote outside root: %v", statErr)
	}
}

func TestWriteSchemaDumpRejectsDuplicateDestinationBeforeMutation(t *testing.T) {
	target := filepath.Join(t.TempDir(), "schema")
	source := schemaDumpResult{Tables: []schemaDumpTable{
		{Name: "users", DDL: "CREATE TABLE users (id INT);"},
		{Name: "users", DDL: "CREATE TABLE users (id BIGINT);"},
	}}
	result, attempted, err := writeSchemaDump(target, source)
	if err == nil {
		t.Fatal("writeSchemaDump() error = nil, want collision rejection")
	}
	if attempted != 0 || len(result.Files) != 0 {
		t.Fatalf("writeSchemaDump() progress = attempted %d, files %v; want no mutation", attempted, result.Files)
	}
}

func TestWriteSchemaDumpRejectsSymlinkRoot(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(root, "outside")
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "schema-link")
	if err := os.Symlink(outside, target); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	source := schemaDumpResult{Tables: []schemaDumpTable{
		{Name: "users", DDL: "CREATE TABLE users (id INT);"},
	}}
	result, attempted, err := writeSchemaDump(target, source)
	if err == nil {
		t.Fatal("writeSchemaDump() error = nil, want symlink-root rejection")
	}
	if attempted != 0 || len(result.Files) != 0 {
		t.Fatalf("writeSchemaDump() progress = attempted %d, files %v; want no mutation", attempted, result.Files)
	}
	if _, statErr := os.Lstat(filepath.Join(outside, "users.sql")); !os.IsNotExist(statErr) {
		t.Fatalf("symlink-root rejection wrote outside root: %v", statErr)
	}
}

func TestWritePrivateMutationFileRejectsSymlinkTarget(t *testing.T) {
	root := t.TempDir()
	secureMutationAuditTestParent(t, root)
	sentinel := filepath.Join(root, "sentinel")
	if err := os.WriteFile(sentinel, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "users.sql")
	if err := os.Symlink(sentinel, target); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := writePrivateMutationFile(root, "users.sql", []byte("changed")); err == nil {
		t.Fatal("writePrivateMutationFile() error = nil, want symlink rejection")
	}
	data, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "unchanged" {
		t.Fatalf("symlink target changed: %q", data)
	}
}
