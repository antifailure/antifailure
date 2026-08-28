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

// documents are the trees whose prose is checked.
var documents = []string{"docs/src/content/docs", "."}

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
	report("prosecheck: %d documents, %d places where the punctuation gives it away\n", len(files), len(found))

	if len(found) > 0 {
		return fmt.Errorf("%d places use punctuation this project does not", len(found))
	}
	return nil
}

// fence matches the start or end of a fenced code block.
var fence = regexp.MustCompile("^\\s*```")

// inlineCode matches a span between backticks, which is where a command line
// flag legitimately lives.
var inlineCode = regexp.MustCompile("`[^`]*`")

// Check reports the banned punctuation in one document.
//
// Exported so a test can drive it without a file, and so anything else that
// grows prose can reuse the same definition rather than write a second one that
// disagrees.
func Check(name, body string) []finding {
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

		for _, b := range banned {
			if b.pattern.MatchString(clean) {
				out = append(out, finding{
					file: name, num: i + 1, line: line,
					what: b.what, instead: b.instead,
				})
			}
		}
	}
	return out
}

// markdown lists the documents to check, relative to root.
func markdown(root string) ([]string, error) {
	seen := map[string]bool{}
	var out []string

	for _, dir := range documents {
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
			if filepath.Ext(path) != ".md" {
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
