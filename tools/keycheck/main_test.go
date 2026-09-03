package main

import (
	"os"
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

// A chart finding carries rendered offsets, not source offsets. The label must
// make that distinction explicit so the source template never appears to name
// a line that belongs to the combined Helm output.
func TestAProfiledMessageLabelsRenderedLines(t *testing.T) {
	f := finding{
		File: "chart/templates/service.yaml", Line: 11, Key: "type", First: 5,
		Profiles: []string{"rich inline cronjob"},
	}
	s := f.String()
	var failures []string
	if !strings.Contains(s, "rendered line 11") {
		failures = append(failures, "the surviving offset is not labeled as rendered: "+s)
	}
	if !strings.Contains(s, "rendered line 5") {
		failures = append(failures, "the discarded offset is not labeled as rendered: "+s)
	}
	if strings.Contains(s, "service.yaml:11") {
		failures = append(failures, "the rendered offset is formatted as a source line: "+s)
	}
	if len(failures) > 0 {
		t.Fatalf("profiled finding:\n%s", strings.Join(failures, "\n"))
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

// A branch that defaults off still has to be read. The external profile is the
// only one that enables autoscaling, so a default render would miss this
// duplicate.
func TestADuplicateInABranchDisabledByDefaultsIsFound(t *testing.T) {
	root, chart := testChart(t, "autoscaling:\n  enabled: false\n", map[string]string{
		"templates/hpa.yaml": `{{- if .Values.autoscaling.enabled }}
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: k
spec:
  maxReplicas: 3
  maxReplicas: 4
{{- end }}
`,
	})
	result, err := scanChart(root, chart)
	if err != nil {
		t.Fatalf("scanChart: %v", err)
	}
	if len(result.Findings) != 1 {
		t.Fatalf("want 1 finding, got %v", result.Findings)
	}
	hit := result.Findings[0]
	if hit.Key != "maxReplicas" {
		t.Errorf("key = %q, want maxReplicas", hit.Key)
	}
	if strings.Join(hit.Profiles, ",") != "external secret autoscaled inProcess with ingress" {
		t.Errorf("profiles = %v, want only the external autoscaling profile", hit.Profiles)
	}
}

// The chart has twelve authored YAML templates. Reaching each one is the proof
// behind the gate's chart coverage claim, so the expected list is deliberate.
func TestEveryAuthoredChartTemplateIsReached(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	result, err := scanChart(root, "deploy/helm/antifailure-control-plane")
	if err != nil {
		t.Fatalf("scanChart: %v", err)
	}
	want := []string{
		"templates/cronjob-maintenance.yaml",
		"templates/deployment.yaml",
		"templates/hpa.yaml",
		"templates/ingress.yaml",
		"templates/job-bootstrap.yaml",
		"templates/networkpolicy.yaml",
		"templates/pdb.yaml",
		"templates/secret-bootstrap.yaml",
		"templates/secret.yaml",
		"templates/service.yaml",
		"templates/serviceaccount.yaml",
		"templates/tests/test-connection.yaml",
	}
	if strings.Join(result.Templates, "\n") != strings.Join(want, "\n") {
		t.Fatalf("reached templates:\n%s\nwant:\n%s", strings.Join(result.Templates, "\n"), strings.Join(want, "\n"))
	}
}

// Template names prove breadth, and these markers prove the conditional
// bodies rendered with the values each profile is meant to exercise.
func TestChartProfilesReachTheirOptionalBranches(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		profile  helmProfile
		contains []string
		omits    []string
	}{
		{
			profile: chartProfiles[0],
			contains: []string{
				"name: keycheck-inline-profile", "kind: CronJob", "kind: Job",
				"kind: NetworkPolicy", "- from:\n        []", "kind: PodDisruptionBudget",
				"kind: ServiceAccount", "kind: Secret", "imagePullSecrets:",
				"keycheck.service: covered", "keycheck.service-account: covered",
				"keycheck.pod-label: covered", "keycheck.pod-annotation: covered",
				"name: AF_APP_BASE_URL", "name: AF_INSECURE_COOKIES",
				"name: AF_OPERATOR_SETS_PLAN", "name: AF_SIGNIN_ALLOWLIST",
				"name: AF_EVENT_RETENTION_MONTHS", "name: AF_EVENT_ARCHIVE_DIR",
				"mountPath: /archive", "replicas: 2", "topologySpreadConstraints:",
				"nodeSelector:", "tolerations:", "affinity:",
				"image: ghcr.io/antifailure/control-plane:",
			},
			omits: []string{"kind: HorizontalPodAutoscaler", "kind: Ingress", "@sha256:"},
		},
		{
			profile: chartProfiles[1],
			contains: []string{
				"name: keycheck-fullname", "kind: HorizontalPodAutoscaler",
				"kind: Ingress", "kind: Job", "kind: NetworkPolicy",
				"kind: PodDisruptionBudget", "serviceAccountName: keycheck-external",
				"name: keycheck-database", "name: keycheck-github",
				"image: ghcr.io/antifailure/control-plane@sha256:aaaaaaaa",
				"name: AF_MAINTENANCE_DATABASE_URL", "name: AF_SIGNIN_ALLOWLIST\n              value: \"\"",
				"keycheck.ingress: covered", "ingressClassName: keycheck",
				"secretName: keycheck-tls", "namespaceSelector:",
				"keycheck.network: allowed",
			},
			omits: []string{
				"kind: Secret", "kind: ServiceAccount", "kind: CronJob",
				"\n  replicas:", "imagePullSecrets:",
			},
		},
		{
			profile: chartProfiles[2],
			contains: []string{
				"name: antifailure-control-plane", "kind: Secret", "kind: Ingress",
				"replicas: 1", "serviceAccountName: default",
				"image: ghcr.io/antifailure/control-plane:v1.1.1-keycheck",
				"AF_DATABASE_URL: \"keycheck\"",
			},
			omits: []string{
				"AF_MIGRATION_DATABASE_URL", "name: AF_SIGNIN_ALLOWLIST",
				"name: AF_MAINTENANCE_DATABASE_URL", "kind: Job", "kind: CronJob",
				"kind: HorizontalPodAutoscaler", "kind: PodDisruptionBudget",
				"kind: NetworkPolicy", "kind: ServiceAccount", "ingressClassName:",
				"imagePullSecrets:", "topologySpreadConstraints:", "nodeSelector:",
				"tolerations:", "affinity:", "keycheck.ingress:", "secretName:", "@sha256:",
			},
		},
	}

	var failures []string
	for _, test := range tests {
		out, renderErr := renderChartProfile(root, "deploy/helm/antifailure-control-plane", test.profile)
		if renderErr != nil {
			failures = append(failures, test.profile.Name+": "+renderErr.Error())
			continue
		}
		rendered := string(out)
		for _, marker := range test.contains {
			if !strings.Contains(rendered, marker) {
				failures = append(failures, test.profile.Name+": missing "+marker)
			}
		}
		for _, marker := range test.omits {
			if strings.Contains(rendered, marker) {
				failures = append(failures, test.profile.Name+": unexpectedly rendered "+marker)
			}
		}
	}
	if len(failures) > 0 {
		t.Fatalf("profile branch coverage:\n%s", strings.Join(failures, "\n"))
	}
}

// The same authored duplicate renders in all three profiles. It is one defect,
// so rendered line movement must not make the report repeat it three times.
func TestADuplicateAcrossProfilesIsReportedOnce(t *testing.T) {
	root, chart := testChart(t, "ingress:\n  enabled: false\n", map[string]string{
		"templates/service.yaml": `apiVersion: v1
kind: Service
metadata:
  name: k
{{- if .Values.ingress.enabled }}
  labels:
    profile: external
{{- end }}
spec:
  type: ClusterIP
  type: NodePort
`,
	})
	result, err := scanChart(root, chart)
	if err != nil {
		t.Fatalf("scanChart: %v", err)
	}
	if len(result.Findings) != 1 {
		t.Fatalf("want 1 finding, got %v", result.Findings)
	}
	var wantProfiles []string
	for _, profile := range chartProfiles {
		wantProfiles = append(wantProfiles, profile.Name)
	}
	if strings.Join(result.Findings[0].Profiles, "\n") != strings.Join(wantProfiles, "\n") {
		t.Fatalf("profiles = %v, want %v", result.Findings[0].Profiles, wantProfiles)
	}
}

// Every part of the identity prevents two distinct authored defects from
// collapsing into one. Only the second profile for the exact same identity is
// evidence for the existing finding.
func TestFindingIdentityKeepsDistinctDefects(t *testing.T) {
	base := finding{
		File: "chart/templates/service.yaml", Document: "v1 Service k",
		Path: `$["spec"]`, Key: "type", Occurrence: 2,
		Profiles: []string{"rich inline cronjob"},
	}
	found := mergeFindings([]finding{
		base,
		{
			File: base.File, Document: base.Document, Path: base.Path,
			Key: base.Key, Occurrence: base.Occurrence,
			Profiles: []string{"external secret autoscaled inProcess with ingress"},
		},
		{File: "chart/templates/other.yaml", Document: base.Document, Path: base.Path, Key: base.Key, Occurrence: base.Occurrence},
		{File: base.File, Document: "v1 Service other", Path: base.Path, Key: base.Key, Occurrence: base.Occurrence},
		{File: base.File, Document: base.Document, Path: `$["metadata"]`, Key: base.Key, Occurrence: base.Occurrence},
		{File: base.File, Document: base.Document, Path: base.Path, Key: "port", Occurrence: base.Occurrence},
		{File: base.File, Document: base.Document, Path: base.Path, Key: base.Key, Occurrence: 3},
	})
	if len(found) != 6 {
		t.Fatalf("want 6 distinct findings, got %d: %v", len(found), found)
	}
}

// One bad profile cannot hide behind two successful renders. The error names
// the profile that failed and keeps Helm's reason.
func TestASingleProfileRenderFailureIsFatalAndNamed(t *testing.T) {
	root, chart := testChart(t, "autoscaling:\n  enabled: false\n", map[string]string{
		"templates/service.yaml": `{{- if .Values.autoscaling.enabled }}
{{- fail "the autoscaled branch is broken" }}
{{- end }}
apiVersion: v1
kind: Service
metadata:
  name: k
`,
	})
	result, err := scanChart(root, chart)
	if err == nil {
		t.Fatal("want the external profile render failure, got nil")
	}
	if !strings.Contains(err.Error(), `profile "external secret autoscaled inProcess with ingress"`) {
		t.Fatalf("error does not name the failed profile: %v", err)
	}
	if !strings.Contains(err.Error(), "the autoscaled branch is broken") {
		t.Fatalf("error lost Helm's diagnostic: %v", err)
	}
	if strings.Join(result.Templates, ",") != "templates/service.yaml" {
		t.Fatalf("successful profiles were discarded: %v", result.Templates)
	}
}

// Helm accepts duplicate values and keeps the last one. The profiles are part
// of this gate, so they pass through the same duplicate check before Helm can
// silently change what the matrix covers.
func TestADuplicateInProfileValuesStopsBeforeHelm(t *testing.T) {
	root, chart := testChart(t, "", map[string]string{
		"templates/service.yaml": "apiVersion: v1\nkind: Service\nmetadata:\n  name: k\n",
	})
	profile := helmProfile{
		Name:    "duplicate values",
		Release: "keycheck",
		Values:  "config:\n  port: 1\nconfig:\n  port: 2\n",
	}
	called := false
	render := func(_, _ string, _ helmProfile) ([]byte, error) {
		called = true
		separator := strings.Repeat("-", 3)
		return []byte(separator + "\n# Source: keycheck-test/templates/service.yaml\napiVersion: v1\nkind: Service\nmetadata:\n  name: k\n"), nil
	}
	_, err := scanChartProfilesWith(root, chart, []helmProfile{profile}, render)
	var failures []string
	if err == nil {
		failures = append(failures, "duplicate profile values returned no error")
	} else {
		if !strings.Contains(err.Error(), `profile "duplicate values" has invalid values`) {
			failures = append(failures, "error did not name the profile: "+err.Error())
		}
		if !strings.Contains(err.Error(), `"config" was already defined`) {
			failures = append(failures, "error did not name the duplicate: "+err.Error())
		}
	}
	if called {
		failures = append(failures, "Helm was called with ambiguous profile values")
	}
	if len(failures) > 0 {
		t.Fatalf("profile validation:\n%s", strings.Join(failures, "\n"))
	}
}

func testChart(t *testing.T, values string, templates map[string]string) (string, string) {
	t.Helper()
	root := t.TempDir()
	chart := "chart"
	files := map[string]string{
		"Chart.yaml":  "apiVersion: v2\nname: keycheck-test\nversion: 0.1.0\n",
		"values.yaml": values,
	}
	for name, body := range templates {
		files[name] = body
	}
	for name, body := range files {
		path := filepath.Join(root, chart, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root, chart
}
