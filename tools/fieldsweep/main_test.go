package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEveryManifestFieldIsHonoredOrRefused is the gate. It runs against this
// repository, not a fixture, which is the only version of this check worth
// having: a field added to the manifest schema with no reader and no refusal
// fails here rather than reaching somebody's antifailure.yaml and being
// discarded in silence.
//
// If this is red on your branch, you added a manifest field that nothing
// consumes. Give it a reader, or refuse it in
// engine/internal/manifest/validate.go and write a row in
// tools/docs/manifest-field-exemptions.tsv saying why. Both are cheap; the
// third option, leaving it, is what produced replicas.
func TestEveryManifestFieldIsHonoredOrRefused(t *testing.T) {
	var out strings.Builder
	code, err := run("../..", false, &out)
	if err != nil {
		t.Fatalf("the sweep could not run: %v", err)
	}
	if code != 0 {
		t.Fatalf("the sweep found a manifest field that is neither honored nor refused:\n%s", out.String())
	}
	t.Log("\n" + out.String())
}

// fixture writes the smallest tree the sweep can read: a schema with one
// struct, a validate.go, and one consumer.
type fixture struct {
	schema     string
	validate   string
	consumer   string
	exemptions string
	// contract is schemas/manifest.v1.json. Empty means the smallest document
	// that parses, which is what a fixture with no label row needs.
	contract string
}

func write(t *testing.T, f fixture) string {
	t.Helper()
	root := t.TempDir()
	contract := f.contract
	if contract == "" {
		contract = `{"$defs":{}}`
	}
	files := map[string]string{
		"schemas/manifest.v1.json":                 contract,
		"engine/pkg/schema/manifest.go":            "package schema\n\n" + f.schema,
		"engine/internal/manifest/validate.go":     "package manifest\n\n" + f.validate,
		"engine/internal/runtime/local/runtime.go": consumerPackage + f.consumer,
		"tools/docs/manifest-field-exemptions.tsv": f.exemptions,
	}
	for rel, body := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// consumerPackage is a package that names the schema, which is what makes its
// selectors count. A package with no such import cannot be reading a manifest
// field, because reaching one means naming a schema type.
const consumerPackage = "package local\n\nimport _ \"github.com/antifailure/antifailure/engine/pkg/schema\"\n\n"

const twoFields = `type Service struct {
	Port     int
	Replicas int
}
`

// sweep runs the tool over a fixture and returns its exit code and output.
func sweep(t *testing.T, f fixture) (int, string) {
	t.Helper()
	var out strings.Builder
	code, err := run(write(t, f), false, &out)
	if err != nil {
		t.Fatalf("the sweep could not run: %v", err)
	}
	return code, out.String()
}

// The positive control. Without it a check that refuses everything would pass
// every test below and be worthless.
func TestAFieldSomethingReadsIsHonored(t *testing.T) {
	code, out := sweep(t, fixture{
		schema:   "type Service struct {\n\tPort int\n}\n",
		consumer: "func use(s any) { _ = s.(interface{ x() }) }\nvar _ = func(p struct{ Port int }) int { return p.Port }\n",
	})
	if code != 0 {
		t.Fatalf("a field with a reader was reported unread:\n%s", out)
	}
	if !strings.Contains(out, "1 of 1 manifest fields are accounted for.") {
		t.Fatalf("the count is wrong:\n%s", out)
	}
}

// The case that produced the tool. A field in the schema, nothing reading it,
// nothing refusing it.
func TestAFieldNothingReadsAndNothingRefusesIsReported(t *testing.T) {
	code, out := sweep(t, fixture{
		schema:   twoFields,
		consumer: "var _ = func(p struct{ Port int }) int { return p.Port }\n",
	})
	if code == 0 {
		t.Fatalf("a field nothing reads passed the sweep:\n%s", out)
	}
	if !strings.Contains(out, "Service.Replicas is declared and nothing reads it.") {
		t.Fatalf("the report does not name the field:\n%s", out)
	}
	if !strings.Contains(out, "1 of 2 manifest fields are accounted for.") {
		t.Fatalf("the count is wrong:\n%s", out)
	}
}

// A row with a refusal behind it is the accepted answer.
func TestAFieldWithARefusalAndARowIsAccepted(t *testing.T) {
	code, out := sweep(t, fixture{
		schema:     twoFields,
		validate:   "func refuse() { add(base + \".replicas\") }\n",
		consumer:   "var _ = func(p struct{ Port int }) int { return p.Port }\n",
		exemptions: "Service.Replicas\trefused\t.replicas\tNothing reads it and both runtimes start one container.\n",
	})
	if code != 0 {
		t.Fatalf("a refused field with a row was reported:\n%s", out)
	}
	if !strings.Contains(out, "2 of 2 manifest fields are accounted for.") {
		t.Fatalf("the count is wrong:\n%s", out)
	}
	if !strings.Contains(out, "refused  1") {
		t.Fatalf("the refusal is not counted separately:\n%s", out)
	}
}

// The row is a claim about validate.go, and the claim is checked. Deleting the
// refusal and leaving the row is exactly how an exemption file rots into a
// list of things somebody once said.
func TestARowWhoseRefusalIsGoneIsReported(t *testing.T) {
	code, out := sweep(t, fixture{
		schema:     twoFields,
		validate:   "func refuse() { add(base + \".schedule\") }\n",
		consumer:   "var _ = func(p struct{ Port int }) int { return p.Port }\n",
		exemptions: "Service.Replicas\trefused\t.replicas\tNothing reads it.\n",
	})
	if code == 0 {
		t.Fatalf("a row whose refusal is gone passed the sweep:\n%s", out)
	}
	if !strings.Contains(out, "does not mention that path") {
		t.Fatalf("the report does not say the refusal is missing:\n%s", out)
	}
}

// The other direction. A row for a field something now reads is a decision
// that has outlived its reason.
func TestAStaleRowForAFieldSomethingReadsIsReported(t *testing.T) {
	code, out := sweep(t, fixture{
		schema:     twoFields,
		validate:   "func refuse() { add(base + \".replicas\") }\n",
		consumer:   "var _ = func(p struct{ Replicas int }) int { return p.Replicas }\n",
		exemptions: "Service.Replicas\trefused\t.replicas\tNothing reads it.\n",
	})
	if code == 0 {
		t.Fatalf("a stale row passed the sweep:\n%s", out)
	}
	if !strings.Contains(out, "something reads it now, so delete the row") {
		t.Fatalf("the report does not say the row is stale:\n%s", out)
	}
}

func TestARowForAFieldThatNoLongerExistsIsReported(t *testing.T) {
	code, out := sweep(t, fixture{
		schema:     "type Service struct {\n\tPort int\n}\n",
		validate:   "func refuse() { add(base + \".replicas\") }\n",
		consumer:   "var _ = func(p struct{ Port int }) int { return p.Port }\n",
		exemptions: "Service.Replicas\trefused\t.replicas\tNothing reads it.\n",
	})
	if code == 0 {
		t.Fatalf("a row for a field that is gone passed the sweep:\n%s", out)
	}
	if !strings.Contains(out, "no longer exists") {
		t.Fatalf("the report does not say the field is gone:\n%s", out)
	}
}

// The declaration, the normalizer and the validator are not readers. Counting
// the normalizer as one would have reported replicas as wired, because
// normalize.go assigned it a default on every manifest and consulted it never.
func TestTheNormalizerIsNotAReader(t *testing.T) {
	root := write(t, fixture{
		schema:   twoFields,
		consumer: "var _ = func(p struct{ Port int }) int { return p.Port }\n",
	})
	path := filepath.Join(root, "engine", "internal", "manifest", "normalize.go")
	body := "package manifest\n\nimport _ \"github.com/antifailure/antifailure/engine/pkg/schema\"\n\n" +
		"var _ = func(p struct{ Replicas int }) int { return p.Replicas }\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	code, err := run(root, false, &out)
	if err != nil {
		t.Fatal(err)
	}
	if code == 0 {
		t.Fatalf("the normalizer was counted as a reader:\n%s", out.String())
	}
}

// A test file is not a reader either. A field read only by the test that
// checks it parses is a field nothing in the product consumes.
func TestATestFileIsNotAReader(t *testing.T) {
	root := write(t, fixture{
		schema:   twoFields,
		consumer: "var _ = func(p struct{ Port int }) int { return p.Port }\n",
	})
	path := filepath.Join(root, "engine", "internal", "runtime", "local", "runtime_test.go")
	body := consumerPackage + "var _ = func(p struct{ Replicas int }) int { return p.Replicas }\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	code, err := run(root, false, &out)
	if err != nil {
		t.Fatal(err)
	}
	if code == 0 {
		t.Fatalf("a test file was counted as a reader:\n%s", out.String())
	}
}

// A package that never names the schema cannot be reading a manifest field,
// and counting its selectors is what made every field called Name or Path
// report as wired the moment anything anywhere read the other one.
func TestAPackageThatDoesNotImportTheSchemaIsNotAReader(t *testing.T) {
	root := write(t, fixture{
		schema:   twoFields,
		consumer: "var _ = func(p struct{ Port int }) int { return p.Port }\n",
	})
	path := filepath.Join(root, "engine", "internal", "unrelated", "thing.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "package unrelated\n\nvar _ = func(p struct{ Replicas int }) int { return p.Replicas }\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	code, err := run(root, false, &out)
	if err != nil {
		t.Fatal(err)
	}
	if code == 0 {
		t.Fatalf("a package with no schema import was counted as a reader:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "Service.Replicas is declared and nothing reads it.") {
		t.Fatalf("the report does not name the field:\n%s", out.String())
	}
}

// A label is the third answer, and it exists because refusing one was wrong.
// workflows[].tags was refused here first and eleven deliberate groupings in
// the examples and in this repository's own manifest stopped parsing. What was
// missing was never a reader; it was a description.
func TestALabelWithADescriptionInTheSchemaIsAccepted(t *testing.T) {
	code, out := sweep(t, fixture{
		schema:     twoFields,
		consumer:   "var _ = func(p struct{ Port int }) int { return p.Port }\n",
		exemptions: "Service.Replicas\tlabel\tservice.replicas\tA grouping for whoever reads the manifest.\n",
		contract:   `{"$defs":{"service":{"properties":{"replicas":{"description":"A label the engine does not read."}}}}}`,
	})
	if code != 0 {
		t.Fatalf("a described label was reported:\n%s", out)
	}
	if !strings.Contains(out, "label    1") {
		t.Fatalf("the label is not counted separately:\n%s", out)
	}
}

// The rule that makes a label mean something. An undocumented label and a
// field somebody forgot to wire look exactly alike from the outside, so a row
// claiming the first has to point at a description that says so.
func TestALabelWithNoDescriptionInTheSchemaIsReported(t *testing.T) {
	code, out := sweep(t, fixture{
		schema:     twoFields,
		consumer:   "var _ = func(p struct{ Port int }) int { return p.Port }\n",
		exemptions: "Service.Replicas\tlabel\tservice.replicas\tA grouping for whoever reads the manifest.\n",
		contract:   `{"$defs":{"service":{"properties":{"replicas":{"type":"integer"}}}}}`,
	})
	if code == 0 {
		t.Fatalf("an undocumented label passed the sweep:\n%s", out)
	}
	if !strings.Contains(out, "carries no description") {
		t.Fatalf("the report does not say the description is missing:\n%s", out)
	}
}

func TestALabelPointingAtNothingInTheSchemaIsReported(t *testing.T) {
	code, out := sweep(t, fixture{
		schema:     twoFields,
		consumer:   "var _ = func(p struct{ Port int }) int { return p.Port }\n",
		exemptions: "Service.Replicas\tlabel\tservice.replicas\tA grouping for whoever reads the manifest.\n",
		contract:   `{"$defs":{"service":{"properties":{}}}}`,
	})
	if code == 0 {
		t.Fatalf("a label pointing at nothing passed the sweep:\n%s", out)
	}
	if !strings.Contains(out, "has no $defs.service.properties.replicas") {
		t.Fatalf("the report does not say the entry is missing:\n%s", out)
	}
}

// A kind nobody defined is a typo, and a typo that silently exempted a field
// would be the same defect one level up.
func TestAnUnknownKindIsReported(t *testing.T) {
	code, out := sweep(t, fixture{
		schema:     twoFields,
		consumer:   "var _ = func(p struct{ Port int }) int { return p.Port }\n",
		exemptions: "Service.Replicas\tfine\t.replicas\tSomebody typed a word that is not a kind.\n",
	})
	if code == 0 {
		t.Fatalf("an unknown kind passed the sweep:\n%s", out)
	}
	if !strings.Contains(out, `has kind "fine"`) {
		t.Fatalf("the report does not name the bad kind:\n%s", out)
	}
}
