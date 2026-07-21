package backend

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestQueryResultPreservesSQLNullInJSONAndTableRows(t *testing.T) {
	result := QueryResult{
		Columns: []string{"nullable", "empty"},
		Rows:    [][]string{{"", ""}},
		Nulls:   [][]bool{{true, false}},
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if got := string(data); got != `{"columns":["nullable","empty"],"rows":[[null,""]]}` {
		t.Fatalf("Marshal() = %s", got)
	}
	display := result.DisplayRows()
	if display[0][0] != "NULL" || display[0][1] != "" {
		t.Fatalf("DisplayRows() = %#v", display)
	}
	if result.Rows[0][0] != "" {
		t.Fatalf("DisplayRows() mutated source rows: %#v", result.Rows)
	}
}

func TestPlanFingerprintDistinguishesSQLNullFromEmptyString(t *testing.T) {
	nullResult := QueryResult{Columns: []string{"value"}, Rows: [][]string{{""}}, Nulls: [][]bool{{true}}}
	emptyResult := QueryResult{Columns: []string{"value"}, Rows: [][]string{{""}}}
	if PlanFingerprint(nullResult) == PlanFingerprint(emptyResult) {
		t.Fatal("PlanFingerprint() conflated SQL NULL with an empty string")
	}
}

func TestExplainResultPreservesSQLNullInJSON(t *testing.T) {
	result := ExplainResult{
		Columns:         []string{"detail"},
		Rows:            [][]string{{""}},
		Nulls:           [][]bool{{true}},
		EstimatedRows:   1,
		PlanFingerprint: "sha256:test",
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(data), `"rows":[[null]]`) {
		t.Fatalf("Marshal() = %s", data)
	}
}
