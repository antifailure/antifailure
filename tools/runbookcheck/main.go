// Command runbookcheck proves that a numbered runbook still numbers itself.
//
// A runbook whose steps an operator follows in order carries two kinds of
// number, and only one of them is visible while editing. The headings are
// visible: `### 6. Bind the certificate` is right there. The references are
// not: "the numeric id from step 9" sits four hundred lines away in prose, and
// nothing anywhere ties the two together. So inserting or removing a step is
// an edit to one number and a silent invalidation of every reference past it,
// and the reader who follows the wrong one lands on the wrong step of a
// production stand-up.
//
// Two rules, both mechanical, neither a judgement.
//
// One, the step headings on a page run 1, 2, 3 with no gap and no repeat, in
// the order they appear. A gap is a step somebody deleted without renumbering.
// A repeat is two steps inserted at the same place by two people. Out of order
// is a section moved and not renumbered.
//
// Two, every "step N" a page mentions is a step that page has. This is the one
// with teeth, because it is the half an editor cannot see. Renumbering nine
// headings by hand and missing one cross reference is the ordinary way this
// goes wrong, and it produces a page that reads correctly and sends the
// operator to the wrong place.
//
// WHAT THIS DOES NOT CATCH, said here rather than left for somebody to
// discover by trusting it. It does not catch a step that was deleted and every
// later step renumbered to close the hole, because that is internally
// consistent and this tool only reads the page. `ff893073` did exactly that to
// docs/self-hosting/production.md: it removed the step that binds the
// certificate to the hostname, renumbered fifteen steps to fourteen, and left
// every cross reference correct. This tool was run against that tree and said
// nothing, which is the honest thing to record about it. The missing step was
// found by reading the diff, and that is still the only instrument that finds
// it.
//
// It also has no opinion about whether a step is in the right place, whether
// its content is true, or whether the runbook is complete. It knows the
// numbers agree with each other.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Where the documentation site keeps its pages. Every runbook this tool has an
// opinion about is one of these.
const docsRoot = "docs/src/content/docs"

// A step heading. Third level and numbered, which is how every numbered
// runbook on this site writes one; a `## 1.` would be a page whose whole body
// is the list, and there is not one.
var stepHeading = regexp.MustCompile(`(?m)^### (\d+)\. `)

// A reference to a step from prose. Case insensitive because a sentence can
// start with one, and bounded on the right so that "step 12345" in an id is
// not read as a reference to step 1.
var stepReference = regexp.MustCompile(`(?i)\bstep (\d+)\b`)

// A fenced code block. References inside one are an operator's own shell, not
// this page's numbering, and a heading inside one is an example rather than a
// step.
var fence = regexp.MustCompile("(?m)^```")

// A page is a numbered runbook once it has this many step headings. One
// numbered heading is a heading that happens to start with a digit.
const minimumSteps = 2

type finding struct {
	path string
	line int
	text string
}

func main() {
	flag.Parse()
	root := "."
	if flag.NArg() > 0 {
		root = flag.Arg(0)
	}

	pages, err := runbookPages(filepath.Join(root, docsRoot))
	if err != nil {
		fmt.Fprintln(os.Stderr, "runbookcheck:", err)
		os.Exit(2)
	}

	var findings []finding
	checked := 0
	for _, path := range pages {
		body, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, "runbookcheck:", err)
			os.Exit(2)
		}
		rel, _ := filepath.Rel(root, path)
		f, isRunbook := checkPage(rel, string(body))
		if isRunbook {
			checked++
		}
		findings = append(findings, f...)
	}

	if len(findings) == 0 {
		fmt.Printf("runbookcheck: %d numbered runbooks, every step number agrees\n", checked)
		return
	}
	for _, f := range findings {
		fmt.Fprintf(os.Stderr, "%s:%d: %s\n", f.path, f.line, f.text)
	}
	fmt.Fprintf(os.Stderr, "\nrunbookcheck: %d problems in %d numbered runbooks\n", len(findings), checked)
	os.Exit(1)
}

func runbookPages(dir string) ([]string, error) {
	var pages []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || filepath.Ext(path) != ".md" {
			return nil
		}
		pages = append(pages, path)
		return nil
	})
	sort.Strings(pages)
	return pages, err
}

// checkPage returns what is wrong with one page, and whether the page was a
// numbered runbook at all. A page that is not one is not a pass, it is out of
// scope, and the count this tool prints has to say which.
func checkPage(rel, body string) ([]finding, bool) {
	lines := prose(body)

	var steps []int
	stepLine := map[int]int{}
	for i, line := range lines {
		m := stepHeading.FindStringSubmatch(line + "\n")
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		steps = append(steps, n)
		if _, seen := stepLine[n]; !seen {
			stepLine[n] = i + 1
		}
	}
	if len(steps) < minimumSteps {
		return nil, false
	}

	var findings []finding

	// One: 1, 2, 3, in order, no gap, no repeat.
	for i, n := range steps {
		if n == i+1 {
			continue
		}
		findings = append(findings, finding{rel, stepLine[n], fmt.Sprintf(
			"step %d is the %s heading on this page: the numbering has a gap, a repeat, or a section moved without being renumbered",
			n, ordinal(i+1))})
	}

	// Two: every reference resolves. Deduplicated by the number, because one
	// wrong number repeated six times is one edit to make.
	have := map[int]bool{}
	for _, n := range steps {
		have[n] = true
	}
	reported := map[int]bool{}
	for i, line := range lines {
		if strings.HasPrefix(line, "### ") {
			continue
		}
		for _, m := range stepReference.FindAllStringSubmatch(line, -1) {
			n, _ := strconv.Atoi(m[1])
			if have[n] || reported[n] {
				continue
			}
			reported[n] = true
			findings = append(findings, finding{rel, i + 1, fmt.Sprintf(
				"refers to step %d and this page has steps 1 to %d", n, len(steps))})
		}
	}

	sort.SliceStable(findings, func(i, j int) bool { return findings[i].line < findings[j].line })
	return findings, true
}

// prose blanks out fenced code blocks, keeping the line count so that every
// finding still names the line an editor will look at.
func prose(body string) []string {
	lines := strings.Split(body, "\n")
	inFence := false
	for i, line := range lines {
		if fence.MatchString(line) {
			inFence = !inFence
			lines[i] = ""
			continue
		}
		if inFence {
			lines[i] = ""
		}
	}
	return lines
}

func ordinal(n int) string {
	switch {
	case n%100 >= 11 && n%100 <= 13:
		return strconv.Itoa(n) + "th"
	case n%10 == 1:
		return strconv.Itoa(n) + "st"
	case n%10 == 2:
		return strconv.Itoa(n) + "nd"
	case n%10 == 3:
		return strconv.Itoa(n) + "rd"
	}
	return strconv.Itoa(n) + "th"
}
