package manifest_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/manifest"
)

// A constraint declared in schemas/manifest.v1.json, located at the place in a
// manifest where it applies.
type constraint struct {
	Path    string // instance path, dotted, [] for the first array element
	Keyword string
	Detail  string
}

func (c constraint) id() string {
	if c.Detail == "" {
		return c.Path + " " + c.Keyword
	}
	return c.Path + " " + c.Keyword + "=" + c.Detail
}

// cnode is the part of a JSON Schema this walks. Every keyword the manifest
// schema actually uses has a field here; anything it grows later that is not
// listed is invisible, which is why TestSchemaConstraintInventoryIsComplete
// compares this walk against a raw count of the file.
type cnode struct {
	Ref                  string            `json:"$ref"`
	Defs                 map[string]*cnode `json:"$defs"`
	Type                 any               `json:"type"`
	Properties           map[string]*cnode `json:"properties"`
	Items                *cnode            `json:"items"`
	AdditionalProperties json.RawMessage   `json:"additionalProperties"`
	Required             []string          `json:"required"`
	Enum                 []any             `json:"enum"`
	Pattern              string            `json:"pattern"`
	MinLength            *int              `json:"minLength"`
	MaxLength            *int              `json:"maxLength"`
	MinItems             *int              `json:"minItems"`
	MaxItems             *int              `json:"maxItems"`
	MaxProperties        *int              `json:"maxProperties"`
	UniqueItems          *bool             `json:"uniqueItems"`
	Minimum              *float64          `json:"minimum"`
	Maximum              *float64          `json:"maximum"`
}

func (n *cnode) resolve(root *cnode) *cnode {
	for n != nil && n.Ref != "" {
		n = root.Defs[strings.TrimPrefix(n.Ref, "#/$defs/")]
	}
	return n
}

func (n *cnode) typeIs(want string) bool {
	switch t := n.Type.(type) {
	case string:
		return t == want
	case []any:
		for _, e := range t {
			if s, ok := e.(string); ok && s == want {
				return true
			}
		}
	}
	return false
}

// apSchema returns the schema additionalProperties names, when it is a schema
// rather than the boolean false.
func (n *cnode) apSchema() *cnode {
	if len(n.AdditionalProperties) == 0 {
		return nil
	}
	var sub cnode
	if err := json.Unmarshal(n.AdditionalProperties, &sub); err != nil {
		return nil
	}
	return &sub
}

func (n *cnode) apIsFalse() bool {
	return strings.TrimSpace(string(n.AdditionalProperties)) == "false"
}

func loadConstraintSchema(t *testing.T) *cnode {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "schemas", "manifest.v1.json"))
	require.NoError(t, err)
	var root cnode
	require.NoError(t, json.Unmarshal(raw, &root))
	return &root
}

// enumerate walks the schema from the root and returns every constraint it
// declares, each located at the instance path where a manifest would violate
// it. Order is deterministic so the inventory is diffable.
func enumerate(root *cnode) []constraint {
	var out []constraint
	var walk func(n *cnode, path string, depth int)
	walk = func(n *cnode, path string, depth int) {
		n = n.resolve(root)
		if n == nil || depth > 24 {
			return
		}
		if n.Type != nil {
			out = append(out, constraint{Path: path, Keyword: "type"})
		}
		if n.Pattern != "" {
			out = append(out, constraint{Path: path, Keyword: "pattern"})
		}
		if len(n.Enum) > 0 {
			out = append(out, constraint{Path: path, Keyword: "enum"})
		}
		if n.MinLength != nil {
			out = append(out, constraint{Path: path, Keyword: "minLength"})
		}
		if n.MaxLength != nil {
			out = append(out, constraint{Path: path, Keyword: "maxLength"})
		}
		if n.Minimum != nil {
			out = append(out, constraint{Path: path, Keyword: "minimum"})
		}
		if n.Maximum != nil {
			out = append(out, constraint{Path: path, Keyword: "maximum"})
		}
		if n.MinItems != nil {
			out = append(out, constraint{Path: path, Keyword: "minItems"})
		}
		if n.MaxItems != nil {
			out = append(out, constraint{Path: path, Keyword: "maxItems"})
		}
		if n.MaxProperties != nil {
			out = append(out, constraint{Path: path, Keyword: "maxProperties"})
		}
		if n.UniqueItems != nil && *n.UniqueItems {
			out = append(out, constraint{Path: path, Keyword: "uniqueItems"})
		}
		for _, r := range n.Required {
			out = append(out, constraint{Path: path, Keyword: "required", Detail: r})
		}
		if n.apIsFalse() {
			out = append(out, constraint{Path: path, Keyword: "additionalProperties"})
		}
		names := make([]string, 0, len(n.Properties))
		for name := range n.Properties {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			child := path + "." + name
			if path == "" {
				child = name
			}
			walk(n.Properties[name], child, depth+1)
		}
		if sub := n.apSchema(); sub != nil {
			child := path + ".*"
			if path == "" {
				child = "*"
			}
			walk(sub, child, depth+1)
		}
		if n.Items != nil {
			walk(n.Items, path+"[]", depth+1)
		}
	}
	walk(root, "", 0)
	return out
}

// ---------------------------------------------------------------------------
// Instance generation.
//
// A base manifest is generated from the schema itself: every property present,
// every value chosen to satisfy the constraint declared on it. Then one place
// is mutated per constraint, producing a document the published schema rejects
// and that differs from the base in exactly one place.
// ---------------------------------------------------------------------------

// stringCandidates is tried in order. The first that matches the property's
// pattern and length bounds is used, so nothing here is mapped to a field by
// hand: a new pattern in the schema either matches one of these or the
// generator refuses to guess and the cell is reported as unmeasured.
var stringCandidates = []string{
	"web", "web-one", "web_one", "Web_One1", "a",
	"1h", "30s", "500ms", "10m", "2d", "1", "60", "1.5m", "512Mi", "10/s",
	"web.main", "an example sentence that is long enough",
}

var badString = "!! not a valid value !!"

func matches(pattern, s string) bool {
	if pattern == "" {
		return true
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return false
	}
	return re.MatchString(s)
}

// sampleString picks a value satisfying the declared pattern and lengths.
func sampleString(n *cnode) (string, bool) {
	if len(n.Enum) > 0 {
		if s, ok := n.Enum[0].(string); ok {
			return s, true
		}
	}
	for _, c := range stringCandidates {
		if !matches(n.Pattern, c) {
			continue
		}
		if n.MinLength != nil && len(c) < *n.MinLength {
			continue
		}
		if n.MaxLength != nil && len(c) > *n.MaxLength {
			continue
		}
		return c, true
	}
	return "", false
}

// generator builds instances and records the paths it could not fill.
type generator struct {
	root      *cnode
	overrides map[string]any
	unfilled  []string
}

func (g *generator) sample(n *cnode, path string, depth int) any {
	n = n.resolve(g.root)
	if n == nil || depth > 24 {
		return nil
	}
	if v, ok := g.overrides[path]; ok {
		return v
	}
	switch {
	case n.typeIs("object"):
		obj := map[string]any{}
		names := make([]string, 0, len(n.Properties))
		for name := range n.Properties {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			child := path + "." + name
			if path == "" {
				child = name
			}
			if v := g.sample(n.Properties[name], child, depth+1); v != nil {
				obj[name] = v
			}
		}
		if sub := g.apSchemaOf(n); sub != nil {
			child := path + ".*"
			if path == "" {
				child = "*"
			}
			if v := g.sample(sub, child, depth+1); v != nil {
				obj[mapKeyFor(path)] = v
			}
		}
		return obj
	case n.typeIs("array"):
		if n.Items == nil {
			return []any{}
		}
		v := g.sample(n.Items, path+"[]", depth+1)
		if v == nil {
			return []any{}
		}
		return []any{v}
	case n.typeIs("string"):
		s, ok := sampleString(n)
		if !ok {
			g.unfilled = append(g.unfilled, path)
			return nil
		}
		return s
	case n.typeIs("integer"), n.typeIs("number"):
		if len(n.Enum) > 0 {
			return n.Enum[0]
		}
		v := 1.0
		if n.Minimum != nil && *n.Minimum > v {
			v = *n.Minimum
		}
		if n.Maximum != nil && *n.Maximum < v {
			v = *n.Maximum
		}
		if n.typeIs("integer") {
			return int(v)
		}
		return v
	case n.typeIs("boolean"):
		return true
	}
	g.unfilled = append(g.unfilled, path)
	return nil
}

func (g *generator) apSchemaOf(n *cnode) *cnode {
	return n.apSchema()
}

// mapKeyFor names the single key generated for a free form map.
func mapKeyFor(path string) string {
	switch path {
	case "personas[].attributes", "auth.attribute_columns":
		return "team"
	}
	return "sample_key"
}

// ---------------------------------------------------------------------------
// Path navigation over the generated instance.
// ---------------------------------------------------------------------------

// walkTo returns the container holding the last path element, and that element
// as a key or index.
func walkTo(doc any, path string) (any, string, bool) {
	segs := strings.Split(path, ".")
	cur := doc
	for i, seg := range segs {
		last := i == len(segs)-1
		arrays := 0
		for strings.HasSuffix(seg, "[]") {
			seg = strings.TrimSuffix(seg, "[]")
			arrays++
		}
		if last && arrays == 0 {
			return cur, soleKey(cur, seg), true
		}
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, "", false
		}
		next, ok := m[soleKey(cur, seg)]
		if !ok {
			return nil, "", false
		}
		for a := 0; a < arrays; a++ {
			arr, ok := next.([]any)
			if !ok || len(arr) == 0 {
				return nil, "", false
			}
			if last && a == arrays-1 {
				return arr, "0", true
			}
			next = arr[0]
		}
		cur = next
	}
	return nil, "", false
}

// soleKey turns the free form map wildcard into the key the generator wrote.
func soleKey(container any, seg string) string {
	if seg != "*" {
		return seg
	}
	m, ok := container.(map[string]any)
	if !ok {
		return seg
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return seg
	}
	return keys[0]
}

func valueAt(doc any, path string) (any, bool) {
	if path == "" {
		return doc, true
	}
	holder, key, ok := walkTo(doc, path)
	if !ok {
		return nil, false
	}
	switch h := holder.(type) {
	case map[string]any:
		v, ok := h[key]
		return v, ok
	case []any:
		if len(h) == 0 {
			return nil, false
		}
		return h[0], true
	}
	return nil, false
}

func setAt(doc any, path string, value any) bool {
	if path == "" {
		return false
	}
	holder, key, ok := walkTo(doc, path)
	if !ok {
		return false
	}
	switch h := holder.(type) {
	case map[string]any:
		h[key] = value
		return true
	case []any:
		if len(h) == 0 {
			return false
		}
		h[0] = value
		return true
	}
	return false
}

func deleteAt(doc any, path string) bool {
	holder, key, ok := walkTo(doc, path)
	if !ok {
		return false
	}
	m, ok := holder.(map[string]any)
	if !ok {
		return false
	}
	if _, present := m[key]; !present {
		return false
	}
	delete(m, key)
	return true
}

func deepCopy(v any) any {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

// violate returns a copy of base that breaks exactly the one constraint, and
// reports whether it could construct one.
func violate(root *cnode, base any, c constraint) (any, bool) {
	doc := deepCopy(base)
	if doc == nil {
		return nil, false
	}
	node := nodeAtPath(root, c.Path)
	if node == nil {
		return nil, false
	}
	switch c.Keyword {
	case "required":
		p := c.Detail
		if c.Path != "" {
			p = c.Path + "." + c.Detail
		}
		return doc, deleteAt(doc, p)
	case "additionalProperties":
		v, ok := valueAt(doc, c.Path)
		if !ok {
			return nil, false
		}
		m, ok := v.(map[string]any)
		if !ok {
			return nil, false
		}
		m["af_not_a_declared_property"] = "x"
		return doc, true
	case "maxProperties":
		v, ok := valueAt(doc, c.Path)
		if !ok {
			return nil, false
		}
		m, ok := v.(map[string]any)
		if !ok {
			return nil, false
		}
		for i := 0; i <= *node.MaxProperties; i++ {
			m[fmt.Sprintf("k%d", i)] = "v"
		}
		return doc, true
	case "type":
		if c.Path == "" {
			return "af: a manifest that is not an object", true
		}
		var wrong any = map[string]any{"af": "wrong type"}
		if node.typeIs("object") {
			wrong = "af not an object"
		}
		return doc, setAt(doc, c.Path, wrong)
	case "enum":
		var wrong any = badString
		if _, isNum := node.Enum[0].(float64); isNum {
			wrong = 999999
		}
		return doc, setAt(doc, c.Path, wrong)
	case "pattern":
		return doc, setAt(doc, c.Path, badString)
	case "minLength":
		return doc, setAt(doc, c.Path, strings.Repeat("a", atLeastZero(*node.MinLength-1)))
	case "maxLength":
		seed, ok := sampleString(node)
		if !ok || seed == "" {
			seed = "a"
		}
		s := strings.Repeat(seed, (*node.MaxLength/len(seed) + 2))
		return doc, setAt(doc, c.Path, s[:*node.MaxLength+1])
	case "minimum":
		return doc, setAt(doc, c.Path, *node.Minimum-1)
	case "maximum":
		return doc, setAt(doc, c.Path, *node.Maximum+1)
	case "minItems":
		return doc, setAt(doc, c.Path, []any{})
	case "maxItems":
		v, ok := valueAt(doc, c.Path)
		if !ok {
			return nil, false
		}
		arr, ok := v.([]any)
		if !ok || len(arr) == 0 {
			return nil, false
		}
		out := make([]any, 0, *node.MaxItems+1)
		for i := 0; i <= *node.MaxItems; i++ {
			out = append(out, uniquify(deepCopy(arr[0]), i))
		}
		return doc, setAt(doc, c.Path, out)
	case "uniqueItems":
		v, ok := valueAt(doc, c.Path)
		if !ok {
			return nil, false
		}
		arr, ok := v.([]any)
		if !ok || len(arr) == 0 {
			return nil, false
		}
		return doc, setAt(doc, c.Path, []any{arr[0], deepCopy(arr[0])})
	}
	return nil, false
}

// uniquify makes the nth copy of an array element distinct, so that a maxItems
// violation is not also a duplicate name violation.
func uniquify(v any, i int) any {
	m, ok := v.(map[string]any)
	if !ok {
		if s, ok := v.(string); ok && i > 0 {
			return fmt.Sprintf("%s-%d", s, i)
		}
		return v
	}
	for _, key := range []string{"name", "id", "goal", "host", "sql", "path"} {
		if s, ok := m[key].(string); ok && i > 0 {
			m[key] = fmt.Sprintf("%s-%d", s, i)
		}
	}
	return m
}

// nodeAtPath resolves an instance path back to the schema node that declares
// the constraints on it.
func nodeAtPath(root *cnode, path string) *cnode {
	n := root.resolve(root)
	if path == "" {
		return n
	}
	for _, seg := range strings.Split(path, ".") {
		arrays := 0
		for strings.HasSuffix(seg, "[]") {
			seg = strings.TrimSuffix(seg, "[]")
			arrays++
		}
		if n == nil {
			return nil
		}
		var next *cnode
		if seg == "*" {
			next = n.apSchema()
		} else {
			next = n.Properties[seg]
		}
		if next == nil {
			return nil
		}
		next = next.resolve(root)
		for a := 0; a < arrays; a++ {
			if next == nil || next.Items == nil {
				return nil
			}
			next = next.Items.resolve(root)
		}
		n = next
	}
	return n
}

func atLeastZero(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

// baseOverrides supplies a value for a path the generator cannot infer from
// the schema alone, because the engine validates it beyond what the schema
// says: a cron expression, a read only SQL statement, a host pattern. Every
// entry here is a place the published schema is weaker than the engine, which
// is the opposite of the drift this file measures and is therefore fine.
var baseOverrides = map[string]any{}

// tuning is read from the file named by AF_SCHEMA_TUNING, so that the base
// manifests can be adjusted without recompiling. The build lock on this
// machine had a thirty deep queue, and a compiled test binary plus a data file
// is the difference between one hold and twenty.
//
// There is more than one base because the manifest has mutually exclusive
// fields: a database is built from a source or from a seed and never both, an
// egress rule in block mode may not carry a credential, a web service may not
// carry a cron schedule. A single maximal document is refused by twenty four
// of the engine's own rules, so each constraint is measured in the first base
// that both contains it and the engine accepts.
type baseSpec struct {
	Name      string           `json:"name"`
	Why       string           `json:"why"`
	Overrides map[string]any   `json:"overrides"`
	Prune     []string         `json:"prune"`
	Append    map[string][]any `json:"append"`
}

type tuning struct {
	Bases []baseSpec `json:"bases"`
	// RefusedFields are paths the engine refuses whatever value they carry,
	// because the field is in the schema and nothing reads it. A constraint
	// under one of these is reported separately rather than counted as
	// enforced, because refusing the field is not the same as keeping the
	// promise the constraint makes.
	RefusedFields map[string]string `json:"refused_fields"`
}

func loadTuning() tuning {
	var tn tuning
	path := os.Getenv("AF_SCHEMA_TUNING")
	if path == "" {
		tn.Bases = []baseSpec{{Name: "generated"}}
		return tn
	}
	raw, err := os.ReadFile(path) //nolint:gosec // a developer named this file
	if err != nil {
		panic("AF_SCHEMA_TUNING: " + err.Error())
	}
	if err := json.Unmarshal(raw, &tn); err != nil {
		panic("AF_SCHEMA_TUNING: " + err.Error())
	}
	if len(tn.Bases) == 0 {
		tn.Bases = []baseSpec{{Name: "generated"}}
	}
	return tn
}

// builtBase is one generated document and what the engine said about it.
type builtBase struct {
	spec     baseSpec
	doc      any
	err      string
	unfilled []string
}

func buildBases(root *cnode, tn tuning) []builtBase {
	out := make([]builtBase, 0, len(tn.Bases))
	for _, spec := range tn.Bases {
		overrides := map[string]any{}
		for k, v := range baseOverrides {
			overrides[k] = v
		}
		for k, v := range spec.Overrides {
			overrides[k] = v
		}
		g := &generator{root: root, overrides: overrides}
		doc := g.sample(root, "", 0)
		for _, p := range spec.Prune {
			deleteAt(doc, p)
		}
		for path, extra := range spec.Append {
			if v, ok := valueAt(doc, path); ok {
				if arr, ok := v.([]any); ok {
					setAt(doc, path, append(arr, extra...))
				}
			}
		}
		out = append(out, builtBase{spec: spec, doc: doc, err: parseErr(doc), unfilled: g.unfilled})
	}
	return out
}

// present reports whether the constraint has somewhere to be violated in doc.
func present(doc any, c constraint) bool {
	if c.Path == "" {
		return true
	}
	if c.Keyword == "required" {
		_, ok := valueAt(doc, c.Path)
		return ok
	}
	_, ok := valueAt(doc, c.Path)
	return ok
}

type verdict struct {
	c      constraint
	status string
	reason string
	base   string
	engine string
}

const (
	statusEnforced = "ENFORCED"
	statusNot      = "NOT-ENFORCED"
	statusRefused  = "REFUSED-FIELD"
	statusNoLook   = "COULD-NOT-LOOK"
)

func measure(t *testing.T) ([]verdict, []builtBase) {
	t.Helper()
	root := loadConstraintSchema(t)
	tn := loadTuning()
	bases := buildBases(root, tn)

	dumpDir := os.Getenv("AF_SCHEMA_DUMP")
	if dumpDir != "" {
		_ = os.MkdirAll(dumpDir, 0o750)
		for i, b := range bases {
			raw, _ := json.MarshalIndent(b.doc, "", "  ")
			_ = os.WriteFile(filepath.Join(dumpDir, fmt.Sprintf("base_%d_%s.json", i, b.spec.Name)), raw, 0o600)
		}
	}

	var out []verdict
	for _, c := range enumerate(root) {
		v := verdict{c: c}
		if why, ok := refusedField(tn.RefusedFields, c.Path); ok {
			v.status = statusRefused
			v.reason = why
			out = append(out, v)
			continue
		}
		measured := false
		var reasons []string
		for _, b := range bases {
			if b.err != "" {
				reasons = append(reasons, b.spec.Name+": base refused")
				continue
			}
			if !present(b.doc, c) {
				reasons = append(reasons, b.spec.Name+": absent")
				continue
			}
			bad, ok := violate(root, b.doc, c)
			if !ok {
				reasons = append(reasons, b.spec.Name+": no violating document")
				continue
			}
			if dumpDir != "" {
				raw, _ := json.MarshalIndent(bad, "", "  ")
				name := strings.NewReplacer("/", "_", " ", "_", "*", "star", "[", "", "]", "").Replace(c.id())
				_ = os.WriteFile(filepath.Join(dumpDir, "bad__"+name+".json"), raw, 0o600)
			}
			e := parseErr(bad)
			v.base = b.spec.Name
			if e == "" {
				v.status = statusNot
			} else {
				v.status = statusEnforced
				v.engine = firstProblem(e)
			}
			measured = true
			break
		}
		if !measured {
			v.status = statusNoLook
			v.reason = strings.Join(reasons, "; ")
		}
		out = append(out, v)
	}
	return out, bases
}

// refusedField reports whether path is, or is under, a field the engine
// refuses outright.
func refusedField(refused map[string]string, path string) (string, bool) {
	for p, why := range refused {
		if path == p || strings.HasPrefix(path, p+".") || strings.HasPrefix(path, p+"[") {
			return why, true
		}
	}
	return "", false
}

func firstProblem(e string) string {
	for _, l := range strings.Split(e, "\n") {
		l = strings.TrimSpace(l)
		if l != "" && !strings.HasPrefix(l, "antifailure.yaml") {
			return l
		}
	}
	return strings.TrimSpace(e)
}

func parseErr(doc any) string {
	raw, err := json.Marshal(doc)
	if err != nil {
		return "marshal: " + err.Error()
	}
	if _, err := manifest.Parse(raw, "antifailure.yaml", ""); err != nil {
		return err.Error()
	}
	return ""
}

// TestSchemaConstraintReport prints the measurement. It asserts nothing; the
// gate is TestEverySchemaConstraintIsEnforced.
func TestSchemaConstraintReport(t *testing.T) {
	if os.Getenv("AF_SCHEMA_REPORT") == "" {
		t.Skip("set AF_SCHEMA_REPORT=1 for the constraint report")
	}
	verdicts, bases := measure(t)
	for _, b := range bases {
		t.Logf("base %-10s accepted=%v unfilled=%v", b.spec.Name, b.err == "", b.unfilled)
		if b.err != "" {
			t.Logf("  %s", b.err)
		}
	}
	counts := map[string]int{}
	byKeyword := map[string]map[string]int{}
	for _, v := range verdicts {
		counts[v.status]++
		if byKeyword[v.c.Keyword] == nil {
			byKeyword[v.c.Keyword] = map[string]int{}
		}
		byKeyword[v.c.Keyword][v.status]++
		if v.status != statusEnforced {
			t.Logf("%-14s %-58s %s%s", v.status, v.c.id(), v.reason, v.base)
		}
	}
	t.Logf("TOTAL %d  %v", len(verdicts), counts)
	keys := make([]string, 0, len(byKeyword))
	for k := range byKeyword {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		t.Logf("  %-22s %v", k, byKeyword[k])
	}
	if out := os.Getenv("AF_SCHEMA_JSON"); out != "" {
		type row struct{ Path, Keyword, Detail, Status, Reason, Base, Engine string }
		rows := make([]row, 0, len(verdicts))
		for _, v := range verdicts {
			rows = append(rows, row{v.c.Path, v.c.Keyword, v.c.Detail, v.status, v.reason, v.base, v.engine})
		}
		raw, _ := json.MarshalIndent(rows, "", " ")
		require.NoError(t, os.WriteFile(out, raw, 0o600))
	}
}
