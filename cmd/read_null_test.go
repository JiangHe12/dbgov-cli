package cmd

import (
	"bytes"
	"strings"
	"testing"

	dbbackend "github.com/JiangHe12/dbgov-cli/internal/backend"
)

func TestQueryOutputDistinguishesSQLNullFromEmptyString(t *testing.T) {
	result := dbbackend.QueryResult{
		Columns: []string{"nullable", "empty"},
		Rows:    [][]string{{"", ""}},
		Nulls:   [][]bool{{true, false}},
	}
	meta := contextMeta{Name: "test", Engine: "mysql", Host: "localhost", Database: "app"}

	var jsonOut bytes.Buffer
	if err := printQueryResult(&cliFlags{Output: "json", Out: &jsonOut, Err: &bytes.Buffer{}}, meta, result); err != nil {
		t.Fatalf("printQueryResult(json) error = %v", err)
	}
	compact := strings.Join(strings.Fields(jsonOut.String()), "")
	if !strings.Contains(compact, `"rows":[[null,""]]`) {
		t.Fatalf("JSON output = %s", jsonOut.String())
	}

	var tableOut bytes.Buffer
	if err := printQueryResult(&cliFlags{Output: "table", Out: &tableOut, Err: &bytes.Buffer{}}, meta, result); err != nil {
		t.Fatalf("printQueryResult(table) error = %v", err)
	}
	if !strings.Contains(tableOut.String(), "NULL") {
		t.Fatalf("table output does not label SQL NULL: %s", tableOut.String())
	}
}
