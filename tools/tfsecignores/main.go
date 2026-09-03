// Command tfsecignores holds tfsec's inline suppressions to the same policy
// tools/vulncheck holds .govulncheck.yaml to.
//
// tfsec answers "does this configuration break a rule". A finding that cannot
// be fixed still needs a written decision rather than a flag that turns the
// scanner off, and tfsec's mechanism for that is an inline comment:
//
//	#tfsec:ignore:azure-keyvault-specify-network-acl:exp:2027-03-03
//
// That mechanism is most of what is needed and it is missing the parts that
// make a suppression trustworthy. tfsec accepts a directive with no expiry at
// all, and it says nothing whatever about a directive that no longer suppresses
// anything. So this tool applies three rules, and the third is the one that
// matters:
//
//  1. A directive with no expiry, or with no prose beside it. A suppression
//     with no shelf life is a policy change wearing a temporary hat, and one
//     with no stated reason is not a decision.
//  2. A directive past its expiry. tfsec already stops honouring it, so the
//     finding comes back on its own; what tfsec does not do is say WHY it came
//     back. A reader who is told "CRITICAL, vault network ACL" and not "the
//     decision to accept this lapsed on such a date" goes looking for a change
//     that nobody made.
//  3. A directive that suppressed nothing. This is the rule tfsec has no
//     version of. Somebody fixes the configuration, the finding goes away, and
//     the comment explaining why it was tolerated stays behind. From then on it
//     reads as protection that is not there, and it hides the fact that the
//     real finding moved or changed shape.
//
// WHY IT REFUSES TO GUESS. tfsec failing to run and tfsec finding nothing look
// identical in an exit code, and that is not a hypothetical here: the installer
// asked the GitHub releases API for the latest version, got HTTP 403, and the
// step failed before reading a single file. Eight findings sat unseen behind a
// green-looking check. So output this tool cannot parse is an error, never an
// empty result set.
//
// AND WHY IT ASKS THE QUESTION THE WAY IT DOES, because the obvious way is
// wrong and looks right. tfsec has an --include-ignored flag that adds the
// suppressed results to its output with status 2, which reads exactly like
// "here is what your directives suppressed". It is not that. tfsec marks a
// check ignored whenever a directive NAMES it, whether or not the check would
// have failed: point a directive at azure-keyvault-no-purge on a vault that
// already has purge protection and the check moves from passed to ignored, with
// nothing suppressed. A staleness rule built on that flag calls every stale
// directive live, which is the one answer it exists to prevent, and it does so
// silently.
//
// So the source of truth is a --no-ignores run instead: the findings that exist
// with every suppression disabled. A directive is live only if its rule is
// actually failing in its own file in that run. That is a question with a no in
// it.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// dateLayout is the only accepted spelling of an expiry, and it is tfsec's own:
// the directive is read by tfsec as well as by this tool, and a date the two
// disagree about would be worse than no date.
const dateLayout = "2006-01-02"

// reasonFloor is how much prose has to sit directly above a directive before it
// counts as a stated reason. It is deliberately low. The bar is "somebody wrote
// something" rather than a word count nobody can defend, because the thing
// worth catching is a bare directive parachuted in with no explanation at all.
const reasonFloor = 40

// directive is one inline suppression, as written in a .tf file.
type directive struct {
	File    string
	Line    int
	Rule    string
	Expires string

	expires time.Time
}

func (d directive) where() string {
	return fmt.Sprintf("%s:%d", d.File, d.Line)
}

// result is the subset of tfsec's JSON this tool decides on. Status 2 is
// "ignored"; anything else is a finding that was reported.
type result struct {
	RuleID   string `json:"rule_id"`
	LongID   string `json:"long_id"`
	Status   int    `json:"status"`
	Location struct {
		Filename string `json:"filename"`
	} `json:"location"`
}

// tfsec's statuses: 0 failed, 1 passed, 2 ignored. Under --no-ignores nothing
// is ignored and passed results are not emitted, so a failure is the only thing
// expected; it is filtered for explicitly anyway, because a tool that counts
// whatever it is handed stops being able to say no when the shape changes.
const statusFailed = 0

type report struct {
	Results []result `json:"results"`
}

// directiveRe matches tfsec's inline form. The trailing group is everything
// after the rule id, which tfsec spells as colon separated key/value pairs
// (`exp` and `ws`), so it is split rather than matched with a second pattern:
// a new key added upstream should be ignored here, not rejected.
var directiveRe = regexp.MustCompile(`#tfsec:ignore:(\S+)`)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "tfsecignores: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	fl := flag.NewFlagSet("tfsecignores", flag.ContinueOnError)
	fl.SetOutput(out)
	bin := fl.String("tfsec", "tfsec", "path to the tfsec binary")
	dir := fl.String("dir", filepath.Join("infra", "terraform"), "directory to scan, relative to the root")
	if err := fl.Parse(args); err != nil {
		return err
	}

	root := "."
	if fl.NArg() > 0 {
		root = fl.Arg(0)
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	scan := filepath.Join(root, *dir)

	directives, err := parseDirectives(os.DirFS(scan), *dir)
	if err != nil {
		return err
	}

	failing, err := unsuppressedFindings(*bin, scan, root)
	if err != nil {
		return err
	}

	return decide(directives, failing, out)
}

// parseDirectives reads every .tf file under fsys and returns the suppressions
// in it, in file and line order.
//
// prefix is only cosmetic: it is what a path is reported as, so that a failure
// names infra/terraform/... rather than a path relative to a directory the
// reader cannot see from the message.
func parseDirectives(fsys fs.FS, prefix string) ([]directive, error) {
	var found []directive
	err := fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// .terraform holds downloaded providers and modules. A directive
			// in somebody else's module is not ours to hold to a policy, and
			// on a machine that has run `terraform init` there are thousands
			// of files in there.
			if d.Name() == ".terraform" {
				return fs.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".tf" {
			return nil
		}
		raw, err := fs.ReadFile(fsys, path)
		if err != nil {
			return err
		}
		ds, err := parseFile(filepath.ToSlash(filepath.Join(prefix, path)), string(raw))
		if err != nil {
			return err
		}
		found = append(found, ds...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(found, func(i, j int) bool {
		if found[i].File != found[j].File {
			return found[i].File < found[j].File
		}
		return found[i].Line < found[j].Line
	})
	return found, nil
}

// parseFile pulls the directives out of one file's text and applies the rules
// that can be decided from the text alone: an expiry that is present and well
// formed, and a reason that exists.
func parseFile(name, text string) ([]directive, error) {
	lines := strings.Split(text, "\n")
	var found []directive
	for i, line := range lines {
		m := directiveRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		parts := strings.Split(m[1], ":")
		d := directive{File: name, Line: i + 1, Rule: parts[0]}

		for j := 1; j+1 < len(parts); j += 2 {
			if parts[j] == "exp" {
				d.Expires = parts[j+1]
			}
		}
		if d.Rule == "" {
			return nil, fmt.Errorf("%s:%d: a tfsec ignore names no rule", name, i+1)
		}
		if d.Expires == "" {
			return nil, fmt.Errorf(
				"%s:%d: the ignore for %s has no expiry. Write it as "+
					"#tfsec:ignore:%s:exp:YYYY-MM-DD. A suppression with no date is one nobody rereads",
				name, i+1, d.Rule, d.Rule)
		}
		t, err := time.Parse(dateLayout, d.Expires)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: the ignore for %s has expiry %q, which is not a %s date",
				name, i+1, d.Rule, d.Expires, dateLayout)
		}
		d.expires = t

		if len(reasonAbove(lines, i)) < reasonFloor {
			return nil, fmt.Errorf(
				"%s:%d: the ignore for %s has no reason written above it. tfsec has no field for one, "+
					"so the prose beside the directive is the only place a reader can find out why this "+
					"was accepted, and a suppression nobody explained is not a decision",
				name, i+1, d.Rule)
		}
		found = append(found, d)
	}
	return found, nil
}

// reasonAbove collects the run of comment lines directly above index i and
// returns their text with the comment markers removed.
//
// A run rather than one line, because the reason for a suppression worth
// keeping is usually a paragraph, and because a rule that only looked at the
// line above would be satisfied by a comment that says "see above".
func reasonAbove(lines []string, i int) string {
	var prose []string
	for j := i - 1; j >= 0; j-- {
		t := strings.TrimSpace(lines[j])
		if !strings.HasPrefix(t, "#") {
			break
		}
		// Another directive is not prose about this one.
		if directiveRe.MatchString(t) {
			break
		}
		prose = append(prose, strings.TrimSpace(strings.TrimPrefix(t, "#")))
	}
	return strings.TrimSpace(strings.Join(prose, " "))
}

// unsuppressedFindings runs tfsec with every suppression disabled and returns,
// per file, the set of rules that are actually failing there. That set is what
// a directive has to match to be earning its place. Both the short and the long
// id are recorded, because a directive may name either and tfsec reports both.
func unsuppressedFindings(bin, scan, root string) (map[string]map[string]bool, error) {
	cmd := exec.Command(bin, scan, "--no-ignores", "--format", "json", "--no-colour")
	cmd.Dir = root
	stdout, err := cmd.Output()
	// tfsec exits non-zero when it reports a finding, which is the ordinary
	// case here and says nothing about whether it ran. Whether the output
	// parses is what says that, so the exit code is deliberately not consulted
	// and a parse failure below carries the diagnosis instead.
	if err != nil && len(stdout) == 0 {
		detail := err.Error()
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			detail = strings.TrimSpace(string(ee.Stderr))
		}
		return nil, fmt.Errorf("running %s: %s\nIt produced no output at all, so nothing was scanned. "+
			"This is the shape of the failure that hid eight findings behind a green check: "+
			"a scanner that did not run is not a scanner that found nothing", bin, detail)
	}

	var rep report
	if err := json.Unmarshal(stdout, &rep); err != nil {
		return nil, fmt.Errorf("reading tfsec output as JSON: %w\nThe first bytes were: %.200q", err, stdout)
	}

	byFile := map[string]map[string]bool{}
	for _, r := range rep.Results {
		if r.Status != statusFailed {
			continue
		}
		file := normalise(r.Location.Filename, root)
		if byFile[file] == nil {
			byFile[file] = map[string]bool{}
		}
		if r.LongID != "" {
			byFile[file][r.LongID] = true
		}
		if r.RuleID != "" {
			byFile[file][r.RuleID] = true
		}
	}
	return byFile, nil
}

// normalise turns tfsec's absolute filename into the repository relative path
// the directives are reported under, so the two can be compared.
func normalise(name, root string) string {
	if rel, err := filepath.Rel(root, name); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(name)
}

// decide applies the expiry and staleness rules and writes a report.
func decide(directives []directive, failing map[string]map[string]bool, out io.Writer) error {
	return decideAt(directives, failing, out, time.Now())
}

// decideAt takes the clock as an argument so the expiry rule is testable
// without waiting for a date to pass.
func decideAt(directives []directive, failing map[string]map[string]bool, out io.Writer, now time.Time) error {
	var problems []string
	var live int

	for _, d := range directives {
		expired := now.After(d.expires)
		used := failing[d.File][d.Rule]

		switch {
		case expired:
			fmt.Fprintf(out, "EXPIRED  %s  %s  accepted until %s\n", d.where(), d.Rule, d.Expires)
			problems = append(problems, fmt.Sprintf(
				"the decision to accept %s at %s expired on %s. tfsec has already stopped honouring it, "+
					"so the finding is back; reread the reason written above it and either fix the "+
					"configuration or set a new date on purpose",
				d.Rule, d.where(), d.Expires))
		case !used:
			fmt.Fprintf(out, "STALE    %s  %s  nothing in this file breaks that rule\n", d.where(), d.Rule)
			problems = append(problems, fmt.Sprintf(
				"the ignore for %s at %s suppressed nothing: with every ignore disabled, that rule is "+
					"not failing in that file. So either the finding was fixed and this comment now "+
					"reads as protection that is not there, or the finding moved and this is no longer "+
					"pointed at it. Delete it or move it",
				d.Rule, d.where()))
		default:
			live++
		}
	}

	fmt.Fprintf(out, "\n%d ignores, %d live, %d expired or stale\n",
		len(directives), live, len(directives)-live)

	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "\n  "))
	}
	return nil
}
