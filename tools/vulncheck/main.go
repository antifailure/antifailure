// Command vulncheck runs govulncheck over every Go module in the repository and
// holds the result to a policy that a bare govulncheck run cannot express.
//
// govulncheck answers "is there a known vulnerability my code can reach". That
// is necessary and not sufficient. Some findings cannot be fixed by upgrading:
// an advisory may carry no fix for the module path we import, or the vulnerable
// code may be in a component we do not run. Those need a written decision, not
// a flag that turns the scanner off. So every reachable finding must be matched
// by an entry in .govulncheck.yaml that says why it cannot hurt us and when
// that judgement expires.
//
// Three things fail the gate, and the third is the one that matters most:
//
//  1. A reachable vulnerability with no entry. The default is to fail.
//  2. An entry past its expiry. Accepting a risk is a decision with a shelf
//     life; without an expiry the file becomes a graveyard nobody rereads.
//  3. An entry that matches nothing. A suppression that no longer suppresses
//     anything is dead code, and dead code in a security policy is worse than
//     dead code anywhere else: it reads as protection that is not there, and it
//     hides the fact that the real finding moved or changed shape.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// policyFile is read relative to the repository root.
const policyFile = ".govulncheck.yaml"

// dateLayout is the only accepted spelling of an expiry. Deliberately not a
// full timestamp: the decision is "reread this by such a day", not a deadline
// to the second.
const dateLayout = "2006-01-02"

type policy struct {
	Allow []allowEntry `yaml:"allow"`
}

type allowEntry struct {
	ID      string `yaml:"id"`
	Module  string `yaml:"module"`
	Reason  string `yaml:"reason"`
	Expires string `yaml:"expires"`

	expires time.Time
}

// finding is the subset of govulncheck's JSON we act on. The stream also
// carries config, progress and osv messages, which we read for the summary
// text but do not decide on.
type message struct {
	OSV     *osvEntry `json:"osv"`
	Finding *finding  `json:"finding"`
}

type osvEntry struct {
	ID      string `json:"id"`
	Summary string `json:"summary"`
}

type finding struct {
	OSV   string  `json:"osv"`
	Trace []frame `json:"trace"`
}

type frame struct {
	Module   string `json:"module"`
	Package  string `json:"package"`
	Function string `json:"function"`
}

// reachable reports whether this finding is a called-symbol finding rather than
// a "you require a module that has a vulnerability somewhere" finding.
// govulncheck distinguishes the two by whether the innermost trace frame names
// a function. Requiring a vulnerable module we never call is not a defect worth
// blocking a merge over, and treating it as one trains people to ignore the
// gate.
func (f *finding) reachable() bool {
	return len(f.Trace) > 0 && f.Trace[0].Function != ""
}

// vulnerableModule is the module the vulnerability lives in, which is the
// innermost frame. The outermost frame is our own code, and keying on that
// would make every entry read "module: our own engine", which says nothing.
func (f *finding) vulnerableModule() string {
	if len(f.Trace) == 0 {
		return ""
	}
	return f.Trace[0].Module
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "vulncheck: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	root := "."
	if len(args) > 0 {
		root = args[0]
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}

	pol, err := loadPolicy(filepath.Join(root, policyFile))
	if err != nil {
		return err
	}

	// Every module in the repository, found by walking for go.mod rather than
	// listed here or read out of go.work. A hardcoded list keeps passing after
	// somebody adds a module, and it passes by not looking, which is the exact
	// failure this tool exists to prevent.
	//
	// go.work is the wrong source for the same reason: ee/engine is a real
	// module holding shipping enterprise code and it is deliberately outside
	// the workspace, so that its dependencies stay out of the engine's graph.
	// Reading the workspace would have left it unscanned, which is the one
	// module where nobody would notice.
	//
	// tools is included even though it does not ship. A compromised code
	// generator or merge gate is a supply chain problem whether or not its
	// output carries a version number.
	modules, err := discoverModules(root)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "scanning %d modules: %s\n\n", len(modules), strings.Join(modules, ", "))

	// Built once, then run in each module's own directory rather than driven
	// with govulncheck's -C flag. Building once is the cheaper shape when there
	// is more than one module to scan, and a working directory is easier to
	// reason about than a flag that changes what ./... means.
	//
	// Worth knowing before diagnosing a slow run as a hung one: symbol level
	// analysis of the engine walks the Docker client, the OpenTelemetry
	// exporters and a pure Go SQLite, and it is minutes of CPU. On a loaded
	// machine it can sit at a few percent CPU for a long time with its output
	// apparently frozen, because the JSON is block buffered and the last flush
	// lands on a boundary. Check the load average before concluding it is
	// stuck.
	bin, cleanup, err := buildScanner(root)
	if err != nil {
		return err
	}
	defer cleanup()

	var all []*finding
	summaries := map[string]string{}
	for _, mod := range modules {
		found, sums, err := scan(bin, root, mod)
		if err != nil {
			return fmt.Errorf("scanning %s: %w", mod, err)
		}
		all = append(all, found...)
		for k, v := range sums {
			summaries[k] = v
		}
	}

	return decide(all, summaries, pol, out)
}

// discoverModules finds every Go module in the repository, as a path relative
// to the root.
//
// Directories that hold somebody else's code or deliberately broken fixtures
// are skipped: a vendored tree is already covered through the module that
// vendored it, and a testdata module is usually invalid on purpose.
func discoverModules(root string) ([]string, error) {
	skip := map[string]bool{
		".git": true, "node_modules": true, "vendor": true,
		"testdata": true, "dist": true, "bin": true,
	}

	var mods []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && (skip[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
				return fs.SkipDir
			}
			return nil
		}
		if d.Name() != "go.mod" {
			return nil
		}
		rel, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		if rel == "." {
			// A go.mod at the root would mean the repository is itself a
			// module; there is not one today, and scanning "." alongside the
			// real modules would double count.
			return nil
		}
		mods = append(mods, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(mods) == 0 {
		return nil, fmt.Errorf("found no Go module under %s, so there would be nothing to scan", root)
	}
	sort.Strings(mods)
	return mods, nil
}

// buildScanner compiles the govulncheck pinned in tools/go.mod. Pinning it
// there rather than reaching for @latest means the scanner is version
// controlled like everything else: a scanner that silently changes under you is
// a poor thing to hang a merge gate on.
func buildScanner(root string) (string, func(), error) {
	dir, err := os.MkdirTemp("", "vulncheck")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }

	bin := filepath.Join(dir, "govulncheck")
	cmd := exec.Command("go", "build", "-o", bin, "golang.org/x/vuln/cmd/govulncheck")
	cmd.Dir = filepath.Join(root, "tools")
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	if out, err := cmd.CombinedOutput(); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("building govulncheck: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return bin, cleanup, nil
}

// scan runs govulncheck against one module and returns its reachable findings.
func scan(bin, root, mod string) ([]*finding, map[string]string, error) {
	cmd := exec.Command(bin, "-format", "json", "./...")
	cmd.Dir = filepath.Join(root, mod)
	// GOWORK=off because the modules are scanned one at a time, each against
	// its own go.mod and go.sum, which is what a consumer of that module gets.
	// It is also required rather than merely tidy: ee/engine is deliberately
	// outside go.work, and Go refuses to build a directory that is not listed
	// in an active workspace.
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOWORK=off")

	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if err != nil {
		// govulncheck exits non-zero when it finds something in text mode, but
		// in JSON mode a non-zero exit means it could not do its job at all.
		// The most common cause is no route to the vulnerability database, and
		// a scanner that silently passes when it cannot reach its data is worse
		// than no scanner, so this is fatal rather than a warning.
		return nil, nil, fmt.Errorf("govulncheck failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}

	findings, summaries, err := parse(stdout)
	if err != nil {
		return nil, nil, err
	}
	return findings, summaries, nil
}

// parse reads govulncheck's newline-delimited JSON stream. It is a sequence of
// concatenated objects rather than an array, so it is decoded as a stream.
func parse(stream []byte) ([]*finding, map[string]string, error) {
	dec := json.NewDecoder(strings.NewReader(string(stream)))
	var findings []*finding
	summaries := map[string]string{}
	for {
		var m message
		if err := dec.Decode(&m); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, nil, fmt.Errorf("decoding govulncheck output: %w", err)
		}
		if m.OSV != nil {
			summaries[m.OSV.ID] = m.OSV.Summary
		}
		if m.Finding != nil && m.Finding.reachable() {
			findings = append(findings, m.Finding)
		}
	}
	return findings, summaries, nil
}

func loadPolicy(path string) (*policy, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		// No file means no accepted risks, which is the correct starting state
		// and must not be an error. It becomes an error only if something is
		// actually found.
		return &policy{}, nil
	}
	if err != nil {
		return nil, err
	}

	var pol policy
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&pol); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: %w", policyFile, err)
	}

	for i := range pol.Allow {
		e := &pol.Allow[i]
		switch {
		case e.ID == "":
			return nil, fmt.Errorf("%s: an entry has no id", policyFile)
		case e.Module == "":
			return nil, fmt.Errorf("%s: %s has no module", policyFile, e.ID)
		case strings.TrimSpace(e.Reason) == "":
			return nil, fmt.Errorf("%s: %s has no reason, and an accepted risk without a stated reason is not a decision", policyFile, e.ID)
		case e.Expires == "":
			return nil, fmt.Errorf("%s: %s has no expires date", policyFile, e.ID)
		}
		t, err := time.Parse(dateLayout, e.Expires)
		if err != nil {
			return nil, fmt.Errorf("%s: %s has expires %q, which is not a %s date", policyFile, e.ID, e.Expires, dateLayout)
		}
		e.expires = t
	}
	return &pol, nil
}

// decide applies the three rules and writes a report.
func decide(findings []*finding, summaries map[string]string, pol *policy, out io.Writer) error {
	return decideAt(findings, summaries, pol, out, time.Now())
}

// decideAt takes the clock as an argument so the expiry rule is testable
// without waiting for a date to pass.
func decideAt(findings []*finding, summaries map[string]string, pol *policy, out io.Writer, now time.Time) error {
	// key is id+module: the same advisory can cover several module paths, and
	// accepting it for one is not accepting it for another.
	type key struct{ id, module string }

	seen := map[key]bool{}
	for _, f := range findings {
		seen[key{f.OSV, f.vulnerableModule()}] = true
	}

	allowed := map[key]*allowEntry{}
	var expired, unused []*allowEntry
	for i := range pol.Allow {
		e := &pol.Allow[i]
		k := key{e.ID, e.Module}
		allowed[k] = e
		if now.After(e.expires) {
			expired = append(expired, e)
		}
		if !seen[k] {
			unused = append(unused, e)
		}
	}

	var uncovered []key
	for k := range seen {
		if allowed[k] == nil {
			uncovered = append(uncovered, k)
		}
	}
	sort.Slice(uncovered, func(i, j int) bool {
		if uncovered[i].id != uncovered[j].id {
			return uncovered[i].id < uncovered[j].id
		}
		return uncovered[i].module < uncovered[j].module
	})

	var problems []string

	for _, k := range uncovered {
		_, _ = fmt.Fprintf(out, "REACHABLE  %s  %s\n", k.id, k.module)
		if s := summaries[k.id]; s != "" {
			_, _ = fmt.Fprintf(out, "           %s\n", s)
		}
		_, _ = fmt.Fprintf(out, "           https://pkg.go.dev/vuln/%s\n", k.id)
		problems = append(problems, fmt.Sprintf("%s is reachable in %s and is not accepted in %s", k.id, k.module, policyFile))
	}

	for _, e := range expired {
		_, _ = fmt.Fprintf(out, "EXPIRED    %s  %s  accepted until %s\n", e.ID, e.Module, e.Expires)
		problems = append(problems, fmt.Sprintf("the decision to accept %s in %s expired on %s and needs rereading", e.ID, e.Module, e.Expires))
	}

	for _, e := range unused {
		_, _ = fmt.Fprintf(out, "STALE      %s  %s  matches nothing\n", e.ID, e.Module)
		problems = append(problems, fmt.Sprintf("%s in %s is accepted in %s but nothing reaches it any more, so the entry is claiming to protect against something that is not there", e.ID, e.Module, policyFile))
	}

	covered := len(seen) - len(uncovered)
	_, _ = fmt.Fprintf(out, "\n%d reachable, %d accepted, %d unaccepted, %d expired, %d stale\n",
		len(seen), covered, len(uncovered), len(expired), len(unused))

	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "\n  "))
	}
	return nil
}
