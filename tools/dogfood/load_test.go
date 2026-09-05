package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"valid", `{"Load":{"Sent":4,"Routes":[{"Route":"GET /runs","Sent":4}]}}`, true},
		{"invalid JSON", `{`, false},
		{"omitted", `{}`, false},
		{"unavailable", `{"Load":{"Unavailable":"unsafe routes","Sent":4,"Routes":[{"Route":"GET /runs","Sent":4}]}}`, false},
		{"empty", `{"Load":{"Sent":0}}`, false},
		{"missing routes", `{"Load":{"Sent":4}}`, false},
		{"unsent route", `{"Load":{"Sent":4,"Routes":[{"Route":"GET /runs","Sent":4},{"Route":"GET /audit","Sent":0}]}}`, false},
		{"unnamed route", `{"Load":{"Sent":4,"Routes":[{"Sent":4}]}}`, false},
		{"wrong sum", `{"Load":{"Sent":4,"Routes":[{"Route":"GET /runs","Sent":3}]}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkLoadEvidence([]byte(tc.body)); (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
		})
	}
}

func TestLoadEvidenceControlsDogfoodGreen(t *testing.T) {
	for _, tc := range []struct {
		name                          string
		required, write, valid, green bool
	}{
		{"not required", false, false, false, true},
		{"unreadable", true, false, false, false},
		{"empty experiment", true, true, false, false},
		{"observed requests", true, true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "report.json")
			if tc.write {
				body := `{}`
				if tc.valid {
					body = `{"Load":{"Sent":4,"Routes":[{"Route":"GET /runs","Sent":4}]}}`
				}
				if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			run := &Run{Green: true}
			(&runner{requireLoad: tc.required, reportJSONPath: path}).readLoadEvidence(run)
			if run.Green != tc.green {
				t.Fatalf("green=%v, findings=%v", run.Green, run.Findings)
			}
		})
	}
}
