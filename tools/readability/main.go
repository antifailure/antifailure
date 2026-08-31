// Command readability reports how hard each documentation page is to read.
//
// A report rather than a gate, and deliberately. Prose quality is not a
// threshold: a page explaining a masking transform has longer sentences than
// a quickstart and should. What this catches is the page that drifted, the
// one nobody has read aloud, the paragraph that turned into a single
// eighty word sentence while somebody was adding a clause.
//
// Usage:
//
//	go run ./tools/readability .            report every page, worst first
//	go run ./tools/readability --max 24 .   also fail when a page is worse
//
// The flags come BEFORE the path, and that ordering is load bearing rather
// than cosmetic. Go's flag package stops parsing at the first argument that
// is not a flag, so `readability . --max 24` parses no flags at all: max
// stays at its zero value, the threshold branch never runs, and the command
// reports every page and exits 0 no matter how bad the prose is. Both the
// justfile recipe and the CI step were written that way, and the usage line
// directly above this one is where they copied it from, so this gate could
// not fail from the day it was added until the day somebody read the flag
// package's rule. NArg is checked below so that the next person gets an
// error instead of a green run.
//
// The score is Flesch reading ease, on the usual scale where higher is
// easier: 60 to 70 is plain English, below 30 is heavy going. Syllables are
// counted by the standard vowel group heuristic, which is an approximation
// and is stated as one rather than presented as a measurement.
package main

import (
	"flag"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

func main() {
	max := flag.Float64("max", 0, "fail when a page's mean sentence length exceeds this")
	flag.Parse()

	root := "."
	if flag.NArg() > 0 {
		root = flag.Arg(0)
	}
	// A gate that cannot fail is worse than no gate, because it is counted.
	// Anything after the path is an argument the flag package silently
	// ignored, and the only way to reach here with one is the mistake
	// described above, so refuse rather than measure and pass.
	if flag.NArg() > 1 {
		fmt.Fprintf(os.Stderr,
			"readability: %q came after the path and was ignored, so any threshold in it was not applied.\n"+
				"Put the flags first: go run ./tools/readability --max 28 %s\n",
			strings.Join(flag.Args()[1:], " "), root)
		os.Exit(2)
	}
	if err := run(root, *max, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "readability:", err)
		os.Exit(1)
	}
}

// report is one page's numbers.
type report struct {
	Path      string
	Words     int
	Sentences int
	Mean      float64
	Longest   int
	Flesch    float64
}

func run(root string, max float64, out *os.File) error {
	pages, err := collect(root)
	if err != nil {
		return err
	}
	if len(pages) == 0 {
		return fmt.Errorf("no markdown under %s, so this report is looking in the wrong place", root)
	}

	reports := make([]report, 0, len(pages))
	for _, path := range pages {
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		r := measure(string(raw))
		r.Path = filepath.ToSlash(rel)
		if r.Sentences == 0 {
			continue
		}
		reports = append(reports, r)
	}

	// Worst first, because a report sorted by name is a report nobody reads
	// past the first screen.
	sort.Slice(reports, func(i, j int) bool { return reports[i].Mean > reports[j].Mean })

	fmt.Fprintf(out, "%-58s %6s %6s %7s %7s\n", "page", "words", "mean", "longest", "flesch")
	over := 0
	for _, r := range reports {
		fmt.Fprintf(out, "%-58s %6d %6.1f %7d %7.1f\n", r.Path, r.Words, r.Mean, r.Longest, r.Flesch)
		if max > 0 && r.Mean > max {
			over++
		}
	}

	fmt.Fprintf(out, "\nreadability: %d pages, mean sentence %.1f words, hardest page %s\n",
		len(reports), overallMean(reports), reports[0].Path)
	if over > 0 {
		return fmt.Errorf("%d pages have a mean sentence longer than %.0f words", over, max)
	}
	return nil
}

func overallMean(rs []report) float64 {
	words, sentences := 0, 0
	for _, r := range rs {
		words += r.Words
		sentences += r.Sentences
	}
	if sentences == 0 {
		return 0
	}
	return float64(words) / float64(sentences)
}

// collect finds the pages a reader sees: the documentation site and the
// examples, whose READMEs are the first prose most people meet. docs/plan is
// the build log for this repository rather than product documentation.
func collect(root string) ([]string, error) {
	var out []string
	for _, dir := range []string{
		filepath.Join(root, "docs", "src", "content", "docs"),
		filepath.Join(root, "examples"),
	} {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			// Installed dependencies and build output are not prose this
			// project ships. An example with a package.json puts thousands of
			// other people's READMEs under node_modules, and the report went
			// from 45 pages to 94 with the hardest one belonging to semver.
			if d.IsDir() {
				switch d.Name() {
				case "node_modules", ".next", "dist", "vendor":
					return fs.SkipDir
				}
				return nil
			}
			if filepath.Ext(path) != ".md" {
				return nil
			}
			out = append(out, path)
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	sort.Strings(out)
	return out, nil
}

var (
	frontmatter = regexp.MustCompile(`(?s)\A---\n.*?\n---\n`)
	fenced      = regexp.MustCompile("(?s)```.*?```")
	indented    = regexp.MustCompile(`(?m)^(?:    |\t).*$`)
	inlineCode  = regexp.MustCompile("`[^`]*`")
	link        = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	heading     = regexp.MustCompile(`(?m)^#{1,6} .*$`)
	tableRow    = regexp.MustCompile(`(?m)^\|.*$`)
	htmlComment = regexp.MustCompile(`(?s)<!--.*?-->`)
	directive   = regexp.MustCompile(`(?m)^:::.*$`)
	listItem    = regexp.MustCompile(`(?m)^\s*(?:[-*+]|\d+\.)\s+`)
	wordRe      = regexp.MustCompile(`[A-Za-z][A-Za-z'-]*`)
)

// measure counts the prose and nothing else.
//
// Code, tables, headings and link targets are removed rather than counted. A
// table of forty error codes is not a sentence, and a page that happens to
// hold one is not harder to read for it. Link text stays, because a reader
// reads it.
func measure(page string) report {
	text := frontmatter.ReplaceAllString(page, "")
	text = fenced.ReplaceAllString(text, " ")
	// An indented block is a code block too, and a README that lists three
	// endpoints that way is not harder to read for it.
	text = indented.ReplaceAllString(text, " ")
	text = htmlComment.ReplaceAllString(text, " ")
	text = tableRow.ReplaceAllString(text, " ")
	text = heading.ReplaceAllString(text, " ")
	text = directive.ReplaceAllString(text, " ")
	text = link.ReplaceAllString(text, "$1")
	text = inlineCode.ReplaceAllString(text, "code")
	// A list item is a sentence, whether or not its author ended it with a
	// full stop. Most do not, so three bullets and the paragraph after them
	// were being read as one sentence and counted at the length of all four.
	// That inflates the mean on every page that uses a list, which is the
	// opposite of the bias this tool says it has: the splitter is deliberately
	// crude in the direction of reporting a page as easier than it is, and
	// this was the one place it reported pages as harder. A README with two
	// short lists measured 24.2 words a sentence and was reported as the
	// hardest page in the project; it measures 22.1 once each item is its own
	// sentence, and the hardest page is the one that genuinely is.
	text = listItem.ReplaceAllString(text, ". ")

	var r report
	syllables := 0
	for _, sentence := range splitSentences(text) {
		words := wordRe.FindAllString(sentence, -1)
		if len(words) == 0 {
			continue
		}
		r.Sentences++
		r.Words += len(words)
		if len(words) > r.Longest {
			r.Longest = len(words)
		}
		for _, w := range words {
			syllables += countSyllables(w)
		}
	}
	if r.Sentences == 0 {
		return r
	}
	r.Mean = float64(r.Words) / float64(r.Sentences)
	r.Flesch = math.Round((206.835-
		1.015*r.Mean-
		84.6*float64(syllables)/float64(r.Words))*10) / 10
	return r
}

// splitSentences breaks on terminal punctuation followed by whitespace.
//
// Crude, and the crudeness is bounded: an abbreviation splits a sentence in
// two and makes the page look easier than it is, which biases the report
// towards silence rather than towards false alarms.
func splitSentences(text string) []string {
	var out []string
	start := 0
	runes := []rune(text)
	for i, r := range runes {
		if r != '.' && r != '!' && r != '?' {
			continue
		}
		if i+1 < len(runes) && !isSpace(runes[i+1]) {
			continue
		}
		out = append(out, string(runes[start:i]))
		start = i + 1
	}
	if start < len(runes) {
		out = append(out, string(runes[start:]))
	}
	return out
}

func isSpace(r rune) bool { return r == ' ' || r == '\n' || r == '\t' || r == '\r' }

// countSyllables is the vowel group heuristic: every run of vowels is a
// syllable, a trailing silent e is not, and every word has at least one.
//
// It is wrong about "queue" and "rhythm" and right about almost everything
// else, which is enough for a number that is compared against itself over
// time rather than against a standard.
func countSyllables(word string) int {
	w := strings.ToLower(word)
	count, inVowel := 0, false
	for _, r := range w {
		vowel := strings.ContainsRune("aeiouy", r)
		if vowel && !inVowel {
			count++
		}
		inVowel = vowel
	}
	if strings.HasSuffix(w, "e") && !strings.HasSuffix(w, "le") && count > 1 {
		count--
	}
	if count == 0 {
		return 1
	}
	return count
}
