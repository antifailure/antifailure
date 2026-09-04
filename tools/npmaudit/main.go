// Command npmaudit runs `npm audit` over every lockfile in the repository and
// holds the result to a policy, the way tools/vulncheck does for Go.
//
// THE GAP THIS CLOSES. govulncheck covers the Go modules and nothing covered
// the JavaScript. Eight lockfiles ship here, one of them the control plane that
// holds organizations, policy and billing, and every `npm ci` in
// .github/workflows/ci.yml passes --no-audit. So the half of this repository
// that is exposed to the internet had no dependency advisory check at all,
// while the half that runs on a developer's laptop had a good one.
//
// npm audit is not govulncheck and the difference is worth stating rather than
// papering over: it reads the lockfile and the registry's advisory database,
// and it has no reachability analysis. It cannot tell you that the vulnerable
// function is never called. That makes it noisier per finding, which is exactly
// why the finding goes through a written decision rather than a threshold flag.
// A --audit-level knob turns the gate down quietly; .npmaudit.yaml makes
// somebody say why, and expire the saying.
//
// Three things fail the gate, and they are the same three as vulncheck's,
// because the failure mode is the same one:
//
//  1. An advisory with no entry. The default is to fail.
//  2. An entry past its expiry. Accepting a risk is a decision with a shelf
//     life; without an expiry the file becomes a graveyard nobody rereads.
//  3. An entry that matches nothing. A suppression that no longer suppresses
//     anything reads as protection that is not there.
//
// A project with dependencies that no lockfile covers is REPORTED rather than
// skipped. npm audit cannot speak for a tree it cannot resolve, and a check that
// quietly leaves one project out is the shape of assertion this repository has
// been caught by before: scoped to a collection that excludes the casualty, it
// passes forever and discovers nothing. Runner used to be that project. It has
// its own lockfile now and is audited with the other seven.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// policyFile is read relative to the repository root.
const policyFile = ".npmaudit.yaml"

// dateLayout is the only accepted spelling of an expiry, the same as
// .govulncheck.yaml's. The decision is "reread this by such a day", not a
// deadline to the second.
const dateLayout = "2006-01-02"

type policy struct {
	Allow []allowEntry `yaml:"allow"`
}

type allowEntry struct {
	ID      string `yaml:"id"`
	Package string `yaml:"package"`
	Reason  string `yaml:"reason"`
	Expires string `yaml:"expires"`

	expires time.Time
}

// report is the subset of `npm audit --json` we act on.
//
// AuditReportVersion is a pointer because its presence is how we tell a real
// report from anything else npm printed. npm exits non-zero when it finds an
// advisory AND when it fails, so the exit code alone cannot distinguish "five
// findings" from "no registry".
type report struct {
	AuditReportVersion *int                   `json:"auditReportVersion"`
	Vulnerabilities    map[string]packageVuln `json:"vulnerabilities"`
	Message            string                 `json:"message"`
	Error              *npmError              `json:"error"`
}

type npmError struct {
	Code    string `json:"code"`
	Summary string `json:"summary"`
	Detail  string `json:"detail"`
}

// packageVuln carries npm's own rolled-up severity for the package, which this
// tool deliberately does not decode: it is the maximum across the package's
// advisories, and a decision is made per advisory. The severity below comes off
// the advisory itself.
type packageVuln struct {
	Name string            `json:"name"`
	Via  []json.RawMessage `json:"via"`
}

// advisory is one entry of `via` when that entry is an object.
//
// `via` IS A UNION AND THE DECODER HAS TO SAY SO. An element is either an
// advisory object or a plain string naming another vulnerable package that this
// one depends on. Decoding it as []advisory does not fail loudly; it fails by
// unmarshalling every string element into a zero-valued advisory, which is a
// finding with no id that would then be reported as an unaccepted advisory
// called "". Probed against the real thing rather than assumed: a lockfile
// pinning request 2.88.2 produces
// `"via": ["https://...GHSA-p8p7-x288-28g6" as an object, "form-data", "qs",
// "tough-cookie", "uuid"]` in one array.
//
// The string elements are dropped on purpose and nothing is lost. Each names
// another key in the same vulnerabilities map, so the advisory it points at is
// walked in its own right.
type advisory struct {
	Source   int    `json:"source"`
	Name     string `json:"name"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	Severity string `json:"severity"`
}

// finding is one advisory against one package, in one project.
type finding struct {
	ID       string // the GHSA identifier, from the advisory URL
	Package  string
	Severity string
	Title    string
	URL      string
	Project  string
}

// key is what an allow entry matches on. Advisory plus package, not advisory
// alone: the same advisory can name several packages and accepting it for one
// is not accepting it for another.
type key struct{ id, pkg string }

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "npmaudit: %v\n", err)
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

	if _, err := exec.LookPath("npm"); err != nil {
		return errors.New("npm is not on PATH, and this gate is npm's audit. Install Node.js")
	}

	locked, unlocked, err := discoverProjects(root)
	if err != nil {
		return err
	}

	var findings []*finding
	for _, project := range locked {
		found, err := auditProject(filepath.Join(root, project), project)
		if err != nil {
			return err
		}
		findings = append(findings, found...)
	}

	pol, err := loadPolicy(filepath.Join(root, policyFile))
	if err != nil {
		return err
	}

	return decide(findings, locked, unlocked, pol, out)
}

// discoverProjects returns the directories holding a package-lock.json, and
// separately the ones holding a package.json with dependencies that no lockfile
// covers.
//
// The second list is the point of returning two. npm audit cannot resolve a
// tree without a lockfile: it exits with ENOLOCK and prints an error object
// rather than a report. Skipping those directories silently would leave the
// count in the summary describing a smaller repository than the one being
// checked.
//
// A WORKSPACE MEMBER HAS NO LOCKFILE OF ITS OWN AND IS COVERED ANYWAY, which is
// why coverage is a question about ancestors rather than about siblings. The
// first version of this asked only whether package-lock.json sat next to the
// package.json, and reported seven of this repository's eight lockfile-less
// projects as unchecked: web/apps/api, web/packages/db, web/packages/policy and
// the four under ee/web are npm workspaces, resolved by the root lockfile that
// lists them. Seven false alarms in a summary is how a real one, runner, stops
// being read.
func discoverProjects(root string) (locked, unlocked []string, err error) {
	skip := map[string]bool{
		".git": true, "node_modules": true, "vendor": true,
		"testdata": true, "dist": true, "bin": true,
	}

	// Collected first and classified afterwards, because whether a directory is
	// covered depends on a lockfile that may sit above it and that the walk may
	// not have reached yet.
	type candidate struct {
		rel      string
		manifest string
	}
	var candidates []candidate
	hasLock := map[string]bool{}

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && (skip[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
				return fs.SkipDir
			}
			return nil
		}
		if d.Name() != "package.json" {
			return nil
		}
		dir := filepath.Dir(path)
		rel, err := filepath.Rel(root, dir)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)

		if _, statErr := os.Stat(filepath.Join(dir, "package-lock.json")); statErr == nil {
			hasLock[rel] = true
			locked = append(locked, rel)
			return nil
		}
		candidates = append(candidates, candidate{rel: rel, manifest: path})
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	for _, c := range candidates {
		if coveredByAncestor(c.rel, hasLock) {
			continue
		}
		hasDeps, err := declaresDependencies(c.manifest)
		if err != nil {
			return nil, nil, err
		}
		if hasDeps {
			unlocked = append(unlocked, c.rel)
		}
	}

	if len(locked) == 0 {
		return nil, nil, fmt.Errorf("found no package-lock.json under %s, so there would be nothing to audit", root)
	}
	sort.Strings(locked)
	sort.Strings(unlocked)
	return locked, unlocked, nil
}

// coveredByAncestor reports whether some directory above rel holds a lockfile.
// npm resolves a workspace from the root, so auditing the root audits every
// member, and a member reported separately would be reported twice.
func coveredByAncestor(rel string, hasLock map[string]bool) bool {
	for dir := path.Dir(rel); dir != "." && dir != "/"; dir = path.Dir(dir) {
		if hasLock[dir] {
			return true
		}
	}
	return hasLock["."]
}

// declaresDependencies reports whether a package.json asks for anything at all.
// A package.json with no dependencies has no tree to audit, so it is neither
// covered nor uncovered and saying so would be noise.
func declaresDependencies(path string) (bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	var manifest struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	return len(manifest.Dependencies)+len(manifest.DevDependencies) > 0, nil
}

// auditProject runs npm audit in one directory and flattens its report.
//
// --package-lock-only so that node_modules does not have to be installed: the
// lockfile is the thing under review, and requiring an install would make this
// gate cost six installs and depend on which of them had been run.
func auditProject(dir, project string) ([]*finding, error) {
	cmd := exec.Command("npm", "audit", "--package-lock-only", "--json")
	cmd.Dir = dir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, runErr := cmd.Output()

	// The exit code is deliberately not consulted. npm audit exits 1 when it
	// finds an advisory, which is the case this gate exists to handle, and it
	// also exits non-zero when it cannot run at all. What separates them is
	// whether a report came back.
	return parse(stdout, project, runErr, stderr.String())
}

func parse(stdout []byte, project string, runErr error, stderr string) ([]*finding, error) {
	var rep report
	if err := json.Unmarshal(stdout, &rep); err != nil {
		return nil, fmt.Errorf("%s: npm audit printed something that is not a report: %w\n%s", project, err, trim(stderr))
	}
	if rep.Error != nil {
		return nil, npmRefusal(project, &rep, runErr, stderr)
	}
	if rep.AuditReportVersion == nil {
		if runErr != nil {
			return nil, fmt.Errorf("%s: npm audit failed: %w\n%s", project, runErr, trim(stderr))
		}
		return nil, fmt.Errorf("%s: npm audit produced no report and did not say why\n%s", project, trim(stderr))
	}

	var findings []*finding
	for _, vuln := range rep.Vulnerabilities {
		for _, raw := range vuln.Via {
			var adv advisory
			if err := json.Unmarshal(raw, &adv); err != nil {
				// A string element, naming another package in the same map.
				// Not an error and not a finding: that package is walked on its
				// own turn, carrying its own advisory objects.
				continue
			}
			if adv.URL == "" {
				continue
			}
			name := adv.Name
			if name == "" {
				name = vuln.Name
			}
			findings = append(findings, &finding{
				ID:       identifier(adv.URL, adv.Source),
				Package:  name,
				Severity: adv.Severity,
				Title:    adv.Title,
				URL:      adv.URL,
				Project:  project,
			})
		}
	}
	return findings, nil
}

// npmRefusal keeps every diagnostic channel npm uses for an audit endpoint
// failure. npm 11 writes the useful network failure into the root message,
// then adds an error object whose code, summary and detail are all empty. Older
// releases put the useful fields inside error. The process error and stderr are
// independent evidence and must not disappear merely because stdout was JSON.
func npmRefusal(project string, rep *report, runErr error, stderr string) error {
	var lines []string
	nested := []string{trim(rep.Error.Code), trim(rep.Error.Summary), trim(rep.Error.Detail)}
	var fields []string
	for _, field := range nested {
		if field != "" {
			fields = append(fields, field)
		}
	}
	if len(fields) == 0 {
		lines = append(lines, "npm supplied no error code, summary, or detail")
	} else {
		lines = append(lines, strings.Join(fields, " "))
	}
	if message := trim(rep.Message); message != "" {
		lines = append(lines, "npm message: "+message)
	}
	if runErr != nil {
		lines = append(lines, "process error: "+runErr.Error())
	}
	if diagnostic := trim(stderr); diagnostic != "" {
		lines = append(lines, "stderr: "+diagnostic)
	}
	return fmt.Errorf("%s: npm audit refused: %s", project, strings.Join(lines, "\n"))
}

// identifier is the GHSA id out of the advisory URL, because that is the name a
// person can search for. The numeric npm source id is the fallback: it is
// stable and it is what npm keys on, but nobody recognises it.
func identifier(url string, source int) string {
	if i := strings.LastIndex(url, "/"); i >= 0 && i+1 < len(url) {
		if id := url[i+1:]; strings.HasPrefix(id, "GHSA-") {
			return id
		}
	}
	return fmt.Sprintf("npm-%d", source)
}

func trim(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 2000 {
		return s[:2000] + "\n..."
	}
	return s
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
		case e.Package == "":
			return nil, fmt.Errorf("%s: %s has no package", policyFile, e.ID)
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
func decide(findings []*finding, locked, unlocked []string, pol *policy, out io.Writer) error {
	return decideAt(findings, locked, unlocked, pol, out, time.Now())
}

// decideAt takes the clock as an argument so the expiry rule is testable
// without waiting for a date to pass.
func decideAt(findings []*finding, locked, unlocked []string, pol *policy, out io.Writer, now time.Time) error {
	seen := map[key]*finding{}
	projects := map[key]map[string]bool{}
	for _, f := range findings {
		k := key{f.ID, f.Package}
		seen[k] = f
		if projects[k] == nil {
			projects[k] = map[string]bool{}
		}
		projects[k][f.Project] = true
	}

	allowed := map[key]*allowEntry{}
	var expired, unused []*allowEntry
	for i := range pol.Allow {
		e := &pol.Allow[i]
		k := key{e.ID, e.Package}
		allowed[k] = e
		if now.After(e.expires) {
			expired = append(expired, e)
		}
		if seen[k] == nil {
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
		return uncovered[i].pkg < uncovered[j].pkg
	})

	var problems []string
	var writeErr error
	report := func(format string, args ...any) {
		if writeErr != nil {
			return
		}
		_, writeErr = fmt.Fprintf(out, format, args...)
	}

	for _, k := range uncovered {
		f := seen[k]
		report("ADVISORY   %s  %s  %s  in %s\n", k.id, k.pkg, f.Severity, strings.Join(sortedSet(projects[k]), ", "))
		if f.Title != "" {
			report("           %s\n", f.Title)
		}
		report("           %s\n", f.URL)
		problems = append(problems, fmt.Sprintf("%s affects %s and is not accepted in %s", k.id, k.pkg, policyFile))
	}

	for _, e := range expired {
		report("EXPIRED    %s  %s  accepted until %s\n", e.ID, e.Package, e.Expires)
		problems = append(problems, fmt.Sprintf("the decision to accept %s in %s expired on %s and needs rereading", e.ID, e.Package, e.Expires))
	}

	for _, e := range unused {
		report("STALE      %s  %s  matches nothing\n", e.ID, e.Package)
		problems = append(problems, fmt.Sprintf("%s in %s is accepted in %s but no lockfile carries it any more, so the entry is claiming to protect against something that is not there", e.ID, e.Package, policyFile))
	}

	// Named rather than ignored. A lockfile is what npm audit resolves against,
	// so a project without one is not covered by this gate and the summary must
	// not read as though it were.
	for _, project := range unlocked {
		report("UNCHECKED  %s has dependencies and no package-lock.json, so npm audit cannot speak for it\n", project)
	}

	covered := len(seen) - len(uncovered)
	report("\n%d lockfile(s) audited, %d not covered, %d advisories, %d accepted, %d unaccepted, %d expired, %d stale\n",
		len(locked), len(unlocked), len(seen), covered, len(uncovered), len(expired), len(unused))

	if writeErr != nil {
		return fmt.Errorf("write the npm audit report: %w", writeErr)
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "\n  "))
	}
	return nil
}

func sortedSet(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
