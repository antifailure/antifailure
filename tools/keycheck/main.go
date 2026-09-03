// Command keycheck proves that no YAML file in this repository defines the
// same key twice in one mapping.
//
// The failure that earned it: examples/github-workflow.yml declared `seed` and
// `concurrency` twice each in its workflow_dispatch inputs. YAML resolves that
// by keeping the last definition, so the first pair's descriptions were text
// nobody would ever see, sitting in the file this product hands a new user as
// the worked example of how to wire it into CI. Nothing failed. The workflow
// parsed, GitHub accepted it, the example rendered, and the two descriptions
// were simply gone. A defect that produces no error and no wrong answer, only
// a quietly discarded half of a file, is the kind that survives every review.
//
// It is not a cosmetic class. The same shape in a workflow silently drops a
// `permissions` block, an `env` entry or an `if` guard, and the run that
// results looks exactly like the run somebody intended.
//
// WHY THIS WALKS THE NODE TREE RATHER THAN DECODING.
//
// gopkg.in/yaml.v3 does report a duplicate when a document is decoded into a
// Go map, and that is the obvious implementation. It is the wrong one here for
// two reasons, and both are the difference between a check that answers this
// question and one that answers a nearby question.
//
// The first is that decoding stops. An unmarshal error carries the first
// duplicate it met and abandons the rest of the document, so a file with four
// of these reports one, gets fixed once, and comes back. This walk reports
// every one of them in every file in a single pass.
//
// The second is that decoding fails for reasons that have nothing to do with
// duplicate keys. Half the YAML here is a Helm template or a GitHub expression,
// and a decode that errors on a type mismatch or an unknown tag is
// indistinguishable, from the caller's side, from a decode that errored on a
// duplicate. A check that cannot tell its own finding from an unrelated parse
// failure is a check that will one day be silenced wholesale. Parsing to a
// yaml.Node succeeds on anything syntactically well formed and leaves the
// judgement here.
//
// WHAT IS DELIBERATELY OUT OF SCOPE. Keys that repeat across sibling documents
// in a multi document file are not duplicates; each document is its own
// mapping and each is walked separately. A merge key is a key like any other
// and repeating it in one mapping is the same defect, so it is not special
// cased.
//
// HELM TEMPLATES ARE RENDERED RATHER THAN SKIPPED, and this is the half of the
// gate that took the longest to get right. A chart template is Go template
// source, not YAML: gopkg.in/yaml.v3 cannot parse one, so the first version of
// this command reported twelve unparseable files, checked the other forty
// three, and printed a summary that named fifty five. That summary is the
// defect this repository keeps finding in its own instruments. A check that
// silently covers four fifths of what it claims is worse than one that covers
// none, because the number reads as coverage.
//
// Nothing else catches it either. `helm lint` was pointed at a chart whose
// service.yaml defined `type` twice and it returned "1 chart(s) linted, 0
// chart(s) failed". A duplicate key survives rendering intact, so `helm
// template` output holds it and can be parsed. That is what this does.
//
// If helm is not installed this command FAILS rather than skipping the charts.
// Skipping would restore exactly the silence above, one environment variable
// further away.
//
// There are no exemptions and there is no exemption file, because unlike a
// naming rule or a prose rule this has no defensible exception: a mapping that
// defines a key twice has thrown one of them away, and no reason makes that
// intended.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// finding is one key that a later key in the same mapping shadowed.
type finding struct {
	File  string
	Line  int
	Key   string
	First int
}

func (f finding) String() string {
	return fmt.Sprintf("%s:%d: %q was already defined at line %d. YAML keeps this one, so the earlier definition has no effect.",
		f.File, f.Line, f.Key, f.First)
}

// skipped names directories that hold code this repository did not write.
var skipped = map[string]bool{
	".git": true, "node_modules": true, "dist": true, "build": true,
	".next": true, "vendor": true, "testdata": true,
}

func main() {
	root := flag.String("root", ".", "directory to scan")
	flag.Parse()

	charts, err := findCharts(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "keycheck:", err)
		os.Exit(2)
	}

	files, err := collect(*root, charts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "keycheck:", err)
		os.Exit(2)
	}

	var found []finding
	var unread []string
	for _, f := range files {
		hits, scanErr := scan(*root, f)
		if scanErr != nil {
			// A file this could not read is not a clean file. It is recorded
			// and it fails the run, because "no duplicates" said about
			// something nothing parsed is the silence this gate exists to
			// remove.
			fmt.Fprintf(os.Stderr, "keycheck: %s could not be parsed: %v\n", f, scanErr)
			unread = append(unread, f)
			continue
		}
		found = append(found, hits...)
	}

	for _, c := range charts {
		hits, renderErr := scanChart(*root, c)
		if renderErr != nil {
			fmt.Fprintf(os.Stderr, "keycheck: the chart at %s could not be rendered: %v\n", c, renderErr)
			unread = append(unread, c)
			continue
		}
		found = append(found, hits...)
	}

	if len(found) == 0 && len(unread) == 0 {
		fmt.Printf("keycheck: %d YAML files and %d rendered chart(s), no key defined twice in one mapping\n",
			len(files), len(charts))
		return
	}

	sort.Slice(found, func(i, j int) bool {
		if found[i].File != found[j].File {
			return found[i].File < found[j].File
		}
		return found[i].Line < found[j].Line
	})
	for _, f := range found {
		fmt.Println(f)
	}
	if len(found) > 0 {
		fmt.Fprintf(os.Stderr, "\nkeycheck: %d key(s) silently discarded. YAML keeps the LAST definition, so the earlier one has no effect.\n", len(found))
	}
	if len(unread) > 0 {
		fmt.Fprintf(os.Stderr, "keycheck: %d file(s) or chart(s) were not checked at all, listed above. That is a failure, not a pass.\n", len(unread))
	}
	os.Exit(1)
}

// chartValues are the minimum a render needs. They are values the chart
// REQUIRES and nothing more: every one of them is enforced by a `fail` in the
// chart's own templates, so a render without them stops before producing
// anything. Rendering with placeholders is safe because nothing here is
// deployed; the output is parsed and thrown away.
//
// A new required value breaks this loudly, with helm's own message naming it,
// which is the failure mode to want.
var chartValues = []string{
	"database.url=keycheck",
	"database.migrationUrl=keycheck",
	"github.clientId=keycheck",
	"github.clientSecret=keycheck",
	"github.redirectUri=https://keycheck.invalid/cb",
}

// findCharts returns the directory of every Helm chart under root.
func findCharts(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipped[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if d.Name() != "Chart.yaml" {
			return nil
		}
		rel, relErr := filepath.Rel(root, filepath.Dir(path))
		if relErr != nil {
			return relErr
		}
		out = append(out, rel)
		return nil
	})
	sort.Strings(out)
	return out, err
}

// scanChart renders one chart and reports every shadowed key in the result.
//
// The templates themselves are unparseable Go template source, so this is the
// only way to look inside them at all.
func scanChart(root, chart string) ([]finding, error) {
	if _, err := exec.LookPath("helm"); err != nil {
		return nil, fmt.Errorf("helm is not installed, so this chart's templates cannot be read: %w", err)
	}
	args := []string{"template", "keycheck", chart}
	for _, v := range chartValues {
		args = append(args, "--set", v)
	}
	cmd := exec.Command("helm", args...)
	cmd.Dir = root
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return parse(filepath.Join(chart, "(rendered)"), out)
}

// collect returns every YAML file under root that this repository wrote,
// except the template directories of the charts, which are rendered instead.
func collect(root string, charts []string) ([]string, error) {
	templates := map[string]bool{}
	for _, c := range charts {
		templates[filepath.Join(c, "templates")] = true
	}
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipped[d.Name()] {
				return fs.SkipDir
			}
			if rel, relErr := filepath.Rel(root, path); relErr == nil && templates[rel] {
				return fs.SkipDir
			}
			return nil
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".yml", ".yaml":
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				rel = path
			}
			out = append(out, rel)
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}

// scan reads one file and returns every shadowed key in it.
func scan(root, rel string) ([]finding, error) {
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		return nil, err
	}
	return parse(rel, b)
}

// parse walks every document in one YAML stream.
func parse(rel string, b []byte) ([]finding, error) {
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	var out []finding
	for {
		var doc yaml.Node
		if err := dec.Decode(&doc); err != nil {
			if errors.Is(err, io.EOF) {
				return out, nil
			}
			return nil, err
		}
		out = append(out, walk(rel, &doc)...)
	}
}

// walk descends one document, reporting duplicates in every mapping it holds.
func walk(rel string, n *yaml.Node) []finding {
	var out []finding
	if n.Kind == yaml.MappingNode {
		// A mapping's Content alternates key, value, key, value. Stepping by
		// two over pairs rather than iterating every child is what keeps a
		// value that happens to equal a key's name from being mistaken for one.
		first := map[string]int{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			if at, seen := first[k.Value]; seen {
				out = append(out, finding{File: rel, Line: k.Line, Key: k.Value, First: at})
				continue
			}
			first[k.Value] = k.Line
		}
	}
	for _, c := range n.Content {
		out = append(out, walk(rel, c)...)
	}
	return out
}
