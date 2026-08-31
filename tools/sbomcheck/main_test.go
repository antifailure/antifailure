package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// binaries stages a release tree the way the publish job has it after the
// archives are extracted: one directory per platform, each holding an af.
func binaries(t *testing.T, platforms ...string) (dir string, sums map[string]string) {
	t.Helper()
	dir = t.TempDir()
	sums = map[string]string{}
	for _, p := range platforms {
		body := []byte("binary for " + p + "\n")
		if err := os.MkdirAll(filepath.Join(dir, p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, p, "af"), body, 0o755); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(body)
		sums[p] = hex.EncodeToString(sum[:])
	}
	return dir, sums
}

// spdx builds a document that satisfies the published schema, so that a test
// which expects a failure fails for the reason it names rather than because the
// fixture was malformed.
func spdx(packageCount int, includeOwn bool, files map[string]string) map[string]any {
	pkgs := []any{}
	if includeOwn {
		pkgs = append(pkgs, map[string]any{
			"name":             ownModule,
			"SPDXID":           "SPDXRef-Package-own",
			"downloadLocation": "NOASSERTION",
			"filesAnalyzed":    false,
			"copyrightText":    "NOASSERTION",
		})
	}
	for i := len(pkgs); i < packageCount; i++ {
		pkgs = append(pkgs, map[string]any{
			"name":             fmt.Sprintf("example.com/dependency%d", i),
			"SPDXID":           fmt.Sprintf("SPDXRef-Package-%d", i),
			"downloadLocation": "NOASSERTION",
			"filesAnalyzed":    false,
			"copyrightText":    "NOASSERTION",
		})
	}

	entries := []any{}
	i := 0
	for name, sum := range files {
		entries = append(entries, map[string]any{
			"fileName":      name,
			"SPDXID":        fmt.Sprintf("SPDXRef-File-%d", i),
			"copyrightText": "NOASSERTION",
			"checksums": []any{
				map[string]any{"algorithm": "SHA256", "checksumValue": sum},
			},
		})
		i++
	}

	return map[string]any{
		"spdxVersion":       "SPDX-2.3",
		"dataLicense":       "CC0-1.0",
		"SPDXID":            "SPDXRef-DOCUMENT",
		"name":              "antifailure",
		"documentNamespace": "https://antifailure.dev/spdx/test",
		"creationInfo": map[string]any{
			"created":  "2026-08-29T22:19:34Z",
			"creators": []any{"Tool: syft-1.51.1"},
		},
		"packages": pkgs,
		"files":    entries,
	}
}

func writeSBOM(t *testing.T, doc map[string]any) string {
	t.Helper()
	body, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "sbom.spdx.json")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func run(t *testing.T, sbom, artifacts string) []string {
	t.Helper()
	problems, err := check(sbom, artifacts, "af", 50, io.Discard)
	if err != nil {
		t.Fatalf("check could not reach a verdict: %v", err)
	}
	return problems
}

// The positive control, and it has to come first: without a document this
// accepts, every failing test below would pass against a command that refused
// everything.
func TestACompleteBillOfMaterialsIsAccepted(t *testing.T) {
	dir, sums := binaries(t, "antifailure_1.2.3_linux_amd64", "antifailure_1.2.3_darwin_arm64")
	files := map[string]string{}
	for p, sum := range sums {
		files[p+"/af"] = sum
	}
	sbom := writeSBOM(t, spdx(60, true, files))

	if problems := run(t, sbom, dir); len(problems) > 0 {
		t.Fatalf("a complete bill of materials was refused: %v", problems)
	}
}

// The document the workflow actually produced. syft over a directory of
// archives emits one package, which is the directory, and no files at all.
// It is valid SPDX and it describes nothing.
func TestTheEmptyDocumentSyftProducesIsRefused(t *testing.T) {
	dir, _ := binaries(t, "antifailure_1.2.3_linux_amd64")
	sbom := writeSBOM(t, spdx(1, false, nil))

	problems := run(t, sbom, dir)
	if len(problems) == 0 {
		t.Fatal("a document listing one package and no files was accepted as a release's " +
			"bill of materials")
	}
	if !mentions(problems, "packages") {
		t.Errorf("nothing in the verdict mentions the package count: %v", problems)
	}
	if !mentions(problems, ownModule) {
		t.Errorf("nothing in the verdict says the engine is missing: %v", problems)
	}
}

// The case that matters most and is the easiest to miss: a document large
// enough to look right, catalogued from the wrong tree. Every package count
// check passes and it still does not describe what shipped.
func TestADocumentAboutOtherBinariesIsRefused(t *testing.T) {
	dir, _ := binaries(t, "antifailure_1.2.3_linux_amd64")
	other := sha256.Sum256([]byte("some other binary\n"))
	files := map[string]string{
		"antifailure_1.2.3_linux_amd64/af": hex.EncodeToString(other[:]),
	}
	sbom := writeSBOM(t, spdx(200, true, files))

	problems := run(t, sbom, dir)
	if len(problems) == 0 {
		t.Fatal("a document recording the wrong hash for the shipped binary was accepted")
	}
	if !mentions(problems, "does not cover the binary") {
		t.Errorf("the verdict does not say which binary is uncovered: %v", problems)
	}
}

// One platform described and another not. The four-platform matrix is exactly
// where a per-platform gap hides, because three green lines read as a pass.
func TestOnePlatformMissingIsRefused(t *testing.T) {
	dir, sums := binaries(t, "antifailure_1.2.3_linux_amd64", "antifailure_1.2.3_darwin_arm64")
	files := map[string]string{
		"antifailure_1.2.3_linux_amd64/af": sums["antifailure_1.2.3_linux_amd64"],
	}
	sbom := writeSBOM(t, spdx(60, true, files))

	problems := run(t, sbom, dir)
	if len(problems) != 1 {
		t.Fatalf("expected exactly the one uncovered platform, got %v", problems)
	}
	if !mentions(problems, "darwin_arm64") {
		t.Errorf("the verdict names the wrong platform: %v", problems)
	}
}

// A format flag changed from spdx-json to something else would otherwise sail
// through: the file would still be JSON, still be signed, and still be
// published under a name ending .spdx.json.
func TestSomethingThatIsNotSPDXIsRefused(t *testing.T) {
	dir, _ := binaries(t, "antifailure_1.2.3_linux_amd64")
	path := filepath.Join(t.TempDir(), "sbom.spdx.json")
	// A CycloneDX document, which is the other thing syft is routinely asked
	// for and the realistic way this file stops being SPDX.
	body := `{"bomFormat":"CycloneDX","specVersion":"1.5","version":1,"components":[]}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	problems := run(t, path, dir)
	if len(problems) == 0 {
		t.Fatal("a CycloneDX document was accepted as SPDX")
	}
	if !mentions(problems, "SPDX 2.3") {
		t.Errorf("the verdict does not say the document is not SPDX: %v", problems)
	}
}

// The schema is the half that says "this is an SPDX document" and it cannot say
// anything about whether the document is about us. Asserting that here keeps
// the two halves from being confused for each other later: the real defect this
// command was written for passed the schema.
func TestTheSchemaAloneDoesNotCatchAnEmptyDocument(t *testing.T) {
	body, err := json.Marshal(spdx(1, false, nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateAgainstSchema(body); err != nil {
		t.Fatalf("a document with one package and no files failed the schema, which means "+
			"this test no longer demonstrates what it claims: %v", err)
	}
}

func TestABrokenDocumentFailsTheSchema(t *testing.T) {
	doc := spdx(60, true, nil)
	delete(doc, "spdxVersion")
	body, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateAgainstSchema(body); err == nil {
		t.Fatal("a document with no spdxVersion passed the schema")
	}
}

// Discovered rather than declared, so a fifth platform is covered without
// anybody remembering to add it here.
func TestEveryPlatformIsFoundWithoutBeingListed(t *testing.T) {
	dir, _ := binaries(t, "a_linux_amd64", "b_linux_arm64", "c_darwin_amd64", "d_darwin_arm64")
	found, err := findBinaries(dir, "af")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 4 {
		t.Fatalf("found %d binaries, want 4", len(found))
	}
	for i := 1; i < len(found); i++ {
		if found[i-1].rel > found[i].rel {
			t.Fatalf("results are not sorted, so the output order depends on the filesystem")
		}
	}
}

func TestNoBinariesAtAllIsAnError(t *testing.T) {
	sbom := writeSBOM(t, spdx(60, true, nil))
	if _, err := check(sbom, t.TempDir(), "af", 50, io.Discard); err == nil {
		t.Fatal("a release directory with no binaries in it reached a verdict")
	}
}

func mentions(problems []string, want string) bool {
	for _, p := range problems {
		if strings.Contains(p, want) {
			return true
		}
	}
	return false
}
