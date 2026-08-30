// Command sbomcheck decides whether a release's bill of materials describes the
// release.
//
// It exists because the one the workflow produced described nothing. The
// publish job ran syft over dist/, and by the time it runs dist/ holds four
// .tar.gz files and a checksums file. syft's directory cataloger does not open
// archives, so it walked past all of them and emitted a valid SPDX 2.3 document
// containing exactly one package: the directory itself. Zero Go modules, zero
// files. That document would have been signed and published, and every stage of
// the pipeline was green, because every stage did exactly what it was asked.
// Measured on this repository: the same syft over the extracted binaries finds
// 363 packages.
//
// That is the same silent success the linker produced when it accepted -X for a
// symbol it could not find, and the remedy is the same. Nothing here trusts
// that the generator ran; it checks what the generator produced.
//
// Two questions, because either alone passes a broken release:
//
//   - Is this an SPDX document? Answered against the published SPDX 2.3 JSON
//     schema, so a format flag changed to cyclonedx, or a field syft renames in
//     a future version, is caught as a schema violation rather than as a
//     missing key nobody thought to look for.
//   - Does it describe THESE binaries? Answered by hashing every binary the
//     release built and requiring the document to record that exact SHA256.
//     A bill of materials that parses and lists somebody else's software, or
//     nothing at all, passes the first question and fails this one.
package main

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// The SPDX 2.3 JSON schema, from spdx/spdx-spec at tag v2.3,
// schemas/spdx-schema.json. Vendored rather than fetched: a gate that reaches
// the network answers a different question on a bad day, and a schema that can
// change underneath a pinned release is not a fixed contract to check against.
//
//go:embed schema/spdx-2.3.schema.json
var spdxSchema []byte

// The module this release is built from. Requiring it by name is what turns
// "the document has packages in it" into "the document is about us": a bill of
// materials catalogued from the wrong directory can be large, well formed, and
// entirely about something else.
const ownModule = "github.com/antifailure/antifailure/engine"

// document is the part of SPDX this reads. Everything else is the schema's
// business.
//
// files is a to-many relation and decodes as an array; each entry's checksums
// is to-many as well. Both are read off a real syft document rather than
// assumed, because getting the cardinality wrong one level up is how a decoder
// throws on the one element that has data and discards the whole collection.
type document struct {
	SPDXVersion string `json:"spdxVersion"`
	Name        string `json:"name"`
	Packages    []struct {
		Name string `json:"name"`
	} `json:"packages"`
	Files []struct {
		FileName  string `json:"fileName"`
		Checksums []struct {
			Algorithm string `json:"algorithm"`
			Value     string `json:"checksumValue"`
		} `json:"checksums"`
	} `json:"files"`
}

func main() {
	sbom := flag.String("sbom", "", "the SPDX document to check")
	artifacts := flag.String("artifacts", "", "directory holding the binaries the release built")
	name := flag.String("binary", "af", "the name of the binary within each platform's directory")
	minPackages := flag.Int("min-packages", 50, "the smallest package count that is not obviously empty")
	flag.Parse()

	if *sbom == "" || *artifacts == "" {
		fail("-sbom and -artifacts are both required")
	}

	problems, err := check(*sbom, *artifacts, *name, *minPackages, os.Stdout)
	if err != nil {
		fail("%v", err)
	}
	if len(problems) > 0 {
		fmt.Fprintf(os.Stderr, "\nsbomcheck: %s does not describe this release.\n", *sbom)
		for _, p := range problems {
			fmt.Fprintf(os.Stderr, "  %s\n", p)
		}
		fmt.Fprintf(os.Stderr, "\nA bill of materials nobody checks is decoration, and a signed "+
			"one that lists nothing is worse: it carries the authority of the signature.\n")
		os.Exit(1)
	}
}

// check returns one string per thing wrong with the document, and an error only
// when it could not reach an opinion at all.
//
// The two are kept apart because they mean different things to a release: a
// problem is a bill of materials that does not describe what shipped, and an
// error is not knowing, which for a gate is the same verdict but a different
// message to whoever has to fix it.
func check(sbomPath, artifacts, name string, minPackages int, out io.Writer) ([]string, error) {
	body, err := os.ReadFile(sbomPath)
	if err != nil {
		return nil, fmt.Errorf("reading the bill of materials: %w", err)
	}

	if err := validateAgainstSchema(body); err != nil {
		return []string{fmt.Sprintf("it is not a valid SPDX 2.3 document: %v", err)}, nil
	}
	fmt.Fprintf(out, "ok  %s validates against the SPDX 2.3 schema\n", filepath.Base(sbomPath))

	var doc document
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("reading the fields out of %s: %w", sbomPath, err)
	}

	var problems []string

	if len(doc.Packages) < minPackages {
		problems = append(problems, fmt.Sprintf(
			"it lists %d packages and %d is already a generous floor. An SPDX document "+
				"containing only the directory it scanned is what syft produces when it is "+
				"pointed at archives instead of binaries, and it looks exactly like a "+
				"working bill of materials",
			len(doc.Packages), minPackages))
	}

	if !describes(doc, ownModule) {
		problems = append(problems, fmt.Sprintf(
			"%s is not in it, so whatever was catalogued, it was not this release", ownModule))
	}

	binaries, err := findBinaries(artifacts, name)
	if err != nil {
		return nil, err
	}
	if len(binaries) == 0 {
		return nil, fmt.Errorf("found no binary named %q under %s. Either the release built "+
			"nothing or this is pointed at the wrong directory, and both are worth stopping for",
			name, artifacts)
	}

	recorded := map[string]string{}
	for _, f := range doc.Files {
		for _, c := range f.Checksums {
			if strings.EqualFold(c.Algorithm, "SHA256") {
				recorded[strings.ToLower(c.Value)] = f.FileName
			}
		}
	}

	for _, b := range binaries {
		sum, err := sha256File(b.path)
		if err != nil {
			return nil, fmt.Errorf("hashing %s: %w", b.path, err)
		}
		if _, ok := recorded[sum]; !ok {
			problems = append(problems, fmt.Sprintf(
				"%s hashes to %s and no file in the document has that hash, so the bill of "+
					"materials does not cover the binary that ships for %s",
				b.rel, sum, b.rel))
			continue
		}
		fmt.Fprintf(out, "ok  %s is described, sha256 %s\n", b.rel, sum)
	}

	if len(problems) == 0 {
		fmt.Fprintf(out, "sbomcheck: %d packages, %d binaries, every one described\n",
			len(doc.Packages), len(binaries))
	}
	return problems, nil
}

func validateAgainstSchema(body []byte) error {
	compiler := jsonschema.NewCompiler()
	schema, err := jsonschema.UnmarshalJSON(strings.NewReader(string(spdxSchema)))
	if err != nil {
		return fmt.Errorf("reading the embedded SPDX schema: %w", err)
	}
	const url = "https://spdx.org/schema/spdx-2.3.json"
	if err := compiler.AddResource(url, schema); err != nil {
		return fmt.Errorf("loading the embedded SPDX schema: %w", err)
	}
	compiled, err := compiler.Compile(url)
	if err != nil {
		return fmt.Errorf("compiling the embedded SPDX schema: %w", err)
	}

	instance, err := jsonschema.UnmarshalJSON(strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("the document is not JSON: %w", err)
	}
	return compiled.Validate(instance)
}

func describes(doc document, module string) bool {
	for _, p := range doc.Packages {
		if p.Name == module {
			return true
		}
	}
	return false
}

type binary struct {
	path string
	rel  string
}

// findBinaries returns every file with the given name under root, which for a
// release is one per platform. Discovered rather than declared on purpose: a
// list of platforms written here would go stale the moment the release matrix
// gains one, and the gate would then pass while silently covering three of four
// artifacts.
func findBinaries(root, name string) ([]binary, error) {
	var out []binary
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() != name {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		out = append(out, binary{path: p, rel: filepath.ToSlash(rel)})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking %s: %w", root, err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].rel < out[j].rel })
	return out, nil
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "sbomcheck: "+format+"\n", args...)
	os.Exit(1)
}
