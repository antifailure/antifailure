// Command schemadoc renders every JSON Schema in schemas/ into a reference
// page.
//
// The schemas are the contract: the manifest is what a user writes and the
// event envelope is what every consumer reads. A hand written page describing
// either one is a second copy of the contract that drifts from the first, and
// the drift is invisible because both look like documentation. This renders
// the page from the schema instead, and `just _generated` fails when the two
// disagree.
//
// The prose pages beside these stay hand written. A generated table says what
// a field is; it cannot say why the field exists or what happens when it is
// wrong, and that is most of what a reference is for.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	if err := run(root); err != nil {
		fmt.Fprintln(os.Stderr, "schemadoc:", err)
		os.Exit(1)
	}
}

// outDir is where the pages land, relative to the repository root.
const outDir = "docs/src/content/docs/reference/schemas"

func run(root string) error {
	paths, err := filepath.Glob(filepath.Join(root, "schemas", "*.json"))
	if err != nil {
		return err
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		return fmt.Errorf("no JSON under %s, so this generator is looking in the wrong place",
			filepath.Join(root, "schemas"))
	}

	dir := filepath.Join(root, outDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	written := 0
	var kept []string
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		// A file under schemas/ that is not a schema is data: the policy
		// vectors are a conformance corpus, not a contract with a shape worth
		// tabulating. Skipping on the absence of $schema is what the JSON
		// itself says about which it is.
		if _, ok := doc["$schema"]; !ok {
			continue
		}

		name := pageName(filepath.Base(path))
		out := filepath.Join(dir, name+".md")
		page, err := render(doc, filepath.Base(path))
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if err := os.WriteFile(out, []byte(page), 0o644); err != nil {
			return err
		}
		kept = append(kept, name)
		written++
	}

	if written == 0 {
		return fmt.Errorf("every file under schemas/ was skipped for having no $schema, " +
			"which means either they are all data or the check is wrong")
	}

	// A page left behind by a schema that was deleted or renamed is a
	// reference to something that no longer exists, which is the one failure a
	// generated page is supposed to make impossible.
	stale, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil {
		return err
	}
	for _, path := range stale {
		base := strings.TrimSuffix(filepath.Base(path), ".md")
		if base == "index" || contains(kept, base) {
			continue
		}
		if err := os.Remove(path); err != nil {
			return err
		}
		fmt.Printf("schemadoc: removed %s, whose schema is gone\n", base)
	}

	fmt.Printf("schemadoc: %d schemas rendered into %s\n", written, outDir)
	return nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// pageName turns manifest.v1.json into manifest-v1.
func pageName(base string) string {
	base = strings.TrimSuffix(base, ".json")
	return strings.ReplaceAll(base, ".", "-")
}

func render(doc map[string]any, source string) (string, error) {
	title := str(doc, "title")
	if title == "" {
		return "", fmt.Errorf("no title, so the page would have no name")
	}
	description := str(doc, "description")
	if description == "" {
		return "", fmt.Errorf("no description, so the page would open on a table")
	}

	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "title: %s\n", yamlString(title+" schema"))
	fmt.Fprintf(&b, "description: %s\n", yamlString(firstSentence(description)))
	b.WriteString("---\n\n")

	fmt.Fprintf(&b, "%s\n\n", description)
	fmt.Fprintf(&b,
		":::note\nThis page is generated from `schemas/%s`. "+
			"Edit the schema, then run `just generate`.\n:::\n\n", source)

	defs := object(doc, "$defs")
	names := sortedKeys(defs)

	// A section with no opening sentence is a table with no explanation, and
	// the field that points at it gets an empty cell in the table above.
	// Refusing here is what keeps the reference complete: the schema is the
	// only place the sentence can come from.
	var undescribed []string
	for _, name := range names {
		if str(object(defs, name), "description") == "" {
			undescribed = append(undescribed, name)
		}
	}
	if len(undescribed) > 0 {
		return "", fmt.Errorf("these definitions have no description, so their sections would open on a table: %s",
			strings.Join(undescribed, ", "))
	}

	b.WriteString(section("The document", doc, defs))

	for _, name := range names {
		def := object(defs, name)
		heading := str(def, "title")
		if heading == "" {
			heading = humanise(name)
		}
		b.WriteString(section(heading, def, defs))
	}
	return b.String(), nil
}

// section renders one object's properties as a table, plus any enumerated
// value list the object itself carries.
func section(heading string, schema map[string]any, defs map[string]any) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %s\n\n", heading)

	if d := str(schema, "description"); d != "" && heading != "The document" {
		fmt.Fprintf(&b, "%s\n\n", d)
	}

	props := object(schema, "properties")
	if len(props) == 0 {
		if values := enumTable(schema); values != "" {
			b.WriteString(values)
			return b.String()
		}
		b.WriteString("No fields.\n\n")
		return b.String()
	}

	required := map[string]bool{}
	for _, r := range list(schema, "required") {
		if s, ok := r.(string); ok {
			required[s] = true
		}
	}

	b.WriteString("| Field | Type | Required | Notes |\n")
	b.WriteString("| --- | --- | --- | --- |\n")
	for _, name := range sortedKeys(props) {
		field := object(props, name)
		req := "no"
		if required[name] {
			req = "**yes**"
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n",
			name, typeOf(field, defs), req, notes(field, defs))
	}
	b.WriteString("\n")

	// A field whose values are enumerated with a description each gets its own
	// table. Squeezing sixty event types into a Notes cell makes a page nobody
	// can read, and dropping them makes a reference that does not reference.
	for _, name := range sortedKeys(props) {
		field := object(props, name)
		if t := enumTableFor(name, field); t != "" {
			b.WriteString(t)
		}
	}
	return b.String()
}

func enumTable(schema map[string]any) string { return enumTableFor("", schema) }

func enumTableFor(name string, field map[string]any) string {
	entries := list(field, "oneOf")
	if len(entries) == 0 {
		return ""
	}
	described := false
	for _, e := range entries {
		if m, ok := e.(map[string]any); ok && str(m, "description") != "" {
			described = true
			break
		}
	}
	if !described {
		return ""
	}

	var b strings.Builder
	if name != "" {
		fmt.Fprintf(&b, "### Values for `%s`\n\n", name)
	}
	b.WriteString("| Value | Meaning |\n| --- | --- |\n")
	for _, e := range entries {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		fmt.Fprintf(&b, "| `%v` | %s |\n", m["const"], escape(str(m, "description")))
	}
	b.WriteString("\n")
	return b.String()
}

// typeOf renders a field's type, following a local reference to the section
// that describes it.
func typeOf(field map[string]any, defs map[string]any) string {
	if ref := str(field, "$ref"); ref != "" {
		return link(ref, defs)
	}
	items := object(field, "items")
	if len(items) > 0 {
		if ref := str(items, "$ref"); ref != "" {
			return "list of " + link(ref, defs)
		}
		return "list of " + plain(str(items, "type"))
	}
	if enum := list(field, "enum"); len(enum) > 0 {
		parts := make([]string, 0, len(enum))
		for _, v := range enum {
			parts = append(parts, fmt.Sprintf("`%v`", v))
		}
		return strings.Join(parts, ", ")
	}
	if _, ok := field["oneOf"]; ok {
		return "string"
	}
	return plain(str(field, "type"))
}

func plain(t string) string {
	if t == "" {
		return "any"
	}
	return t
}

// link turns #/$defs/service into a link to that section, using the same
// heading the section was rendered with.
func link(ref string, defs map[string]any) string {
	name := strings.TrimPrefix(ref, "#/$defs/")
	heading := humanise(name)
	if def := object(defs, name); len(def) > 0 {
		if t := str(def, "title"); t != "" {
			heading = t
		}
	}
	return fmt.Sprintf("[%s](#%s)", heading, anchor(heading))
}

// notes is the prose cell: the description, then the constraints that change
// what a valid value is.
func notes(field map[string]any, defs map[string]any) string {
	parts := []string{}
	d := str(field, "description")
	if d == "" {
		// A property that is a bare reference carries no prose of its own, and
		// an empty cell in a reference table tells a reader nothing. The
		// referenced object's own opening sentence is the closest true thing,
		// and it is one click from the section that says the rest.
		d = firstSentence(refDescription(field, defs))
	}
	if d != "" {
		parts = append(parts, escape(d))
	}
	if v, ok := field["default"]; ok {
		parts = append(parts, fmt.Sprintf("Defaults to `%v`.", v))
	}

	var limits []string
	for _, k := range []string{"minimum", "maximum", "minLength", "maxLength", "minItems", "maxItems", "maxProperties"} {
		if v, ok := field[k]; ok {
			limits = append(limits, fmt.Sprintf("%s %v", humanise(k), v))
		}
	}
	if p := str(field, "pattern"); p != "" {
		limits = append(limits, "matches `"+escape(p)+"`")
	}
	if f := str(field, "format"); f != "" {
		limits = append(limits, "format `"+f+"`")
	}
	if len(limits) > 0 {
		parts = append(parts, capitalise(strings.Join(limits, ", "))+".")
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ")
}

// refDescription reads the description of whatever a field points at, through
// a direct reference or through the item type of a list.
func refDescription(field map[string]any, defs map[string]any) string {
	ref := str(field, "$ref")
	if ref == "" {
		ref = str(object(field, "items"), "$ref")
	}
	if ref == "" {
		return ""
	}
	return str(object(defs, strings.TrimPrefix(ref, "#/$defs/")), "description")
}

// escape makes a value safe inside a table cell. A pipe in a regular
// expression would otherwise end the column and shift every cell after it.
func escape(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	return strings.ReplaceAll(s, "\n", " ")
}

// anchor is the fragment Starlight gives a heading.
func anchor(heading string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(heading) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ', r == '-', r == '_':
			b.WriteRune('-')
		}
	}
	return b.String()
}

// humanise turns maxItems into "max items" and egressRule into "egress rule".
func humanise(s string) string {
	var b strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteRune(' ')
			}
			b.WriteRune(r - 'A' + 'a')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func capitalise(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	if r[0] >= 'a' && r[0] <= 'z' {
		r[0] = r[0] - 'a' + 'A'
	}
	return string(r)
}

func firstSentence(s string) string {
	if i := strings.Index(s, ". "); i > 0 {
		return s[:i+1]
	}
	return s
}

// yamlString quotes a frontmatter value. Titles and descriptions carry colons
// and apostrophes, both of which change what YAML reads.
func yamlString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func str(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func object(m map[string]any, key string) map[string]any {
	if v, ok := m[key].(map[string]any); ok {
		return v
	}
	return nil
}

func list(m map[string]any, key string) []any {
	if v, ok := m[key].([]any); ok {
		return v
	}
	return nil
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
