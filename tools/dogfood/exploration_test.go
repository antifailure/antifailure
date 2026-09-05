package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDogfoodRequiresConfiguredExploration(t *testing.T) {
	for name, result := range map[string]string{
		"complete":    `{"Exploration":{"Results":[{"name":"goal","outcome":{"verdict":"pass"},"visited":["/"],"evidence":{"trace":"trace.zip"}}]}}`,
		"absent":      `{}`,
		"unreadable":  `not-json`,
		"unavailable": `{"Exploration":{"Unavailable":"browser failed"}}`,
		"empty":       `{"Exploration":{"Results":[]}}`,
		"blocked":     `{"Exploration":{"Results":[{"name":"goal","outcome":{"verdict":"blocked"},"visited":["/"],"evidence":{"trace":"trace.zip"}}]}}`,
		"no-page":     `{"Exploration":{"Results":[{"name":"goal","outcome":{"verdict":"pass"},"visited":[],"evidence":{"trace":"trace.zip"}}]}}`,
		"no-trace":    `{"Exploration":{"Results":[{"name":"goal","outcome":{"verdict":"pass"},"visited":["/"],"evidence":{}}]}}`,
		// Each of these three reaches exactly one refusal. The goal ran and
		// left complete evidence, so nothing else in the gate can say no.
		"unavailable-after-a-complete-goal": `{"Exploration":{"Unavailable":"the browser died during teardown","Results":[{"name":"goal","outcome":{"verdict":"pass"},"visited":["/"],"evidence":{"trace":"trace.zip"}}]}}`,
		"another-goal-entirely":             `{"Exploration":{"Results":[{"name":"other","outcome":{"verdict":"pass"},"visited":["/"],"evidence":{"trace":"trace.zip"}}]}}`,
		"one-goal-too-many":                 `{"Exploration":{"Results":[{"name":"goal","outcome":{"verdict":"pass"},"visited":["/"],"evidence":{"trace":"trace.zip"}},{"name":"unasked","outcome":{"verdict":"pass"},"visited":["/"],"evidence":{"trace":"trace.zip"}}]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "antifailure.yaml"), []byte("explore:\n  enabled: true\n  goals:\n    - name: goal\n"), 0600); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "report.json")
			if err := os.WriteFile(path, []byte(result), 0600); err != nil {
				t.Fatal(err)
			}
			run := &Run{Green: true}
			(&runner{root: root, reportJSONPath: path}).readExplorationEvidence(run)
			if run.Green != (name == "complete") {
				t.Fatalf("green=%v, findings=%v", run.Green, run.Findings)
			}
		})
	}
}

func TestDogfoodCannotSkipUnreadableExplorationConfiguration(t *testing.T) {
	for _, name := range []string{"missing-manifest", "malformed-manifest", "missing-report", "disabled"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			body := "explore:\n  enabled: true\n  goals:\n    - name: goal\n"
			if name == "malformed-manifest" {
				body = "explore: ["
			}
			if name == "disabled" {
				body = "explore:\n  enabled: false\n"
			}
			if name != "missing-manifest" {
				if err := os.WriteFile(filepath.Join(root, "antifailure.yaml"), []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			run := &Run{Green: true}
			(&runner{root: root, reportJSONPath: filepath.Join(root, "absent.json")}).readExplorationEvidence(run)
			if run.Green != (name == "disabled") {
				t.Fatalf("green=%v, findings=%v", run.Green, run.Findings)
			}
		})
	}
}
