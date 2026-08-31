// Command prosecheck enforces the one prose rule this project actually has.
//
// The build plan asks for vale with the Google style guide "plus a custom rule
// that flags em dashes and double hyphens in prose". This is that custom rule,
// on its own. vale is a separate binary with a style package to install in CI,
// and the rule it was wanted for is four characters of Unicode; shipping the
// rule now beats shipping neither.
//
// The rule matters because it is the most visible tell of text nobody wrote.
// Em dashes are what a machine reaches for when it does not know whether a
// clause is a parenthesis, a colon or a full stop. This repository's prose is
// meant to read as though a person decided, so the character is banned and a
// comma, a colon or a new sentence is used instead.
//
// CODE IS EXEMPT, and that is the whole difficulty. `--flag` is how a command
// line option is spelled and appears constantly in these documents. So fenced
// blocks and inline spans are skipped, and outside them only a double hyphen
// surrounded by whitespace is a defect, because that is the shape somebody
// means as punctuation.
//
// THE SITE IS PROSE TOO. This checker read `.md` and nothing else, and the
// marketing site is TypeScript, so it had never once been scanned: it shipped
// sixty-eight em dashes to the public web under a green gate. A page's <title>
// is the first prose anybody reads, in the tab and in the search result, and a
// string in a component is the whole of the copy. So `.ts`, `.tsx` and `.mjs`
// under www and console are checked as well.
//
// The two languages need opposite treatment of the same character, which is
// the trap. In Markdown a backtick span is code and is emptied before the scan.
// In TypeScript a backtick span is a template literal, which is a string, which
// is copy: `${title} — ${SITE_NAME}` is exactly the defect this exists for. So
// the scan is chosen by file extension rather than applied uniformly.
package main

import (
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// documents are the trees whose Markdown prose is checked.
// examples is here because an example's README is documentation a user reads
// before anything else: it is the first prose most people meet.
var documents = []string{"docs/src/content/docs", "examples", "."}

// sources are the trees whose TypeScript carries copy rather than only code.
//
// www is the public site and console is the signed-in application. Both write
// their sentences as string literals and JSX text, so both are prose that this
// project publishes and neither is documentation with a different extension.
var sources = []string{"www", "console"}

// sourceExts are the extensions scanned inside sources.
//
// .mjs earns its place: www/scripts/markdown-twins.mjs strips the site name off
// a title, so it carries the separator character in a regexp and would have
// kept it after every .tsx was clean.
var sourceExts = map[string]bool{".ts": true, ".tsx": true, ".mjs": true}

// There is deliberately no exemption list here, and that is a decision rather
// than an omission.
//
// tools/docs/manifest-exemptions.tsv exists because the documentation genuinely
// has to show another product's configuration file, so there is a real case the
// rule cannot express. This rule has no such case: after the site was cleaned,
// not one line under www or console needed the character to do its job. An
// exemption mechanism with no entries is a mechanism nobody has ever run, which
// is the shape of every feature that turns out not to work the day it is
// finally needed. If a genuine case appears, add the list then, with a user for
// it, and make it report an entry that has stopped being needed the way the
// manifest one does.

// banned are the characters and sequences, with what to write instead.
//
// The advice is part of the rule. A checker that says "do not do this" and
// stops teaches somebody to work around it; one that says what to write teaches
// them the convention.
var banned = []struct {
	pattern *regexp.Regexp
	what    string
	instead string
}{
	{regexp.MustCompile(`\x{2014}`), "an em dash", "a comma, a colon, or a new sentence"},
	{regexp.MustCompile(`\x{2013}`), "an en dash", "the word to, or a hyphen in a compound"},
	{regexp.MustCompile(`\s--\s`), "a double hyphen as punctuation", "a comma, a colon, or a new sentence"},
}

// escaped are the ways source code writes the same characters without typing
// them, which a scan for the character itself cannot see.
//
// This is not hypothetical. www/scripts/markdown-twins.mjs carried the em dash
// as `—` inside a regular expression and was the one file under www that
// stayed invisible after every literal em dash on the site was gone. An escape
// is still the character; a reader of the built page cannot tell which way it
// was spelled. HTML entities are here for the same reason: JSX renders
// `&mdash;` as an em dash.
//
// Markdown is not scanned for these, because there `—` is six literal
// characters and an entity is more often being documented than used.
var escaped = []struct {
	pattern *regexp.Regexp
	what    string
	instead string
}{
	{regexp.MustCompile(`(?i)\\u\{?2014\}?|&mdash;|&#x?8212;`), "an em dash, written as an escape", "a comma, a colon, or a new sentence"},
	{regexp.MustCompile(`(?i)\\u\{?2013\}?|&ndash;|&#x?8211;`), "an en dash, written as an escape", "the word to, or a hyphen in a compound"},
}

type finding struct {
	file, what, instead, line string
	num                       int
}

func main() {
	root := flag.String("root", ".", "repository root")
	flag.Parse()
	if args := flag.Args(); len(args) > 0 {
		*root = args[0]
	}
	if err := run(*root, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "\nprosecheck: %v\n", err)
		os.Exit(1)
	}
}

func run(root string, out io.Writer) error {
	files, err := markdown(root)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("found no markdown under %s, so this check is looking in the wrong place", root)
	}
	// Counted separately from the Markdown so that an empty result is an error
	// rather than a quiet success. A gate that silently checks nothing reports
	// green, which is how the site kept its em dashes through every run of this
	// tool that has ever happened.
	code, err := source(root)
	if err != nil {
		return err
	}
	if len(code) == 0 {
		return fmt.Errorf("found no TypeScript under %v in %s, so this check is looking in the wrong place", sources, root)
	}
	files = append(files, code...)

	var found []finding
	for _, f := range files {
		body, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			return err
		}
		found = append(found, Check(f, string(body))...)
	}

	sort.Slice(found, func(i, j int) bool {
		if found[i].file != found[j].file {
			return found[i].file < found[j].file
		}
		return found[i].num < found[j].num
	})
	// Write errors are ignored explicitly, once, with a reason: the verdict of
	// this tool is its exit code, not its report, so a broken pipe on stdout
	// changes what a person can read and not whether the build should fail.
	report := func(format string, args ...any) { _, _ = fmt.Fprintf(out, format, args...) }

	for _, f := range found {
		report("%s:%d  %s. Write %s.\n    %s\n", f.file, f.num, f.what, f.instead, strings.TrimSpace(f.line))
	}
	report("prosecheck: %d files, %d %s where the punctuation gives it away\n",
		len(files), len(found), plural(len(found), "place", "places"))

	if len(found) > 0 {
		return fmt.Errorf("%d %s use punctuation this project does not",
			len(found), plural(len(found), "place", "places"))
	}
	return nil
}

// fence matches the start or end of a fenced code block.
var fence = regexp.MustCompile("^\\s*```")

// inlineCode matches a span between backticks, which is where a command line
// flag legitimately lives.
var inlineCode = regexp.MustCompile("`[^`]*`")

// Check reports the banned punctuation in one file.
//
// The scan is chosen by extension, because a backtick means opposite things in
// the two languages. Exported so a test can drive it without a file, and so
// anything else that grows prose can reuse the same definition rather than
// write a second one that disagrees.
func Check(name, body string) []finding {
	if filepath.Ext(name) == ".md" {
		return checkMarkdown(name, body)
	}
	return checkSource(name, body)
}

// checkMarkdown scans a document, skipping the code a reader is meant to type.
func checkMarkdown(name, body string) []finding {
	var out []finding
	inFence := false

	for i, line := range strings.Split(body, "\n") {
		if fence.MatchString(line) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		// Inline code is emptied rather than skipped, so a defect elsewhere on
		// the same line is still found.
		clean := inlineCode.ReplaceAllString(line, "``")
		out = append(out, defects(name, i+1, line, clean)...)
	}
	return out
}

// checkSource scans a TypeScript file, where every line is eligible.
//
// Nothing is emptied first. A template literal is a string and a string is
// copy; a `//` comment is a sentence somebody wrote and is where the tell hides
// most reliably, because nobody proofreads a comment. The one construct that
// would produce a false positive is the decrement operator, and the double
// hyphen rule already requires whitespace on both sides, which `i--` and
// `--flag` do not have. So the scan is the plain one and there is nothing to
// carve out.
func checkSource(name, body string) []finding {
	var out []finding
	for i, line := range strings.Split(body, "\n") {
		out = append(out, defects(name, i+1, line, line)...)
		for _, e := range escaped {
			if e.pattern.MatchString(line) {
				out = append(out, finding{
					file: name, num: i + 1, line: line,
					what: e.what, instead: e.instead,
				})
			}
		}
	}
	return out
}

// defects reports the banned punctuation in one line. `clean` is what is
// matched against, `line` is what the reader is shown.
func defects(name string, num int, line, clean string) []finding {
	var out []finding
	for _, b := range banned {
		if b.pattern.MatchString(clean) {
			out = append(out, finding{
				file: name, num: num, line: line,
				what: b.what, instead: b.instead,
			})
		}
	}
	return out
}

// markdown lists the documents to check, relative to root.
func markdown(root string) ([]string, error) {
	return collect(root, documents, func(path string) bool {
		return filepath.Ext(path) == ".md"
	})
}

// source lists the site and console files to check, relative to root.
func source(root string) ([]string, error) {
	return collect(root, sources, func(path string) bool {
		return sourceExts[filepath.Ext(path)]
	})
}

// collect walks the named trees under root and returns every file `keep`
// accepts, sorted and de-duplicated.
func collect(root string, dirs []string, keep func(string) bool) ([]string, error) {
	seen := map[string]bool{}
	var out []string

	for _, dir := range dirs {
		base := filepath.Join(root, dir)
		err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				name := d.Name()
				// The plan's own copy, dependencies and build output are not
				// ours to style.
				if path != base && (name == "node_modules" || name == "dist" || name == "out" ||
					name == "vendor" || strings.HasPrefix(name, ".")) {
					return fs.SkipDir
				}
				// The top level is walked one deep only, so that this does not
				// re-walk the whole repository for the "." entry.
				if dir == "." && path != base {
					return fs.SkipDir
				}
				return nil
			}
			if !keep(path) {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil || seen[rel] {
				return nil
			}
			seen[rel] = true
			out = append(out, filepath.ToSlash(rel))
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(out)
	return out, nil
}

// plural picks the word for a count, so a failing run does not report
// "1 places". The message is the only thing a person reads when this gate goes
// red, and a gate that reads like a machine wrote it is a poor advertisement
// for a rule about prose.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
