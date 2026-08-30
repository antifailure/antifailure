// Command errgen turns engine/internal/errors/catalog.yaml into Go constants
// and documentation pages.
//
// The catalog is the single source of truth for the error surface. Generating
// both the code and the documentation from it means the two cannot disagree,
// and committing the output means a contributor who forgets to run the
// generator gets a red gate rather than a silent drift.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// flagPattern matches a long command line flag so that it is not mistaken for
// a double hyphen used as punctuation.
var flagPattern = regexp.MustCompile(`(^|[\s'"(])--[a-zA-Z][a-zA-Z0-9-]*`)

type entry struct {
	Code      string `yaml:"code"`
	Area      string `yaml:"area"`
	Message   string `yaml:"message"`
	NextStep  string `yaml:"next_step"`
	Docs      string `yaml:"docs"`
	Retryable bool   `yaml:"retryable"`
	ExitCode  int    `yaml:"exit_code"`
	// Planned marks a code reserved for a feature this version does not have.
	// The constant is still generated, so the code that will return it compiles
	// the moment it is written, and the reference page leaves it out: a page
	// describing errors the software cannot produce sends somebody searching
	// for their problem to a description of a different one.
	Planned bool `yaml:"planned"`
}

type catalog struct {
	Errors []entry `yaml:"errors"`
}

var areaNames = map[string]string{
	"MAN": "Manifest",
	"DET": "Detection",
	"SEC": "Secrets",
	"DB":  "Database",
	"MSK": "Masking and verification",
	"BLD": "Build",
	"RUN": "Runtime",
	"NET": "Egress",
	"AGT": "Agents",
	"LOD": "Load",
	"ORC": "Differential oracle",
	"GH":  "GitHub",
	"CP":  "Control plane",
	"INF": "Infrastructure",
	"EE":  "Enterprise",
	"SCH": "Scheduling",
	"CPL": "Control plane",
}

func main() {
	var (
		in     = flag.String("catalog", "engine/internal/errors/catalog.yaml", "path to the catalog")
		goOut  = flag.String("go", "engine/internal/errors/codes.gen.go", "path for the generated Go file")
		docOut = flag.String("docs", "docs/src/content/docs/reference/errors.md", "path for the generated reference page")
		check  = flag.Bool("check", false, "fail if regenerating would change anything")
	)
	flag.Parse()

	if err := run(*in, *goOut, *docOut, *check); err != nil {
		fmt.Fprintln(os.Stderr, "errgen:", err)
		os.Exit(1)
	}
}

func run(in, goOut, docOut string, check bool) error {
	raw, err := os.ReadFile(in)
	if err != nil {
		return err
	}
	var c catalog
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return fmt.Errorf("parse %s: %w", in, err)
	}
	if err := validate(c.Errors); err != nil {
		return err
	}
	sort.Slice(c.Errors, func(i, j int) bool { return c.Errors[i].Code < c.Errors[j].Code })

	goSrc, err := renderGo(c.Errors)
	if err != nil {
		return err
	}
	docSrc := renderDocs(c.Errors)

	for _, f := range []struct {
		path string
		data []byte
	}{{goOut, goSrc}, {docOut, docSrc}} {
		if check {
			old, err := os.ReadFile(f.path)
			if err != nil {
				return fmt.Errorf("%s is missing; run 'just generate'", f.path)
			}
			if !bytes.Equal(old, f.data) {
				return fmt.Errorf("%s is out of date; run 'just generate'", f.path)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(f.path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(f.path, f.data, 0o644); err != nil {
			return err
		}
	}
	if !check {
		fmt.Printf("errgen: %d codes across %d areas\n", len(c.Errors), countAreas(c.Errors))
	}
	return nil
}

func countAreas(es []entry) int {
	seen := map[string]struct{}{}
	for _, e := range es {
		seen[e.Area] = struct{}{}
	}
	return len(seen)
}

func validate(es []entry) error {
	if len(es) == 0 {
		return fmt.Errorf("the catalog is empty")
	}
	seen := map[string]struct{}{}
	var problems []string
	for _, e := range es {
		if _, dup := seen[e.Code]; dup {
			problems = append(problems, e.Code+": duplicate code")
		}
		seen[e.Code] = struct{}{}

		parts := strings.Split(e.Code, "-")
		switch {
		case len(parts) != 3 || parts[0] != "AF":
			problems = append(problems, e.Code+": code must read AF-<AREA>-<NNN>")
		case parts[1] != e.Area:
			problems = append(problems, e.Code+": code area does not match the area field "+e.Area)
		case len(parts[2]) != 3:
			problems = append(problems, e.Code+": the numeric part must be three digits")
		}
		if _, ok := areaNames[e.Area]; !ok {
			problems = append(problems, e.Code+": unknown area "+e.Area)
		}
		if !endsSentence(e.Message) {
			problems = append(problems, e.Code+": message must end in a period or a placeholder")
		}
		if !endsSentence(e.NextStep) {
			problems = append(problems, e.Code+": next_step must end in a period or a placeholder")
		}
		if e.Docs == "" || strings.HasPrefix(e.Docs, "/") {
			problems = append(problems, e.Code+": docs must be a slug with no leading slash")
		}
		if e.ExitCode < 0 || e.ExitCode > 10 {
			problems = append(problems, e.Code+": exit_code must be within the registry, 0 to 10")
		}
		// Prose rules that apply to every user facing string in the project.
		for _, s := range []string{e.Message, e.NextStep} {
			if strings.Contains(s, "\u2014") {
				problems = append(problems, e.Code+": prose must not use an em dash")
			}
			// A double hyphen is forbidden as punctuation but is how command
			// line flags are spelled, so flags are removed before the check.
			if strings.Contains(flagPattern.ReplaceAllString(s, " "), "--") {
				problems = append(problems, e.Code+": prose must not use a double hyphen as punctuation")
			}
			if strings.Contains(strings.ToLower(s), "contact support") {
				problems = append(problems, e.Code+": a next step must be an action the reader can take")
			}
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("catalog is not valid:\n  %s", strings.Join(problems, "\n  "))
	}
	return nil
}

// endsSentence accepts a string that ends in a period, or in a placeholder
// such as {detail} whose substituted value carries the provider's own
// punctuation. Anything else is an unfinished sentence.
func endsSentence(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	return strings.HasSuffix(s, ".") || strings.HasSuffix(s, "}")
}

func constName(code string) string {
	// AF-DB-006 becomes AFDB006.
	return strings.ReplaceAll(code, "-", "")
}

func renderGo(es []entry) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString("// Code generated by tools/errgen from catalog.yaml. DO NOT EDIT.\n\n")
	b.WriteString("package errors\n\n")

	b.WriteString("// Catalog codes. Each constant names an entry in catalog.yaml.\n")
	b.WriteString("const (\n")
	lastArea := ""
	for _, e := range es {
		if e.Area != lastArea {
			if lastArea != "" {
				b.WriteString("\n")
			}
			fmt.Fprintf(&b, "\t// %s\n", areaNames[e.Area])
			lastArea = e.Area
		}
		fmt.Fprintf(&b, "\t// %s\n", wrapComment(e.Message, "\t// "))
		fmt.Fprintf(&b, "\t%s Code = %q\n", constName(e.Code), e.Code)
	}
	b.WriteString(")\n\n")

	b.WriteString("// catalog is the generated lookup table.\n")
	b.WriteString("var catalog = map[Code]Entry{\n")
	for _, e := range es {
		fmt.Fprintf(&b, "\t%s: {\n", constName(e.Code))
		fmt.Fprintf(&b, "\t\tCode:      %s,\n", constName(e.Code))
		fmt.Fprintf(&b, "\t\tArea:      %q,\n", e.Area)
		fmt.Fprintf(&b, "\t\tMessage:   %q,\n", e.Message)
		fmt.Fprintf(&b, "\t\tNextStep:  %q,\n", e.NextStep)
		fmt.Fprintf(&b, "\t\tDocs:      %q,\n", e.Docs)
		fmt.Fprintf(&b, "\t\tRetryable: %t,\n", e.Retryable)
		fmt.Fprintf(&b, "\t\tExitCode:  %s,\n", exitConst(e.ExitCode))
		b.WriteString("\t},\n")
	}
	b.WriteString("}\n")

	return format.Source(b.Bytes())
}

func exitConst(n int) string {
	names := map[int]string{
		0: "ExitSuccess", 1: "ExitFailure", 2: "ExitUsage", 3: "ExitConfiguration",
		4: "ExitAuth", 5: "ExitProvider", 6: "ExitPolicyDenied", 7: "ExitVerification",
		8: "ExitTestFailure", 9: "ExitInterruptedClean", 10: "ExitInterruptedDirty",
	}
	if s, ok := names[n]; ok {
		return s
	}
	return fmt.Sprintf("ExitCode(%d)", n)
}

func wrapComment(s, prefix string) string {
	const width = 68
	words := strings.Fields(s)
	var lines []string
	cur := ""
	for _, w := range words {
		if cur == "" {
			cur = w
			continue
		}
		if len(cur)+1+len(w) > width {
			lines = append(lines, cur)
			cur = w
			continue
		}
		cur += " " + w
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return strings.Join(lines, "\n"+prefix)
}

func renderDocs(es []entry) []byte {
	var b bytes.Buffer
	b.WriteString("---\n")
	b.WriteString("title: Error reference\n")
	b.WriteString("description: Every error Antifailure can return, what causes it, and what to do about it.\n")
	b.WriteString("sidebar:\n  order: 6\n")
	b.WriteString("---\n\n")
	b.WriteString("Every user facing error carries a code of the form `AF-<AREA>-<NNN>`.\n")
	b.WriteString("This page is generated from `engine/internal/errors/catalog.yaml`, so it\n")
	b.WriteString("cannot fall behind the code: a code with no entry here fails the build, and\n")
	b.WriteString("an entry that nothing returns fails it too.\n\n")

	b.WriteString("## Exit codes\n\n")
	b.WriteString("Scripts can branch on these. They are stable.\n\n")
	b.WriteString("| Code | Meaning |\n| --- | --- |\n")
	for _, r := range [][2]string{
		{"0", "Success."},
		{"1", "A generic failure. The message says what."},
		{"2", "The command was used incorrectly."},
		{"3", "Configuration is wrong or incomplete."},
		{"4", "Authentication or authorization failed."},
		{"5", "A provider failed. Often retryable."},
		{"6", "A policy denied the operation."},
		{"7", "Verification failed. Masking or an invariant."},
		{"8", "A test failed. Agent verdicts or load thresholds."},
		{"9", "Interrupted, and teardown completed cleanly."},
		{"10", "Interrupted, and resources are still recorded. Run `af down` again."},
	} {
		fmt.Fprintf(&b, "| `%s` | %s |\n", r[0], r[1])
	}
	b.WriteString("\n")

	byArea := map[string][]entry{}
	var areas []string
	planned := 0
	for _, e := range es {
		// Reserved codes are not documented. Somebody reading this page is
		// looking up an error they just saw, and an entry for something this
		// version cannot produce is at best noise and at worst a wrong answer
		// that looks right.
		if e.Planned {
			planned++
			continue
		}
		if _, ok := byArea[e.Area]; !ok {
			areas = append(areas, e.Area)
		}
		byArea[e.Area] = append(byArea[e.Area], e)
	}
	sort.Strings(areas)

	if planned > 0 {
		fmt.Fprintf(&b,
			"%d further codes are reserved for features this version does not have. "+
				"They are in `engine/internal/errors/catalog.yaml` and are left out here "+
				"because this page is for looking up an error you have actually seen.\n\n",
			planned)
	}

	for _, a := range areas {
		fmt.Fprintf(&b, "## %s\n\n", areaNames[a])
		for _, e := range byArea[a] {
			fmt.Fprintf(&b, "### %s\n\n", e.Code)
			fmt.Fprintf(&b, "%s\n\n", e.Message)
			fmt.Fprintf(&b, "**What to do.** %s\n\n", e.NextStep)
			retry := "No. Retrying the same operation unchanged will fail the same way."
			if e.Retryable {
				retry = "Yes. The engine retries automatically where it can."
			}
			fmt.Fprintf(&b, "| | |\n| --- | --- |\n")
			fmt.Fprintf(&b, "| Exit code | `%d` |\n", e.ExitCode)
			fmt.Fprintf(&b, "| Retryable | %s |\n", retry)
			fmt.Fprintf(&b, "| More | [%s](%s) |\n\n", e.Docs, docsURL(e.Docs))
		}
	}
	return b.Bytes()
}

// docsURL turns a catalog docs field into the address the built site serves.
//
// The trailing slash goes on the path, not on the end of the string. Two codes
// point at a heading rather than a page, and appending the slash blindly built
// /docs/reference/cli#af-init/, whose fragment is "af-init/" and matches no
// heading. The link resolved, landed at the top of a long page, and looked
// like it worked.
func docsURL(docs string) string {
	path, fragment, hasFragment := strings.Cut(docs, "#")
	url := "/docs/" + path + "/"
	if hasFragment {
		url += "#" + fragment
	}
	return url
}
