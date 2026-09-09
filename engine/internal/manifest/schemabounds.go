package manifest

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// The bounds published in schemas/manifest.v1.json, enforced.
//
// THE FAILURE THIS WAS WRITTEN FOR. That file is not an internal artifact.
// Editors validate against it, it is the document a reader learns the manifest
// from, and tools/schemadoc generates the published reference table out of it,
// so every constraint in it reads to a user as a promise. It declared 570 of
// them and the engine kept 394. Measured behaviourally rather than by reading:
// walk the schema, generate a manifest violating exactly one constraint, feed
// it to the real parser. 168 constraints were refused by nothing at all, and
// excluding the ones the YAML decoder happens to get for free, the engine kept
// 132 of 265. Half the contract was decoration.
//
// The reason nothing caught it is that everything nearby answers a NEARBY
// question. schema_drift_test.go compares field NAMES and says in its own
// comment that it deliberately compares nothing else. tools/manifestcheck
// reads the documentation's manifests for unknown KEYS. tools/fieldsweep asks
// whether a field is READ. validate.go's own comment stated the gap in one
// line and nobody read it as a defect: "manifest.v1.json carries the same
// bounds, but nothing validates a manifest against the JSON Schema at parse
// time."
//
// WHY THIS IS DRIVEN BY THE SCHEMA RATHER THAN REIMPLEMENTING 168 CHECKS.
// A second hand written spelling of a rule is the hazard this repository keeps
// finding in itself: host matching lives in four places, and a constraint
// written twice is two rules that agree until somebody edits one. Reading the
// published document itself means the two cannot disagree by construction,
// rather than by a test noticing afterwards. It costs no dependency: the
// subset of JSON Schema this file uses is closed and small.
//
// WHAT THIS DELIBERATELY DOES NOT DO. It does not check "type" or
// "additionalProperties". Both were measured as already enforced, at 262 of
// 262 and 39 of 39, by the YAML decoder with KnownFields(true), and a second
// opinion on a YAML tag would only find new ways to disagree with the decoder
// about a quoted number. It does not replace a single hand written check
// either: those carry better messages, and they carry the twenty four cross
// field rules the schema cannot express at all, such as a database declaring
// both a source and a seed. Where both would fire, the hand written one wins
// and this one stays quiet, so nobody sees one mistake reported twice.

//go:embed manifest.v1.json
var schemaJSON []byte

// bounds is the part of a JSON Schema this reads.
type bounds struct {
	Ref                  string             `json:"$ref"`
	Defs                 map[string]*bounds `json:"$defs"`
	Properties           map[string]*bounds `json:"properties"`
	Items                *bounds            `json:"items"`
	AdditionalProperties json.RawMessage    `json:"additionalProperties"`
	Required             []string           `json:"required"`
	Enum                 []any              `json:"enum"`
	Pattern              string             `json:"pattern"`
	MinLength            *int               `json:"minLength"`
	MaxLength            *int               `json:"maxLength"`
	MinItems             *int               `json:"minItems"`
	MaxItems             *int               `json:"maxItems"`
	MaxProperties        *int               `json:"maxProperties"`
	UniqueItems          bool               `json:"uniqueItems"`
	Minimum              *float64           `json:"minimum"`
	Maximum              *float64           `json:"maximum"`

	re  *regexp.Regexp
	sub *bounds // additionalProperties when it is a schema rather than false
}

var (
	boundsOnce sync.Once
	boundsRoot *bounds
	boundsErr  error
)

// schemaBounds parses the embedded schema once.
//
// An error here is returned rather than swallowed, because a schema this
// cannot read is a build that ships no bounds at all while looking healthy,
// which is the shape of every check in this repository that could not say no.
// TestTheEmbeddedSchemaLoads is what turns it red.
func schemaBounds() (*bounds, error) {
	boundsOnce.Do(func() {
		var root bounds
		if err := json.Unmarshal(schemaJSON, &root); err != nil {
			boundsErr = fmt.Errorf("manifest: the embedded schema does not parse: %w", err)
			return
		}
		if err := prepare(&root, 0); err != nil {
			boundsErr = err
			return
		}
		boundsRoot = &root
	})
	return boundsRoot, boundsErr
}

// prepare compiles every pattern and decodes every additionalProperties
// schema, so that neither is done per manifest and a bad pattern is a build
// failure rather than a silently skipped check.
func prepare(n *bounds, depth int) error {
	if n == nil || depth > 32 {
		return nil
	}
	if n.Pattern != "" {
		re, err := regexp.Compile(n.Pattern)
		if err != nil {
			return fmt.Errorf("manifest: the embedded schema has an invalid pattern %q: %w", n.Pattern, err)
		}
		n.re = re
	}
	if len(n.AdditionalProperties) > 0 && strings.TrimSpace(string(n.AdditionalProperties)) != "false" {
		var sub bounds
		if err := json.Unmarshal(n.AdditionalProperties, &sub); err == nil {
			n.sub = &sub
		}
	}
	for _, c := range n.Defs {
		if err := prepare(c, depth+1); err != nil {
			return err
		}
	}
	for _, c := range n.Properties {
		if err := prepare(c, depth+1); err != nil {
			return err
		}
	}
	if err := prepare(n.Items, depth+1); err != nil {
		return err
	}
	return prepare(n.sub, depth+1)
}

func (n *bounds) resolve(root *bounds) *bounds {
	for i := 0; n != nil && n.Ref != "" && i < 8; i++ {
		n = root.Defs[strings.TrimPrefix(n.Ref, "#/$defs/")]
	}
	return n
}

// boundsExceptions are the constraints this pass deliberately does not
// enforce, each with the reason. There are six and every one of them is a
// place the PUBLISHED SCHEMA IS WRONG rather than a place the engine falls
// short, which is why enforcing them would refuse manifests this engine has
// always accepted and always should.
//
// They are listed here rather than dropped from the schema because deleting or
// widening a constraint removes or rewrites a row on a published reference
// page, and that is a decision about a document rather than a lint fix. Each
// entry is a proposal with its evidence attached. An empty list is the goal.
//
// TestEverySchemaConstraintIsEnforced reads this same list through
// export_test.go, so there is one list rather than two that agree until
// somebody edits one.
var boundsExceptions = []struct{ Path, Keyword, Why string }{
	// EMPTY, AND THAT IS THE POINT. It held six entries for one afternoon,
	// and every one of them was a place the published schema was wrong rather
	// than a place the engine fell short, which is drift running the opposite
	// direction to the 168 this file was written for. All three were fixed in
	// the schema rather than excused here:
	//
	//   version in the root required list. Parse deliberately assumes version
	//   1 when the key is absent. 37 of the 51 whole manifests in the
	//   published documentation omit it and ZERO of them broke anything else,
	//   so the documentation and the engine agreed with each other and the
	//   schema was alone.
	//
	//   The enum on database.provider and runtime.provider. Providers are a
	//   registry, and a closed enum made a documented extension point
	//   unusable from a manifest: internal/env registers acmedb and acmert and
	//   names them. They are examples now, which is what they always were.
	//
	//   The pattern on runtime.ttl, max_ttl and idle_sleep, which published
	//   hours and days while ParseDuration accepted ms, s, m, h and d and said
	//   so in its own error message. Widened, which refuses nothing that was
	//   valid before.
	//
	// An entry here means the engine publishes a promise it will not keep, so
	// adding one fails TestEverySchemaConstraintIsEnforced until somebody
	// argues for it in a commit message and changes wantExceptions on purpose.
}

// subscript erases array indices so that services[0].env[2].name and the
// exception written as services[].env[].name are the same place.
var subscript = regexp.MustCompile(`\[[0-9]*\]`)

func excepted(path, keyword string) bool {
	path = subscript.ReplaceAllString(path, "")
	for _, e := range boundsExceptions {
		if subscript.ReplaceAllString(e.Path, "") == path && e.Keyword == keyword {
			return true
		}
	}
	return false
}

// boundsPass walks the document against the schema and reports every declared
// constraint it breaks.
//
// It runs last, and reports nothing at a path a hand written check already
// spoke about, so the better message wins and one mistake is reported once.
func (v *validator) boundsPass() {
	root, err := schemaBounds()
	if err != nil || root == nil || v.doc == nil {
		return
	}
	node := v.doc
	if node.Kind == yaml.DocumentNode {
		if len(node.Content) == 0 {
			return
		}
		node = node.Content[0]
	}
	spoken := make(map[string]bool, len(v.problems))
	for _, p := range v.problems {
		if p.Path != "" {
			spoken[p.Path] = true
		}
	}
	v.walkBounds(root, root, node, "", spoken, 0)
}

func (v *validator) boundsAdd(spoken map[string]bool, n *yaml.Node, path, msg, hint string) {
	if spoken[path] {
		return
	}
	spoken[path] = true
	if len(v.problems) >= maxProblems {
		v.suppressed++
		return
	}
	p := Problem{Path: path, Message: msg, Hint: hint}
	if n != nil {
		p.Line, p.Column = n.Line, n.Column
	}
	v.problems = append(v.problems, p)
}

const boundsHint = "The manifest reference lists what each field accepts: https://antifailure.dev/docs/reference/manifest"

func (v *validator) walkBounds(root, n *bounds, node *yaml.Node, path string, spoken map[string]bool, depth int) {
	n = n.resolve(root)
	if n == nil || node == nil || depth > 32 || node.Kind == yaml.AliasNode {
		return
	}
	switch node.Kind {
	case yaml.MappingNode:
		v.boundsMapping(root, n, node, path, spoken, depth)
	case yaml.SequenceNode:
		v.boundsSequence(root, n, node, path, spoken, depth)
	case yaml.ScalarNode:
		v.boundsScalar(n, node, path, spoken)
	}
}

func (v *validator) boundsMapping(root, n *bounds, node *yaml.Node, path string, spoken map[string]bool, depth int) {
	keys := make(map[string]bool, len(node.Content)/2)
	for i := 0; i+1 < len(node.Content); i += 2 {
		keys[node.Content[i].Value] = true
	}
	for _, want := range n.Required {
		if keys[want] || excepted(path, "required="+want) {
			continue
		}
		v.boundsAdd(spoken, node, join(path, want),
			fmt.Sprintf("%s is required and is not set.", want), boundsHint)
	}
	if n.MaxProperties != nil && len(keys) > *n.MaxProperties {
		v.boundsAdd(spoken, node, path,
			fmt.Sprintf("There are %d entries here, and the most allowed is %d.", len(keys), *n.MaxProperties),
			boundsHint)
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key, val := node.Content[i].Value, node.Content[i+1]
		child := n.Properties[key]
		if child == nil {
			child = n.sub
		}
		if child == nil {
			continue
		}
		v.walkBounds(root, child, val, join(path, key), spoken, depth+1)
	}
}

func (v *validator) boundsSequence(root, n *bounds, node *yaml.Node, path string, spoken map[string]bool, depth int) {
	if n.MinItems != nil && len(node.Content) < *n.MinItems {
		v.boundsAdd(spoken, node, path,
			fmt.Sprintf("There are %d entries here, and the fewest allowed is %d.", len(node.Content), *n.MinItems),
			boundsHint)
	}
	if n.MaxItems != nil && len(node.Content) > *n.MaxItems {
		v.boundsAdd(spoken, node, path,
			fmt.Sprintf("There are %d entries here, and the most allowed is %d.", len(node.Content), *n.MaxItems),
			boundsHint)
	}
	if n.UniqueItems {
		seen := make(map[string]int, len(node.Content))
		for i, item := range node.Content {
			k := itemKey(item)
			if first, ok := seen[k]; ok {
				v.boundsAdd(spoken, item, fmt.Sprintf("%s[%d]", path, i),
					fmt.Sprintf("This repeats entry %d. Every entry here must be different.", first),
					boundsHint)
				continue
			}
			seen[k] = i
		}
	}
	if n.Items == nil {
		return
	}
	for i, item := range node.Content {
		v.walkBounds(root, n.Items, item, fmt.Sprintf("%s[%d]", path, i), spoken, depth+1)
	}
}

func (v *validator) boundsScalar(n *bounds, node *yaml.Node, path string, spoken map[string]bool) {
	if node.Tag == "!!null" {
		return
	}
	value := node.Value
	if len(n.Enum) > 0 && !inEnum(n.Enum, value) && !excepted(path, "enum") {
		v.boundsAdd(spoken, node, path,
			fmt.Sprintf("%q is not one of %s.", value, enumList(n.Enum)), boundsHint)
		return
	}
	if node.Tag == "!!str" {
		if n.re != nil && !n.re.MatchString(value) && !excepted(path, "pattern") {
			v.boundsAdd(spoken, node, path,
				fmt.Sprintf("%q is not in the form this field takes, %s.", value, n.Pattern), boundsHint)
			return
		}
		if n.MinLength != nil && len(value) < *n.MinLength {
			v.boundsAdd(spoken, node, path,
				fmt.Sprintf("This is %d characters and the shortest allowed is %d.", len(value), *n.MinLength),
				boundsHint)
			return
		}
		if n.MaxLength != nil && len(value) > *n.MaxLength {
			v.boundsAdd(spoken, node, path,
				fmt.Sprintf("This is %d characters and the longest allowed is %d.", len(value), *n.MaxLength),
				boundsHint)
			return
		}
	}
	if n.Minimum == nil && n.Maximum == nil {
		return
	}
	num, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return
	}
	if n.Minimum != nil && num < *n.Minimum {
		v.boundsAdd(spoken, node, path,
			fmt.Sprintf("%s is below %s, the smallest value this takes.", value, number(*n.Minimum)), boundsHint)
		return
	}
	if n.Maximum != nil && num > *n.Maximum {
		v.boundsAdd(spoken, node, path,
			fmt.Sprintf("%s is above %s, the largest value this takes.", value, number(*n.Maximum)), boundsHint)
	}
}

// itemKey identifies a sequence entry for the uniqueness check. Scalars
// compare by value; anything structured compares by its rendered form, which
// is enough for the one uniqueItems in this schema and is stable.
func itemKey(n *yaml.Node) string {
	if n.Kind == yaml.ScalarNode {
		return n.Tag + "\x00" + n.Value
	}
	raw, err := yaml.Marshal(n)
	if err != nil {
		return fmt.Sprintf("%p", n)
	}
	return string(raw)
}

func inEnum(values []any, got string) bool {
	for _, want := range values {
		if scalarString(want) == got {
			return true
		}
	}
	return false
}

func enumList(values []any) string {
	out := make([]string, 0, len(values))
	for _, e := range values {
		out = append(out, scalarString(e))
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

// scalarString renders a JSON enum member the way YAML would write it, so that
// 1 matches 1 and not 1.000000.
func scalarString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return number(t)
	case bool:
		return strconv.FormatBool(t)
	case nil:
		return "null"
	}
	return fmt.Sprint(v)
}

func number(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}
