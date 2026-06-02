package cmd

import (
	"encoding/json"
	"os"
	"testing"
)

func assertGoldenJSON(t *testing.T, path string, actual []byte) {
	t.Helper()
	expected, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	var expectedJSON any
	if err := json.Unmarshal(expected, &expectedJSON); err != nil {
		t.Fatalf("golden is not JSON: %v", err)
	}
	var actualJSON any
	if err := json.Unmarshal(actual, &actualJSON); err != nil {
		t.Fatalf("actual is not JSON: %v\n%s", err, string(actual))
	}
	expectedCanonical, _ := json.MarshalIndent(expectedJSON, "", "  ")
	actualCanonical, _ := json.MarshalIndent(actualJSON, "", "  ")
	if string(actualCanonical) != string(expectedCanonical) {
		t.Fatalf("golden mismatch\nwant:\n%s\n\ngot:\n%s", expectedCanonical, actualCanonical)
	}
}
