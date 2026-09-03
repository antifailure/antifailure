package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// write puts one file under a fresh directory and returns the directory.
func write(t *testing.T, name, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The defect this gate was written for, in the shape it actually had: two
// pairs of workflow_dispatch inputs, the first of each pair discarded.
func TestTheWorkedExampleDefectIsFound(t *testing.T) {
	dir := write(t, "github-workflow.yml", `on:
  workflow_dispatch:
    inputs:
      seed:
        description: Makes two runs do the same thing.
      concurrency:
        description: Ceiling on requests in flight.
      run_id:
        description: The control plane's identifier.
      seed:
        description: Replays the same schedule.
      concurrency:
        description: Ceiling on requests in flight, for scenario.
`)
	found, err := scan(dir, "github-workflow.yml")
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(found) != 2 {
		t.Fatalf("want 2 findings, got %d: %v", len(found), found)
	}
	if found[0].Key != "seed" {
		t.Errorf("first finding key = %q, want seed", found[0].Key)
	}
	if found[1].Key != "concurrency" {
		t.Errorf("second finding key = %q, want concurrency", found[1].Key)
	}
	if found[0].First != 4 {
		t.Errorf("first finding points at line %d as the discarded one, want 4", found[0].First)
	}
	if found[0].Line != 10 {
		t.Errorf("first finding reports line %d as the survivor, want 10", found[0].Line)
	}
}

// A file with no duplicate must produce no finding. Without this the gate
// could report every key and still pass the test above.
func TestACleanFileReportsNothing(t *testing.T) {
	dir := write(t, "clean.yml", `on:
  workflow_dispatch:
    inputs:
      seed:
        description: one
      concurrency:
        description: two
`)
	found, err := scan(dir, "clean.yml")
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("want no findings, got %v", found)
	}
}

// A value that happens to equal a sibling key's name is not a duplicate. This
// is what the step-by-two over pairs buys, and a walk over every child would
// fail it.
func TestAValueEqualToAKeyNameIsNotADuplicate(t *testing.T) {
	dir := write(t, "values.yml", `seed: seed
concurrency: seed
`)
	found, err := scan(dir, "values.yml")
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("want no findings, got %v", found)
	}
}

// Nested mappings are walked. A duplicate three levels down is the same defect
// as one at the root and the workflow case is always nested.
func TestADuplicateNestedDeeplyIsFound(t *testing.T) {
	dir := write(t, "nested.yml", `jobs:
  build:
    steps:
      - env:
          A: one
          A: two
`)
	found, err := scan(dir, "nested.yml")
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("want 1 finding, got %v", found)
	}
	if found[0].Key != "A" {
		t.Errorf("key = %q, want A", found[0].Key)
	}
}

// The same key in two documents of one file is not a duplicate. Helm output
// and Kubernetes manifests are full of this and reporting it would make the
// gate unusable on exactly the files that most need it.
func TestSiblingDocumentsDoNotCollide(t *testing.T) {
	dir := write(t, "multi.yml", `kind: Service
---
kind: Deployment
`)
	found, err := scan(dir, "multi.yml")
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("want no findings, got %v", found)
	}
}

// Every duplicate in a file is reported, not just the first. This is the whole
// reason the gate walks the tree instead of decoding into a map, and without
// this assertion the decoding implementation would pass the suite.
func TestEveryDuplicateIsReportedNotJustTheFirst(t *testing.T) {
	dir := write(t, "many.yml", `a: 1
a: 2
b: 3
b: 4
c: 5
c: 6
`)
	found, err := scan(dir, "many.yml")
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(found) != 3 {
		t.Fatalf("want 3 findings, got %d: %v", len(found), found)
	}
}

// collect finds YAML by extension and does not descend into code this
// repository did not write.
func TestCollectSkipsVendoredDirectories(t *testing.T) {
	dir := t.TempDir()
	for _, p := range []string{"a.yml", "sub/b.yaml", "node_modules/c.yml", "x.txt"} {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("k: v\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	files, err := collect(dir, nil)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	got := strings.Join(files, ",")
	want := "a.yml,sub/b.yaml"
	if got != want {
		t.Fatalf("collect = %q, want %q", got, want)
	}
}

// A file this cannot parse must be an error rather than a clean result,
// because "no duplicates" about a file nothing read is the silence the gate
// exists to remove.
func TestAnUnparseableFileIsAnErrorNotACleanPass(t *testing.T) {
	dir := write(t, "broken.yml", "a: [1, 2\nb: :\n")
	found, err := scan(dir, "broken.yml")
	if err == nil {
		t.Fatalf("want a parse error, got %d findings and no error", len(found))
	}
}

// The message names the surviving line and the discarded one, because a
// finding a reader cannot act on is a finding they will silence.
func TestTheMessageNamesBothLines(t *testing.T) {
	f := finding{File: "x.yml", Line: 11, Key: "seed", First: 5}
	s := f.String()
	if !strings.Contains(s, "x.yml:11") {
		t.Errorf("message does not name the surviving line: %s", s)
	}
	if !strings.Contains(s, "line 5") {
		t.Errorf("message does not name the discarded line: %s", s)
	}
	if !strings.Contains(s, `"seed"`) {
		t.Errorf("message does not name the key: %s", s)
	}
}

// A chart's templates are not read as YAML. They are Go template source, which
// no YAML parser can read, so collect must leave them to the render path
// rather than reporting twelve files it could not check.
func TestCollectLeavesChartTemplatesToTheRenderPath(t *testing.T) {
	dir := t.TempDir()
	for _, p := range []string{
		"deploy/chart/Chart.yaml",
		"deploy/chart/values.yaml",
		"deploy/chart/templates/service.yaml",
		"other.yml",
	} {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("k: v\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	charts, err := findCharts(dir)
	if err != nil {
		t.Fatalf("findCharts: %v", err)
	}
	if strings.Join(charts, ",") != "deploy/chart" {
		t.Fatalf("findCharts = %v, want [deploy/chart]", charts)
	}
	files, err := collect(dir, charts)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	got := strings.Join(files, ",")
	want := "deploy/chart/Chart.yaml,deploy/chart/values.yaml,other.yml"
	if got != want {
		t.Fatalf("collect = %q, want %q", got, want)
	}
}

// A duplicate key inside a Helm template is found through the render. This is
// the case helm lint returns clean on, so without it the twelve chart
// templates are covered by nothing at all.
func TestADuplicateInsideAChartTemplateIsFound(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm is not installed")
	}
	dir := t.TempDir()
	chart := filepath.Join(dir, "chart")
	if err := os.MkdirAll(filepath.Join(chart, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"Chart.yaml":  "apiVersion: v2\nname: k\nversion: 0.1.0\n",
		"values.yaml": "kind: Service\n",
		"templates/service.yaml": `apiVersion: v1
kind: {{ .Values.kind }}
metadata:
  name: k
spec:
  type: ClusterIP
  type: NodePort
`,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(chart, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	found, err := scanChart(dir, "chart")
	if err != nil {
		t.Fatalf("scanChart: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("want 1 finding, got %v", found)
	}
	if found[0].Key != "type" {
		t.Errorf("key = %q, want type", found[0].Key)
	}
}
