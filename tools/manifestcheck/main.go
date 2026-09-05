// Command manifestcheck verifies that every manifest shown in the
// documentation is one the engine would actually accept.
//
// It exists because of a defect it found in a page written the same day. The
// hosted getting started page told a reader to write
//
//	control_plane:
//	  url: https://cp.example.com
//
// and the engine refuses that with AF-MAN-002, "Unknown key", because the
// manifest schema sets additionalProperties to false so that a typo cannot
// silently change an environment. The page was live on the site. Nothing in
// the documentation gates could see it: vale reads style, cspell reads
// spelling, lychee reads links, claimcheck reads repository paths, and none of
// them knows what a manifest is.
//
// This is the same shape as claimcheck, and the same shape as a function with
// no callers: something is written down, it looks right, and nothing connects
// it to the thing it claims to describe. The remedy is the same. Do not sweep
// for it by hand, because a sweep is only true on the day it is run.
//
// The first version of this gate could not see the defect it was written for,
// which is worth recording because it is the same mistake in a new place. It
// only checked a block whose every top level key was a top level property of
// the manifest, so that a fragment showing one indented field was skipped
// rather than guessed at. A block reading `control_plane:` has no top level
// property in it at all, so it was skipped too: the signal used to opt out was
// the defect itself.
//
// What separates a fragment from a mistake is not the top level. It is whether
// the key exists in the manifest anywhere. `rules`, `env`, `subset` and
// `teardown_on` are all real keys shown without their parent. `control_plane`
// is a key the manifest does not have at any depth, which is exactly what
// AF-MAN-002 says when the engine reads one. So:
//
//   - every top level key is a top level property: a whole manifest, and every
//     key at every depth is checked against the schema
//   - every top level key exists somewhere in the schema: a fragment, and the
//     path cannot be known, so it passes
//   - any top level key exists nowhere: reported
//
// Genuinely foreign YAML does appear in these pages, because the Database Lab
// provider documents that product's own configuration file. Those are listed
// in tools/docs/manifest-exemptions.tsv with a reason each, and an exemption
// that stops being needed is reported too, so the list cannot rot.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

func main() {
	flag.Parse()
	root := "."
	if flag.NArg() > 0 {
		root = flag.Arg(0)
	}
	if err := run(root, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "manifestcheck:", err)
		os.Exit(1)
	}
}

// fence matches a fenced block and captures its language and body.
var fence = regexp.MustCompile("(?s)```([A-Za-z0-9_-]*)\\n(.*?)```")

func run(root string, out *os.File) error {
	schemaPath := filepath.Join(root, "schemas", "manifest.v1.json")
	body, err := os.ReadFile(schemaPath)
	if err != nil {
		return fmt.Errorf("reading the manifest schema: %w", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return fmt.Errorf("parsing %s: %w", schemaPath, err)
	}

	anywhere := keysAnywhere(doc)
	exempt, err := readExemptions(filepath.Join(root, "tools", "docs", "manifest-exemptions.tsv"))
	if err != nil {
		return err
	}
	used := map[string]bool{}

	pages, err := collect(root)
	if err != nil {
		return err
	}
	if len(pages) == 0 {
		// An empty run is an error rather than a quiet success, for the same
		// reason as every other gate here: a checker that silently checks
		// nothing is worse than no checker, because it reports green.
		return fmt.Errorf("found no pages under %s, so nothing was checked", root)
	}

	var findings []string
	checked := 0
	for _, page := range pages {
		text, readErr := os.ReadFile(page)
		if readErr != nil {
			return readErr
		}
		rel, _ := filepath.Rel(root, page)
		for _, block := range fence.FindAllStringSubmatch(string(text), -1) {
			lang, snippet := strings.ToLower(block[1]), block[2]
			if lang != "yaml" && lang != "yml" {
				continue
			}
			var node any
			if err := yaml.Unmarshal([]byte(snippet), &node); err != nil {
				// A block a reader is invited to copy has to parse. This one
				// is reported whether or not it is a manifest, because there
				// is no kind of YAML for which being unparseable is fine.
				findings = append(findings,
					fmt.Sprintf("%s: a yaml block does not parse: %v", rel, err))
				continue
			}
			mapping, ok := node.(map[string]any)
			if !ok {
				continue
			}
			switch classify(mapping, doc, anywhere) {
			case whole:
				checked++
				walk(mapping, doc, doc, "", func(msg string) {
					findings = append(findings, rel+": "+msg)
				})
			case fragment:
				// A real key shown without its parent. The path cannot be
				// known from the block alone, so this passes rather than
				// guessing at a parent and reporting the guess.
			case foreign:
				for _, key := range sortedKeys(mapping) {
					if anywhere[key] {
						continue
					}
					if reason, ok := exempt[exemption{rel, key}]; ok {
						used[rel+"\t"+key] = true
						_ = reason
						continue
					}
					findings = append(findings, fmt.Sprintf(
						"%s: a yaml block has %q at its top level, which the manifest does not have "+
							"at any depth. The engine refuses an unknown key with AF-MAN-002. If this "+
							"block is another product's file, add it to "+
							"tools/docs/manifest-exemptions.tsv with a reason.", rel, key))
				}
			}
		}
	}

	for key, reason := range exempt {
		if !used[key.page+"\t"+key.key] {
			findings = append(findings, fmt.Sprintf(
				"tools/docs/manifest-exemptions.tsv: %s in %s is exempted and no longer needed (%s)",
				key.key, key.page, reason))
		}
	}

	sort.Strings(findings)
	if len(findings) > 0 {
		for _, f := range findings {
			fmt.Fprintln(os.Stderr, f)
		}
		fmt.Fprintf(os.Stderr, "manifestcheck: %d %s in %d %s\n",
			len(findings), plural(len(findings), "problem", "problems"),
			checked, plural(checked, "manifest", "manifests"))
		return fmt.Errorf("the documentation shows a manifest the engine would refuse")
	}
	_, _ = fmt.Fprintf(out, "manifestcheck: %d %s in the documentation, every key is one the engine accepts\n",
		checked, plural(checked, "manifest", "manifests"))
	return nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// kind is what a yaml block turned out to be.
type kind int

const (
	whole kind = iota
	fragment
	foreign
)

func classify(m map[string]any, root map[string]any, anywhere map[string]bool) kind {
	if len(m) == 0 {
		return fragment
	}
	props, _ := root["properties"].(map[string]any)
	top, known := true, true
	for key := range m {
		if _, found := props[key]; !found {
			top = false
		}
		if !anywhere[key] {
			known = false
		}
	}
	switch {
	case top:
		return whole
	case known:
		return fragment
	default:
		return foreign
	}
}

// keysAnywhere collects every property name the schema uses at any depth.
//
// This is what tells a fragment from a mistake. A key shown without its parent
// is still a key the manifest has; a key the manifest does not have anywhere
// is the thing AF-MAN-002 refuses.
func keysAnywhere(root map[string]any) map[string]bool {
	out := map[string]bool{}
	var visit func(node any)
	visit = func(node any) {
		switch v := node.(type) {
		case map[string]any:
			if props, ok := v["properties"].(map[string]any); ok {
				for key, child := range props {
					out[key] = true
					visit(child)
				}
			}
			for _, key := range []string{"items", "additionalProperties", "$defs", "definitions"} {
				if child, ok := v[key]; ok {
					visit(child)
				}
			}
			for _, key := range []string{"oneOf", "anyOf", "allOf"} {
				if list, ok := v[key].([]any); ok {
					for _, alt := range list {
						visit(alt)
					}
				}
			}
			if defs, ok := v["$defs"].(map[string]any); ok {
				for _, child := range defs {
					visit(child)
				}
			}
		case []any:
			for _, child := range v {
				visit(child)
			}
		}
	}
	visit(root)
	return out
}

// exemption names one foreign key on one page.
type exemption struct{ page, key string }

// readExemptions loads the list of yaml blocks that belong to another product.
//
// A reason is required, for the same reason the forbidden scan requires one:
// an exemption with no argument behind it is indistinguishable from somebody
// silencing a finding they did not understand.
func readExemptions(path string) (map[exemption]string, error) {
	out := map[exemption]string{}
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	for i, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 3 || strings.TrimSpace(parts[2]) == "" {
			return nil, fmt.Errorf("%s line %d: want three tab separated fields, page, key and reason", path, i+1)
		}
		out[exemption{parts[0], parts[1]}] = parts[2]
	}
	return out, nil
}

// deref follows a local $ref, once.
func deref(node, root map[string]any) map[string]any {
	ref, ok := node["$ref"].(string)
	if !ok || !strings.HasPrefix(ref, "#/") {
		return node
	}
	cur := any(root)
	for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		m, isMap := cur.(map[string]any)
		if !isMap {
			return node
		}
		cur = m[part]
	}
	if resolved, isMap := cur.(map[string]any); isMap {
		return resolved
	}
	return node
}

// branches returns the schemas a value may satisfy: the node itself, plus any
// oneOf or anyOf alternatives. A key is accepted when any branch declares it,
// which is the conservative direction: this gate should report a key that no
// reading of the schema allows, and stay quiet otherwise.
func branches(node, root map[string]any) []map[string]any {
	node = deref(node, root)
	out := []map[string]any{node}
	for _, key := range []string{"oneOf", "anyOf", "allOf"} {
		list, ok := node[key].([]any)
		if !ok {
			continue
		}
		for _, alt := range list {
			if m, isMap := alt.(map[string]any); isMap {
				out = append(out, deref(m, root))
			}
		}
	}
	return out
}

// walk checks every key in a value against the schema for it.
func walk(value any, node, root map[string]any, path string, report func(string)) {
	switch v := value.(type) {
	case map[string]any:
		all := branches(node, root)
		for _, key := range sortedKeys(v) {
			child, allowed := propertyFor(all, key)
			if !allowed {
				report(fmt.Sprintf("%s is not a key the manifest has", join(path, key)))
				continue
			}
			if child != nil {
				walk(v[key], child, root, join(path, key), report)
			}
		}
	case []any:
		for _, alt := range branches(node, root) {
			items, ok := alt["items"].(map[string]any)
			if !ok {
				continue
			}
			for i, element := range v {
				walk(element, items, root, fmt.Sprintf("%s[%d]", path, i), report)
			}
			return
		}
	case string:
		checkEnum(v, node, root, path, report)
	}
}

// checkEnum reports a documented value the schema's enum does not list.
//
// The gate could not see this, and it was written for exactly the defect it
// could not see. Two live pages told a reader to write
// `teardown_on: [closed, merged]`, which the engine refuses with AF-MAN-002
// because the enum is close, merge and ttl, and a third had already been
// caught by hand and written up in .changes. Every one of them passed here,
// because walk only ever asked whether a KEY exists. A key that exists with a
// value the engine will not take is the same failure for the reader: they copy
// the block and the manifest does not load.
//
// Deliberately conservative in two directions, because a documentation gate
// that cries wolf is a documentation gate somebody disables. It looks at
// strings only, since every enum in this schema is a list of strings and
// comparing a YAML integer to a JSON number invites a false positive over
// nothing. And it stays quiet unless EVERY branch that has an enum refuses the
// value, so a value permitted by one arm of a oneOf is permitted.
func checkEnum(value string, node, root map[string]any, path string, report func(string)) {
	var allowed []string
	for _, alt := range branches(node, root) {
		list, ok := alt["enum"].([]any)
		if !ok {
			// A branch with no enum accepts this value as far as this check
			// can tell, so there is nothing to report.
			return
		}
		for _, want := range list {
			text, isText := want.(string)
			if !isText {
				return
			}
			if text == value {
				return
			}
			allowed = append(allowed, text)
		}
	}
	if len(allowed) == 0 {
		return
	}
	sort.Strings(allowed)
	report(fmt.Sprintf("%s is %q, which is not one the manifest accepts. The engine refuses it "+
		"with AF-MAN-002. It has to be one of: %s", path, value, strings.Join(allowed, ", ")))
}

// propertyFor finds the schema for a key across a node's branches, and reports
// whether any branch permits it at all.
func propertyFor(all []map[string]any, key string) (map[string]any, bool) {
	permissive := false
	for _, node := range all {
		props, ok := node["properties"].(map[string]any)
		if ok {
			if child, found := props[key]; found {
				if m, isMap := child.(map[string]any); isMap {
					return m, true
				}
				return nil, true
			}
		}
		// A node that does not close itself accepts anything, and a node with
		// no properties at all is not describing an object this gate can check.
		if additional, set := node["additionalProperties"]; !set || additional != false {
			if ok || node["type"] == "object" {
				permissive = true
			}
		}
		if !ok && node["type"] != "object" {
			permissive = true
		}
	}
	return nil, permissive
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// collect finds the pages a reader sees. The same roots as the readability
// report, for the same reason: an example's README is documentation.
func collect(root string) ([]string, error) {
	var out []string
	for _, dir := range []string{
		filepath.Join(root, "docs", "src", "content", "docs"),
		filepath.Join(root, "examples"),
	} {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				switch d.Name() {
				case "node_modules", ".next", "dist", "vendor":
					return fs.SkipDir
				}
				return nil
			}
			if ext := filepath.Ext(path); ext == ".md" || ext == ".mdx" {
				out = append(out, path)
			}
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	sort.Strings(out)
	return out, nil
}
