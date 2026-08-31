package main

// The second half of this tool: a reference table cell that claims to list a
// closed set the schema declares must list all of it.
//
// This exists because docs/src/content/docs/reference/manifest.md is written
// by hand for a machine readable schema, and the site's own documentation
// index claimed it was generated and gated when neither was true. It had
// drifted three times under that claim. egress.default said "block or allow"
// against a six value enum, build.strategy omitted image while the table
// documents the image key four rows further down, and database.provider named
// two of four while the engine constructs all four.
//
// THE RULE IS DELIBERATELY NARROW, and the narrowness is the whole design.
//
// The obvious version of this check, scanning the page for enum values, does
// not work and the numbers are worth keeping. The schema declares 29 string
// enums holding 80 distinct values, and 19 of those 80 appear in this one page
// as ordinary English: block 13 times, docs 7, never 4, image 3, schema 3.
// Backticks do not rescue it either, because the page backticks key names as
// well as values, and services, database and runtime are simultaneously top
// level keys and fidelity.require values. A value scanning gate would fire
// constantly on correct prose and be switched off within a week, which is
// worse than the ungated page it replaced.
//
// What is reliable is reading the table structurally. The headings already are
// the schema paths, so heading plus first cell identifies a property exactly,
// and then only one cell is read: the last one, which is the prose. Two
// refinements were each found by a false alarm rather than by reasoning:
//
//   - Read the LAST cell, not everything after the key. The policy table has a
//     Default column holding one value, and counting that as part of the
//     enumeration invented a finding on policy.query_regression.
//   - Judge only a cell that is ALREADY enumerating, meaning it names at least
//     one value and joins with or or and. Demanding that every enum row
//     restate its enumeration would fire on the nine policy rows, whose
//     section states "Every key takes ignore, warn or fail" once in its
//     preamble and whose rows sensibly do not repeat it. That gate would make
//     the page worse to read while calling it correctness.
//
// Measured against the page as it stood: five cells enumerate, three are
// incomplete, no false alarms.

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// heading matches a section whose title is a manifest key, which is how this
// reference names the object a table describes.
var heading = regexp.MustCompile("^#{2,6} `([a-z_]+)`\\s*$")

// anyHeading ends the previous section. A heading that is prose rather than a
// key, such as "What a service is given", describes no schema object, so rows
// under it are not matched to one.
var anyHeading = regexp.MustCompile(`^#{1,6}\s`)

// tableRow matches a row whose first cell is a manifest key. A row whose key
// cell holds a dotted name, such as migration_lock.warn_ms, is not a property
// of the object the heading names and does not match.
var tableRow = regexp.MustCompile("^\\|\\s*`([a-z_]+)`\\s*\\|(.*)\\|\\s*$")

// joined is what separates a cell that lists a set from a cell that mentions
// one of its members in passing.
var joined = regexp.MustCompile(`\b(or|and)\b`)

// depthOf returns a heading's level, or 0 for anything that is not a heading.
func depthOf(line string) int {
	if !anyHeading.MatchString(line) {
		return 0
	}
	return len(line) - len(strings.TrimLeft(line, "#"))
}

// keyOf is the manifest key a heading names, or "" when its title is prose.
//
// A prose heading still occupies its depth in the stack rather than being
// dropped, because it ends the section above it without ending the sections it
// sits inside. It resolves to nothing on its own: the schema has no property
// named "", so a path containing one finds nothing and its rows go unchecked,
// which is the right answer for a table the schema has no object for.
func keyOf(line string) string {
	m := heading.FindStringSubmatch(line)
	if m == nil {
		return ""
	}
	return m[1]
}

// schemaTree resolves a manifest key path to the values it may take.
type schemaTree struct {
	root any
}

func loadSchema(path string) (*schemaTree, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc any
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &schemaTree{root: doc}, nil
}

// deref follows a $ref to the definition it names. The schema keeps its
// repeated objects under $defs and refers to them, so a walk that does not
// follow refs sees an egress rule as an empty node.
func (t *schemaTree) deref(node any) any {
	for i := 0; i < 10; i++ {
		m, ok := node.(map[string]any)
		if !ok {
			return node
		}
		ref, ok := m["$ref"].(string)
		if !ok {
			return node
		}
		cur := t.root
		for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
			cm, ok := cur.(map[string]any)
			if !ok {
				return nil
			}
			cur = cm[part]
		}
		node = cur
	}
	return node
}

// propertiesAt walks a path of manifest keys and returns the properties of the
// object it lands on, or nil if the path names nothing.
//
// The path comes from the heading nesting rather than from a search of the
// whole schema, and that is not a detail. Searching for any property with the
// heading's name is ambiguous: "database" is both the top level database block
// and a property of the fidelity requirement, with entirely different keys
// under each. A search would have to guess which table it was enforcing. The
// document already says which, because "## `database`" is a top level key and
// "### `subset`" is a key of whatever "##" it sits under, so the nesting is
// the path and no guessing is required.
func (t *schemaTree) propertiesAt(path []string) map[string]any {
	node := t.root
	for _, key := range path {
		m, ok := t.deref(node).(map[string]any)
		if !ok {
			return nil
		}
		props, ok := m["properties"].(map[string]any)
		if !ok {
			return nil
		}
		target, has := props[key]
		if !has {
			return nil
		}
		node = t.deref(target)
		// A block written once and listed many times, such as services, is an
		// array in the schema and a single table in the reference.
		if am, ok := node.(map[string]any); ok && am["type"] == "array" {
			node = t.deref(am["items"])
		}
	}
	m, ok := t.deref(node).(map[string]any)
	if !ok {
		return nil
	}
	props, _ := m["properties"].(map[string]any)
	return props
}

// valuesFor returns the closed set a property may hold, if it declares one.
func (t *schemaTree) valuesFor(prop any) []string {
	m, ok := t.deref(prop).(map[string]any)
	if !ok {
		return nil
	}
	if vals := literals(m["enum"]); vals != nil {
		return vals
	}
	// An array of a closed set, which is how fidelity.require is declared.
	if items, has := m["items"]; has {
		if im, ok := t.deref(items).(map[string]any); ok {
			return literals(im["enum"])
		}
	}
	return nil
}

// literals renders an enum's members the way the documentation writes them.
// A JSON number arrives as a float64 and "14.000000" matches nothing on the
// page, so an integral value is written without its decimal part.
func literals(node any) []string {
	raw, ok := node.([]any)
	if !ok || len(raw) == 0 {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		switch x := v.(type) {
		case string:
			out = append(out, x)
		case float64:
			out = append(out, strconv.FormatFloat(x, 'f', -1, 64))
		case bool:
			out = append(out, strconv.FormatBool(x))
		default:
			return nil
		}
	}
	return out
}

// CheckTable reports reference table cells that list a closed set short.
//
// Exported so a test can drive it without a file, for the reason prosecheck's
// Check is: the interesting cases are one table row long.
func CheckTable(name, body string, tree *schemaTree) []finding {
	var out []finding
	// stack is the heading nesting, one entry per level below the page title,
	// so that "### `build`" under "## `services`" resolves as services.build.
	//
	// A flat "current section" does not work here and the way it fails is
	// quiet. This page puts a prose heading, "### What a service is given",
	// between "## `services`" and "### `build`". Clearing the section on that
	// prose heading left the later "### `build`" with nothing to nest under,
	// so it resolved as a top level key named build, which does not exist, and
	// the row went unchecked while the tool reported success.
	var stack []string

	for i, line := range strings.Split(body, "\n") {
		if d := depthOf(line); d > 0 {
			at := d - 2
			if at < 0 {
				at = 0
			}
			if at > len(stack) {
				at = len(stack)
			}
			stack = append(stack[:at], keyOf(line))
			continue
		}
		if len(stack) == 0 {
			continue
		}
		path := stack
		m := tableRow.FindStringSubmatch(line)
		if m == nil {
			continue
		}

		props := tree.propertiesAt(path)
		prop, has := props[m[1]]
		if !has {
			continue
		}
		values := tree.valuesFor(prop)
		if len(values) == 0 {
			continue
		}

		// Only the last cell. A Default column holds one value and is not a
		// claim about the set.
		cells := strings.Split(m[2], "|")
		notes := cells[len(cells)-1]

		var named, absent []string
		for _, v := range values {
			if strings.Contains(notes, "`"+v+"`") {
				named = append(named, v)
			} else {
				absent = append(absent, v)
			}
		}
		// A cell that names nothing is describing the key, not listing its
		// values. A cell that names one value without joining it to another is
		// mentioning it, not enumerating.
		if len(named) == 0 || !joined.MatchString(notes) || len(absent) == 0 {
			continue
		}

		out = append(out, finding{
			file: name, line: i + 1,
			why: fmt.Sprintf(
				"%s.%s lists %d of the %d values the schema allows. Missing: %s. "+
					"Name them all, or describe the key without listing values.",
				strings.Join(path, "."), m[1], len(named), len(values), strings.Join(absent, ", ")),
			text: strings.TrimSpace(line),
		})
	}
	return out
}
