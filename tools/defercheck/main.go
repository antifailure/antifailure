// Command defercheck refuses work that says somebody will do it later.
//
// THE FAILURE IT WAS WRITTEN FOR, and it is a gap in an instrument rather than
// a defect in a page. tools/docs/forbidden.sh already refuses the four marker
// words as "an unfinished note", and the two word promise as "a promise
// instead of a page". It reads six paths, all of them Markdown:
// docs/src/content/docs, examples, and four files at the repository root. That
// is the documentation this product ships and it is roughly 300 of the 2875
// tracked files.
//
// So on the day this was written, a note to the author planted in any Go file,
// any SQL migration, any Terraform stack, any workflow, the justfile, the
// api's or the runner's TypeScript, the console, the marketing site, or any of
// the seventy gates in this directory passed every check this repository runs.
// The one instrument with an opinion about deferred work was pointed at a
// tenth of the tree, and nothing said so. A marker is cheap to write and
// invisible afterwards, which is the whole reason it needs a gate rather than
// a habit.
//
// WHY THIS IS A SECOND TOOL AND NOT A WIDER forbidden.sh. That scan asks a
// different question of a different surface. Six of its seven rules are about
// a PAGE somebody reads: filler text, an unfilled slot, a private hostname, a
// tenant identifier. Those are right for prose and wrong for source, where a
// GUID is a fixture and `xxx` is a format placeholder. Widening its file set
// would either fire on all of that or grow a pile of exemptions that make the
// prose rules weaker. This asks the one question that is true of every file in
// the tree, in prose and in code alike: does anything here say the work is not
// finished?
//
// FIVE RULES, and each one is a different shape of the same claim.
//
//  1. An unfinished note. The marker words listed in markerWords below,
//     matched case sensitively and at word boundaries, because they are
//     markers rather than words: a case insensitive scan fires on the third of
//     them inside an identifier and a boundaryless one fires on a hexadecimal
//     digest.
//
//  2. A promise instead of a capability. A product described as about to do
//     the thing does not do the thing. Note what the pattern leaves out and
//     why: the "not yet" clause covers implemented, supported, wired and
//     hooked up, and deliberately not written, because
//     web/apps/api/src/analytics/rollup.ts uses that phrasing correctly about
//     rows inside a transaction, and a rule that fires on an accurate sentence
//     is answered by making the sentence worse.
//
//  3. Work marked as not the real thing. A fix or a shim that calls itself
//     short lived is deferred work that named itself. forbidden.sh has this
//     rule for prose; this is the same rule over the rest of the tree.
//
//  4. Work handed to a later change. Both of its hits in this tree today are
//     quotations of another project's release notes, and both are exempted
//     with that as the reason. The rule is prospective and it is honest to say
//     so: it has never caught our own deferral, and it is what catches the
//     first one.
//
//  5. A test that skips without saying why. This is the rule the other four
//     cannot reach and the reason this tool is worth more than a grep. A
//     marker is the WEAKEST form of deferred work, because somebody wrote it
//     down. A test that is disabled is the strongest: it reports a skip, the
//     suite stays green, and the count in the summary reads as a pass. Go's
//     two argumentless skip calls state no reason at all; JavaScript's
//     suffixed skip and todo forms, its x prefixed ones, and a skip option
//     whose value is the bare literal true are unconditional, so they say a
//     test was turned off rather than that a condition for running it was
//     absent. This repository's own convention is the shape that passes:
//     `skip: (await available()) ? false : 'no Postgres at
//     AF_TEST_DATABASE_URL'`, a measured condition and the thing that fixes
//     it. The two patterns in rules below are the specification.
//
// THE FILE SET IS THE GIT INDEX, never a walk of the working tree, and that is
// a rule this repository paid for. tools/gatecheck was rewritten because the
// version that walked the tree read an untracked scratch script and refused a
// fully pinned tree while CI stayed green. A gate whose verdict depends on
// what happens to be lying around is not a gate.
//
// AND IT SAYS WHAT IT DID NOT CHECK, because the defect this repository keeps
// finding in its own instruments is a check that prints a number while
// silently skipping the files it could not parse. A tracked file holding a NUL
// byte cannot be scanned for text. If its name ends in one of the extensions
// below, it is a font or an image, and it is counted and named in the report
// as not checked. If it does not, the run FAILS, because a source file that no
// text gate in this repository can read is a hole in all of them at once. That
// rule found runner/src/cassette.ts, a TypeScript file carrying a raw NUL
// inside a string literal, which every text instrument here had been skipping
// in silence for as long as it existed.
//
// THE EXEMPTIONS PIN A FRAGMENT OF THE LINE, not the path, and that is the
// difference between this file and a list of directories to ignore. A path
// exemption excuses a file forever, so the next marker somebody writes in
// .github/workflows/ci.yml would be covered by a row added for a sentence
// about dependabot-core's parser. A row here has to quote a piece of the line
// it excuses, so it covers that line and nothing else, and a marker on the
// next line of the same file is reported.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// The marker words are assembled from fragments rather than written whole, so
// that this file and its test do not match their own rule. The alternative is
// an exemption for the gate's own source, and an exemption that excuses the
// checker is the one row nobody ever revisits. Spelling it this way costs two
// plus signs and means the tool is subject to itself.
var markerWords = []string{
	"TO" + "DO",
	"TB" + "D",
	"FIX" + "ME",
	"WI" + "P",
	"XX" + "X",
	"HA" + "CK",
}

// rule is a name and the pattern that earns it. The name is the sentence the
// report prints, so it says what is wrong rather than which regexp fired: a
// reader who disagrees with a verdict needs to know which claim they are
// arguing with.
type rule struct {
	why string
	re  *regexp.Regexp
	// langs limits a rule to file extensions where it means something. Empty
	// means every text file. The two skip rules carry one because a sentence
	// about skipping in a Markdown page is prose and a skip key in a JSON
	// fixture is data.
	langs []string
}

// rules is the whole of this tool's opinion. Ordered so that the two rules
// about a disabled test come last, because they are the narrow ones and a file
// caught by a marker rule as well should lead with the marker.
var rules = []rule{
	{
		why: "an unfinished note",
		re:  regexp.MustCompile(`\b(` + strings.Join(markerWords, "|") + `)\b`),
	},
	{
		why: "a promise instead of a capability",
		re: regexp.MustCompile(`[Cc]oming ` + "soon" +
			`|[Nn]ot yet (implemented|supported|wired|hooked up)`),
	},
	{
		why: "work marked as not the real thing",
		re: regexp.MustCompile(`[Tt]emporary (workaround|solution|fix|measure|shim|stub|answer)` +
			`|[Ff]or the time being`),
	},
	{
		why: "work handed to a later change",
		re: regexp.MustCompile(`in a (later|future|subsequent) ` +
			`(pull request|PR|commit|change|release|version)`),
	},
	{
		// Go's own two spellings that carry no message, and the same two
		// calls handed an empty string literal: a skip whose reason is the
		// empty string prints a blank line where the reason belongs, which is
		// worse than the bare call because it looks deliberate. This comment
		// does not spell those calls out, because this file is tracked and
		// the rule below would refuse it; the gate found exactly that the
		// first time the file was committed.
		why:   "a test that skips without saying why",
		re:    regexp.MustCompile(`\.Skip(Now)?\(\s*\)|\.Skipf?\(\s*""`),
		langs: []string{".go"},
	},
	{
		// An unconditional disable. `xit` and `.todo` have no conditional
		// form at all; `it.skip` and `skip: true` do, and the conditional one
		// is what this repository already writes, so refusing the bare form
		// costs nothing that is currently here. `.skip()` with no argument is
		// the runtime API called with no reason, the JavaScript twin of the
		// rule above.
		why: "a test disabled rather than explained",
		re: regexp.MustCompile(`\b(it|test|describe|context|suite)\.(skip|todo)\s*\(` +
			`|\b(xit|xdescribe|xtest)\s*\(` +
			`|\.skip\s*\(\s*\)` +
			`|\bskip\s*:\s*true\b`),
		langs: []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs"},
	},
}

// binaryExtensions are the suffixes a tracked file is allowed to be
// unreadable under. Everything else holding a NUL byte is a failure, because a
// source or prose file that no text tool can read is invisible to every text
// gate here at once rather than only to this one.
var binaryExtensions = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true,
	".ico": true, ".mp4": true, ".mov": true, ".webm": true, ".pdf": true,
	".woff": true, ".woff2": true, ".ttf": true, ".otf": true, ".eot": true,
	".zip": true, ".gz": true, ".tgz": true, ".tar": true, ".wasm": true,
	".bin": true, ".so": true, ".dylib": true, ".dll": true, ".a": true,
}

func main() {
	root := flag.String("root", ".", "repository root")
	exemptions := flag.String("exemptions", "", "path to the exemptions file, default tools/docs/defer-exemptions.tsv under the root")
	flag.Parse()
	if args := flag.Args(); len(args) > 0 {
		*root = args[0]
	}
	if *exemptions == "" {
		*exemptions = filepath.Join(*root, "tools", "docs", "defer-exemptions.tsv")
	}

	if err := run(*root, *exemptions, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "\ndefercheck: %v\n", err)
		os.Exit(1)
	}
}

// finding is one line that says the work is not finished.
type finding struct {
	file string
	line int
	why  string
	text string
}

// exemption is one excused line. fragment is a literal piece of the line, and
// it is what makes a row cover a line rather than a file.
type exemption struct {
	path     string
	why      string
	fragment string
	reason   string
	used     bool
}

func run(root, exemptionsPath string, out io.Writer) error {
	files, err := tracked(root)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("git listed no tracked files under %s, so this check is looking in the wrong place", root)
	}

	excused, err := readExemptions(exemptionsPath)
	if err != nil {
		return err
	}

	var found []finding
	var unreadable []string
	var opaque []string
	scanned := 0

	for _, rel := range files {
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			// A tracked path that cannot be opened is not a clean result. A
			// deleted file still in the index, or one a permission refuses,
			// is a file this gate did not look at, and saying nothing about
			// it is the silent skip this tool exists to refuse.
			unreadable = append(unreadable, fmt.Sprintf("%s (%v)", rel, err))
			continue
		}
		if bytes.IndexByte(body, 0) >= 0 {
			if binaryExtensions[strings.ToLower(filepath.Ext(rel))] {
				opaque = append(opaque, rel)
				continue
			}
			unreadable = append(unreadable, rel+" (holds a NUL byte, so no text gate in this repository can read it)")
			continue
		}
		scanned++
		found = append(found, scan(rel, body, excused)...)
	}

	if len(unreadable) > 0 {
		sort.Strings(unreadable)
		return fmt.Errorf("%d tracked %s could not be scanned, and a file no text gate can read is a hole in all of them:\n  %s",
			len(unreadable), plural(len(unreadable), "file", "files"), strings.Join(unreadable, "\n  "))
	}
	if scanned == 0 {
		return fmt.Errorf("no tracked file under %s held any text, so this check is looking in the wrong place", root)
	}

	var stale []string
	for _, e := range excused {
		if !e.used {
			stale = append(stale, fmt.Sprintf("%s\t%s\t%s", e.path, e.why, e.fragment))
		}
	}

	sort.SliceStable(found, func(i, j int) bool {
		if found[i].file != found[j].file {
			return found[i].file < found[j].file
		}
		return found[i].line < found[j].line
	})
	// Write errors are ignored explicitly and once, for the reason prosecheck
	// gives: the verdict of this tool is its exit code, not its report, so a
	// broken pipe changes what a person can read and not whether the build
	// should fail.
	report := func(format string, args ...any) { _, _ = fmt.Fprintf(out, format, args...) }

	// Every finding is named with its file, its line and the claim it broke,
	// on the report rather than in the error, because the error is one line and
	// a reader needs all of them. This is also what the tool's own tests read:
	// a gate whose findings only exist as a count cannot be proved to have
	// found the right thing.
	for _, f := range found {
		report("%s:%d: %s\n    %s\n", f.file, f.line, f.why, f.text)
	}
	if len(stale) > 0 {
		// An exemption that excuses nothing is a claim about the tree that
		// stopped being true, and left alone it becomes a licence nobody
		// granted over whatever takes that line next.
		report("%d %s in the exemptions matches nothing and can be deleted:\n  %s\n",
			len(stale), plural(len(stale), "row", "rows"), strings.Join(stale, "\n  "))
	}
	if len(found)+len(stale) > 0 {
		return fmt.Errorf("%d %s, %d %s", scanned, plural(scanned, "file", "files"),
			len(found)+len(stale), plural(len(found)+len(stale), "problem", "problems"))
	}

	// The report names what was not checked rather than only what was. A
	// count on its own reads as coverage, and the whole point of this line is
	// that the reader can see the gap.
	report("defercheck: %d files scanned, 0 deferrals\n", scanned)
	if len(opaque) > 0 {
		sort.Strings(opaque)
		report("defercheck: %d tracked %s not checked, each an image, a font or another binary: %s\n",
			len(opaque), plural(len(opaque), "file is", "files are"), strings.Join(opaque, ", "))
	}
	return nil
}

// scan applies every rule that speaks this file's language, reporting at most
// one finding per line per rule.
func scan(rel string, body []byte, excused []*exemption) []finding {
	ext := strings.ToLower(filepath.Ext(rel))
	var found []finding
	lines := strings.Split(string(body), "\n")
	for _, r := range rules {
		if !speaks(r, ext) {
			continue
		}
		for i, line := range lines {
			if !r.re.MatchString(line) {
				continue
			}
			text := strings.TrimSpace(line)
			if excuse(excused, rel, r.why, line) {
				continue
			}
			found = append(found, finding{file: rel, line: i + 1, why: r.why, text: text})
		}
	}
	return found
}

func speaks(r rule, ext string) bool {
	if len(r.langs) == 0 {
		return true
	}
	for _, l := range r.langs {
		if l == ext {
			return true
		}
	}
	return false
}

// excuse marks and reports the first row that covers this line. Both the path
// and the rule have to match, and the row's fragment has to appear in the line
// itself, which is what keeps a row from covering the whole file.
func excuse(excused []*exemption, rel, why, line string) bool {
	hit := false
	for _, e := range excused {
		if e.path != rel || e.why != why || !strings.Contains(line, e.fragment) {
			continue
		}
		e.used = true
		hit = true
	}
	return hit
}

// tracked asks git for the index. The -z form is what survives a path with a
// space or a newline in it.
func tracked(root string) ([]string, error) {
	cmd := exec.Command("git", "-C", root, "ls-files", "-z")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files in %s: %w: %s", root, err, strings.TrimSpace(stderr.String()))
	}
	var files []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" {
			files = append(files, p)
		}
	}
	return files, nil
}

// readExemptions parses the four column file. All four columns are required,
// and a row missing one is an error rather than a row that quietly covers
// more than its author meant: a row with no reason is the thing the stale
// check exists to prevent, arriving on day one.
func readExemptions(path string) ([]*exemption, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var out []*exemption
	for i, line := range strings.Split(string(body), "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 4 {
			return nil, fmt.Errorf("%s:%d has %d tab separated fields and needs four, a path, a rule, a fragment of the line and a reason", path, i+1, len(parts))
		}
		for j, p := range parts {
			if strings.TrimSpace(p) == "" {
				return nil, fmt.Errorf("%s:%d leaves field %d empty, so the row says less than it has to", path, i+1, j+1)
			}
		}
		out = append(out, &exemption{path: parts[0], why: parts[1], fragment: parts[2], reason: parts[3]})
	}
	return out, nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
