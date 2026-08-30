// Package manifest loads, validates, normalizes, and explains antifailure.yaml.
//
// Three properties matter more than anything else here.
//
// Unknown keys are errors, not warnings. A typo in a manifest is the failure
// mode this product exists to prevent: a silently ignored key means an
// environment that is subtly not what the user asked for, and the user finds
// out in production. Every rejection names the line.
//
// Errors are collected, not returned one at a time. Fixing a manifest by
// running the command eight times, each showing the next problem, is a bad
// experience that a validator can trivially avoid.
//
// Normalization happens exactly once, at load. Every later package reads a
// manifest whose defaults are filled in and whose paths are cleaned and
// confined, so no downstream code has to remember what the default health path
// was.
package manifest

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// FileName is the manifest's name at the repository root.
const FileName = "antifailure.yaml"

// MaxSize bounds the manifest. A manifest describes services; it does not
// contain them. The limit is what stops a hostile or generated file from
// turning parsing into a memory problem.
const MaxSize = 1 << 20 // 1 MiB

// maxDepth bounds YAML nesting. Combined with alias expansion being disabled,
// it closes the billion laughs class of attack.
const maxDepth = 32

// maxProblems bounds how many problems are reported from one manifest.
//
// It is a performance bound as much as a usability one. Locating a problem
// walks the document to find the line, so reporting every problem in a file
// with ten thousand bad keys is quadratic. It is also useless: nobody reads
// the two hundredth message, and the first twenty are what get fixed.
const maxProblems = 40

// maxNodes bounds how many YAML nodes the structural checks walk.
//
// A generous real manifest, with fifty services, five hundred egress rules,
// and two hundred workflows, comes to roughly twenty thousand nodes. This is
// more than twice that, and well inside the one mebibyte size limit, so it is
// reachable by a hostile document and unreachable by a real one.
const maxNodes = 50000

// Problem is one validation failure, located in the file.
type Problem struct {
	// Line and Column are one based, and zero when the problem is not
	// attributable to a node, for example a missing required section.
	Line, Column int
	// Path is the dotted location, for example services[1].port.
	Path string
	// Message says what is wrong, in the second person.
	Message string
	// Hint says what to do about it, when there is something specific to say.
	Hint string
}

func (p Problem) String() string {
	var b strings.Builder
	if p.Line > 0 {
		fmt.Fprintf(&b, "line %d", p.Line)
		if p.Column > 0 {
			fmt.Fprintf(&b, ", column %d", p.Column)
		}
		b.WriteString(": ")
	}
	if p.Path != "" {
		b.WriteString(p.Path)
		b.WriteString(": ")
	}
	b.WriteString(p.Message)
	if p.Hint != "" {
		b.WriteString(" ")
		b.WriteString(p.Hint)
	}
	return b.String()
}

// Errors is the set of problems found in one manifest.
type Errors struct {
	Path     string
	Problems []Problem
}

// Unwrap gives the problem list an error code.
//
// Without it, a typo in somebody's manifest is reported as AF-GEN-000, whose
// next step is "this is an unclassified failure, please report it". Telling a
// user to file a bug for their own configuration mistake is worse than saying
// nothing, and it is exactly the failure the error catalog exists to prevent.
func (e *Errors) Unwrap() error {
	return aferrors.Coded(aferrors.AFMAN002, "path", e.Path, "detail", e.detail())
}

func (e *Errors) detail() string {
	switch len(e.Problems) {
	case 0:
		return "no problem was recorded, which is itself a bug"
	case 1:
		return e.Problems[0].String()
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d problems.", len(e.Problems))
	for _, p := range e.Problems {
		b.WriteString("\n  ")
		b.WriteString(p.String())
	}
	return b.String()
}

func (e *Errors) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s is not valid (%d problem", e.Path, len(e.Problems))
	if len(e.Problems) != 1 {
		b.WriteString("s")
	}
	b.WriteString("):")
	for _, p := range e.Problems {
		b.WriteString("\n  ")
		b.WriteString(p.String())
	}
	return b.String()
}

// Find walks up from dir looking for a manifest, and returns its path.
//
// Walking up rather than requiring the repository root means a command works
// from anywhere inside the repository, which is where people actually run it.
func Find(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("manifest: resolve %s: %w", dir, err)
	}
	cur := abs
	for {
		candidate := filepath.Join(cur, FileName)
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate, nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", aferrors.Coded(aferrors.AFMAN001, "path", abs)
		}
		cur = parent
	}
}

// Load reads, validates, and normalizes the manifest at path.
//
// The returned manifest has every default applied and every path cleaned and
// confined to the repository, so callers never repeat that work.
func Load(path string) (*schema.Manifest, error) {
	st, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, aferrors.Coded(aferrors.AFMAN001, "path", filepath.Dir(path))
		}
		return nil, fmt.Errorf("manifest: stat %s: %w", path, err)
	}
	if st.Size() > MaxSize {
		return nil, aferrors.Coded(aferrors.AFMAN005,
			"path", path, "limit", humanBytes(MaxSize))
	}
	data, err := os.ReadFile(path) //nolint:gosec // the path is chosen by the user, which is the point
	if err != nil {
		return nil, fmt.Errorf("manifest: read %s: %w", path, err)
	}
	return Parse(data, path, filepath.Dir(path))
}

// Parse validates and normalizes manifest bytes.
//
// root is the repository root that paths are confined to. Passing an empty
// root skips confinement, which only the schema example tests do.
func Parse(data []byte, displayPath, root string) (*schema.Manifest, error) {
	if len(data) > MaxSize {
		return nil, aferrors.Coded(aferrors.AFMAN005,
			"path", displayPath, "limit", humanBytes(MaxSize))
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, aferrors.Coded(aferrors.AFMAN002,
			"path", displayPath, "detail", cleanYAMLError(err))
	}
	if doc.Kind == 0 {
		return nil, &Errors{Path: displayPath, Problems: []Problem{{
			Message: "The manifest is empty.",
			Hint:    "Run 'af init' to generate one from your repository.",
		}}}
	}

	// Aliases are rejected rather than expanded. A manifest has no legitimate
	// need for them, and expansion is how a small document becomes a large one.
	// The structural walk is bounded so that a hostile document cannot make
	// validation itself the denial of service.
	budget := maxNodes
	aliasProblem := findAlias(&doc, "", &budget)
	if budget <= 0 {
		// The scan gave up before finishing, so it cannot say there are no
		// aliases. The decoder below expands them, so passing an unscanned
		// document to it would defeat the check entirely. A document this
		// large is not a manifest, so refusing is both safe and correct.
		return nil, &Errors{Path: displayPath, Problems: []Problem{{
			Message: fmt.Sprintf("The manifest has more than %d nodes.", maxNodes),
			Hint:    "A manifest describes services; it does not contain them. This is almost always a generated or checked in file that does not belong here.",
		}}}
	}
	if aliasProblem != nil {
		return nil, &Errors{Path: displayPath, Problems: []Problem{*aliasProblem}}
	}
	budget = maxNodes
	if p := checkDepth(&doc, "", 0, &budget); p != nil {
		return nil, &Errors{Path: displayPath, Problems: []Problem{*p}}
	}

	// The version is read before anything else, so that a manifest from a
	// newer build is rejected with an upgrade instruction rather than with a
	// list of unknown keys.
	version, versionNode, present := readVersion(&doc)
	switch {
	case !present:
		// A manifest with no version is assumed to be version 1. Refusing
		// would be pedantic for a field that has only ever had one value, and
		// the assumption is recorded as a warning by the caller.
		version = schema.ManifestVersion
	case version > schema.ManifestVersion:
		return nil, aferrors.Coded(aferrors.AFMAN003,
			"path", displayPath, "found", strconv.Itoa(version))
	case version < 1:
		return nil, &Errors{Path: displayPath, Problems: []Problem{{
			Line: line(versionNode), Column: col(versionNode), Path: "version",
			Message: fmt.Sprintf("The schema version must be %d.", schema.ManifestVersion),
		}}}
	}

	var m schema.Manifest
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		problems := unknownFieldProblems(err, &doc)
		if len(problems) == 0 {
			problems = []Problem{{Message: cleanYAMLError(err)}}
		}
		return nil, &Errors{Path: displayPath, Problems: problems}
	}
	m.Version = version

	normalize(&m, root)
	if problems := validate(&m, &doc, root); len(problems) > 0 {
		sort.SliceStable(problems, func(i, j int) bool {
			if problems[i].Line != problems[j].Line {
				return problems[i].Line < problems[j].Line
			}
			return problems[i].Path < problems[j].Path
		})
		problems = capProblems(problems)
		return nil, &Errors{Path: displayPath, Problems: problems}
	}
	return &m, nil
}

// capProblems bounds a report. Nobody reads the fortieth message, and locating
// each one walks the document, so an unbounded report is quadratic as well as
// unhelpful.
func capProblems(ps []Problem) []Problem {
	if len(ps) <= maxProblems {
		return ps
	}
	out := append([]Problem(nil), ps[:maxProblems]...)
	return append(out, Problem{
		Message: fmt.Sprintf("There are %d more problems, not listed.", len(ps)-maxProblems),
		Hint:    "Fix these first; the rest are often the same mistake repeated.",
	})
}

// readVersion returns the declared version, its node, and whether the key was
// present at all. Presence is separate from the value because "version: 0" is
// an error and an omitted version is not, and a bare integer cannot say which
// of the two it is.
func readVersion(doc *yaml.Node) (version int, node *yaml.Node, present bool) {
	root := doc
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		root = root.Content[0]
	}
	if root.Kind != yaml.MappingNode {
		return 0, nil, false
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value != "version" {
			continue
		}
		v, err := strconv.Atoi(root.Content[i+1].Value)
		if err != nil {
			return -1, root.Content[i+1], true
		}
		return v, root.Content[i+1], true
	}
	return 0, nil, false
}

func findAlias(n *yaml.Node, path string, budget *int) *Problem {
	if n == nil || *budget <= 0 {
		return nil
	}
	*budget--
	if n.Kind == yaml.AliasNode {
		return &Problem{
			Line: n.Line, Column: n.Column, Path: path,
			Message: "YAML anchors and aliases are not allowed in a manifest.",
			Hint:    "Write the value out. Expansion is how a small file becomes a large one.",
		}
	}
	for _, c := range n.Content {
		if p := findAlias(c, path, budget); p != nil {
			return p
		}
	}
	return nil
}

func checkDepth(n *yaml.Node, path string, depth int, budget *int) *Problem {
	if n == nil || *budget <= 0 {
		return nil
	}
	*budget--
	if depth > maxDepth {
		return &Problem{
			Line: n.Line, Column: n.Column, Path: path,
			Message: fmt.Sprintf("The manifest nests deeper than %d levels.", maxDepth),
			Hint:    "Nothing in a manifest needs that depth; this usually means a generated file.",
		}
	}
	for _, c := range n.Content {
		if p := checkDepth(c, path, depth+1, budget); p != nil {
			return p
		}
	}
	return nil
}

// unknownFieldProblems turns the decoder's field errors into located problems
// with a suggestion when a near miss exists.
//
// The decoder reports the line but not a suggestion, and a suggestion is what
// turns "unknown field healthpath" into a fix rather than a search.
func unknownFieldProblems(err error, doc *yaml.Node) []Problem {
	var te *yaml.TypeError
	if !aferrors.As(err, &te) {
		return nil
	}
	out := make([]Problem, 0, min(len(te.Errors), maxProblems))
	for _, msg := range te.Errors {
		if len(out) >= maxProblems {
			out = append(out, Problem{
				Message: fmt.Sprintf("There are %d more problems, not listed.",
					len(te.Errors)-maxProblems),
				Hint: "Fix these first; the rest are often the same mistake repeated.",
			})
			break
		}
		p := Problem{Message: cleanOne(msg)}
		if l, ok := parseLine(msg); ok {
			p.Line = l
		}
		if field, ok := parseUnknownField(msg); ok {
			p.Message = fmt.Sprintf("Unknown key %q.", field)
			if near := suggest(field, keysAt(doc, p.Line)); near != "" {
				p.Hint = fmt.Sprintf("Did you mean %q?", near)
			} else {
				p.Hint = "The manifest reference lists every key. Unknown keys are refused so that a typo cannot silently change an environment."
			}
		}
		out = append(out, p)
	}
	return out
}

func parseLine(msg string) (int, bool) {
	// The decoder formats messages as "line 12: ...".
	if !strings.HasPrefix(msg, "line ") {
		return 0, false
	}
	rest := msg[len("line "):]
	i := strings.IndexByte(rest, ':')
	if i < 0 {
		return 0, false
	}
	n, err := strconv.Atoi(rest[:i])
	if err != nil {
		return 0, false
	}
	return n, true
}

func parseUnknownField(msg string) (string, bool) {
	const marker = "field "
	i := strings.Index(msg, marker)
	if i < 0 || !strings.Contains(msg, "not found in type") {
		return "", false
	}
	rest := msg[i+len(marker):]
	j := strings.IndexByte(rest, ' ')
	if j < 0 {
		return "", false
	}
	return rest[:j], true
}

func cleanOne(msg string) string {
	if i := strings.Index(msg, ": "); i >= 0 && strings.HasPrefix(msg, "line ") {
		msg = msg[i+2:]
	}
	msg = strings.ReplaceAll(msg, "schema.", "")
	if msg == "" {
		return "The value is not valid here."
	}
	return strings.ToUpper(msg[:1]) + msg[1:] + "."
}

func cleanYAMLError(err error) string {
	msg := err.Error()
	msg = strings.ReplaceAll(msg, "yaml: ", "")
	msg = strings.ReplaceAll(msg, "schema.", "")
	return msg
}

// keysAt returns the sibling keys of the mapping containing a line, which is
// the candidate set a suggestion is drawn from.
func keysAt(doc *yaml.Node, targetLine int) []string {
	var best []string
	var bestSpan = 1 << 30
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		if n == nil {
			return
		}
		if n.Kind == yaml.MappingNode {
			lo, hi := 1<<30, 0
			var keys []string
			for i := 0; i+1 < len(n.Content); i += 2 {
				k := n.Content[i]
				keys = append(keys, k.Value)
				if k.Line < lo {
					lo = k.Line
				}
				if k.Line > hi {
					hi = k.Line
				}
			}
			if targetLine >= lo && targetLine <= hi && hi-lo < bestSpan {
				best, bestSpan = keys, hi-lo
			}
		}
		for _, c := range n.Content {
			walk(c)
		}
	}
	walk(doc)
	return best
}

// suggest returns the closest known key within a small edit distance.
func suggest(got string, siblings []string) string {
	candidates := append([]string(nil), knownKeys...)
	candidates = append(candidates, siblings...)
	best, bestDist := "", 3
	for _, c := range candidates {
		if c == got {
			continue
		}
		d := editDistance(strings.ToLower(got), strings.ToLower(c))
		if d < bestDist {
			best, bestDist = c, d
		}
	}
	return best
}

// knownKeys is every key name the manifest uses, at any level. A suggestion
// only has to be close enough to be useful, so one flat list beats tracking
// which keys are legal in which position.
var knownKeys = []string{
	"version", "name", "services", "database", "egress", "personas", "auth", "workflows",
	"invariants", "insights", "load", "runtime", "github",
	"path", "kind", "build", "command", "port", "health_path", "health_timeout",
	"env", "replicas", "resources", "schedule", "migrate", "depends_on",
	"strategy", "dockerfile", "target", "context", "image", "args", "allow_hosts",
	"required", "sandbox", "value", "from", "cpu", "memory",
	"provider", "source_url_env", "url_env", "masking_rules", "golden", "subset", "seed",
	"max_age", "retain", "storage", "storage_url",
	"enabled", "seed_table", "seed_where", "max_rows", "follow_dependents",
	"virtual_relationships", "default", "allow_ipv6", "rules",
	"host", "mode", "paths", "methods", "rate_limit", "credential", "fixtures",
	"webhook_path", "note", "email", "phone", "role", "login", "mfa", "attributes",
	"description", "persona", "start_path", "independent", "budget", "expect", "tags",
	"steps", "usd", "duration", "sql",
	"migration_rehearsal", "query_regression", "plan_diff", "regression_factor",
	"regression_min_ms", "large_table_rows", "rolling_compatibility", "when", "against",
	"source", "source_config", "scale", "safe_routes", "unsafe_routes", "thresholds",
	"p95_increase", "error_rate", "query_count_increase",
	"ttl", "idle_sleep", "domain", "namespace_prefix", "kubeconfig_context",
	"comment", "fork_policy", "teardown_on",
	"adapter", "token_env", "url", "connection", "table", "sessions", "password",
	"schema", "id", "json", "timestamps", "min_length", "symbols", "forbid",
	// These four were declared in schemas/manifest.v1.json and missing here,
	// so a typo of any of them got no suggestion. Found by the drift test,
	// which is the point of having one.
	"project", "api_key_env", "max_branches", "to",
}

func editDistance(a, b string) int {
	if a == b {
		return 0
	}
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min3(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}

func line(n *yaml.Node) int {
	if n == nil {
		return 0
	}
	return n.Line
}

func col(n *yaml.Node) int {
	if n == nil {
		return 0
	}
	return n.Column
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%d MiB", n>>20)
	case n >= 1<<10:
		return fmt.Sprintf("%d KiB", n>>10)
	default:
		return fmt.Sprintf("%d bytes", n)
	}
}

// ParseDuration accepts the manifest's duration spellings, which are a
// deliberate subset of Go's: a plain integer with one unit, plus d for days,
// which Go does not support and which reads far better for a seven day TTL.
func ParseDuration(s string) (time.Duration, error) {
	if s == "" {
		return 0, fmt.Errorf("manifest: the duration is empty")
	}
	if strings.HasSuffix(s, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil {
			return 0, fmt.Errorf("manifest: %q is not a duration: %w", s, err)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("manifest: %q is not a duration, expected a number with ms, s, m, h, or d", s)
	}
	return d, nil
}

// confine cleans p and reports whether it stays inside the repository.
//
// It is the check that stops a manifest from naming ../../etc as a service
// directory or a Dockerfile. Absolute paths are refused for the same reason:
// a manifest is committed and shared, so it must not depend on one machine's
// layout.
func confine(p string) (string, bool) {
	if p == "" {
		return "", true
	}
	if filepath.IsAbs(p) || strings.HasPrefix(p, "/") {
		return "", false
	}
	// Cleaning with path rather than filepath keeps the result in forward
	// slashes, which is what the manifest and the container both use, and
	// makes the check identical on Windows.
	clean := path.Clean(filepath.ToSlash(p))
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", false
	}
	if clean == "." {
		return "", true
	}
	return clean, true
}
