package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var numberValue = map[string]int{
	"two": 2, "three": 3, "four": 4, "five": 5, "six": 6, "seven": 7,
	"eight": 8, "nine": 9, "ten": 10, "eleven": 11, "twelve": 12,
	"thirteen": 13, "fourteen": 14, "fifteen": 15, "sixteen": 16,
	"seventeen": 17, "eighteen": 18, "nineteen": 19, "twenty": 20,
	"twenty-one": 21, "twenty-two": 22, "twenty-three": 23, "twenty-four": 24,
	"twenty-five": 25,
}

// counted matches a stated number. The noun is not in this pattern and the
// words between are not either, because a quantifier for them cannot work:
// greedy, "{0,3}" swallowed "analyzers read the" out of "Twelve analyzers read
// the repository" and left the noun outside the match, so the tool silently
// missed two of the four wrong analyzer counts while reporting success over
// the other two. Lazy is no better, since it settles on nothing every time and
// then "six migration lint rules" stops matching. The gap is measured against
// the noun's own position instead, in nounFollows.
//
// Counts below two are skipped. "one of five states" states a real count and
// "in one state" does not, and the difference is not worth a rule when every
// instance of this defect has been a count of two or more.
var counted = regexp.MustCompile(`(?i)\b(twenty-(?:one|two|three|four|five)|two|three|four|five|six|seven|eight|nine|ten|eleven|twelve|thirteen|fourteen|fifteen|sixteen|seventeen|eighteen|nineteen|twenty|\d{1,3})\s+`)

// filler is the words a count may be separated from its noun by: "six
// migration lint rules" is a claim about rules. Three is the observed maximum
// and anything longer is a different sentence, not a longer noun phrase.
const filler = 3

// nounFollows reports whether the set's noun is what this number counts, and
// the words standing between them.
//
// Requiring the noun at the very start would miss every real instance, since
// all of them qualify the noun. Allowing it anywhere in the sentence would
// read "six tables, and the lint rules ran" as a count of rules. The gap is
// what separates those, so the gap is what is measured.
func nounFollows(noun *regexp.Regexp, rest string) (string, bool) {
	loc := noun.FindStringIndex(rest)
	if loc == nil {
		return "", false
	}
	between := rest[:loc[0]]
	if strings.ContainsAny(between, ".!?:|,;()") {
		return "", false
	}
	if len(strings.Fields(between)) > filler {
		return "", false
	}
	return between, true
}

// hypothetical is a subordinating conjunction, and a count inside the clause
// one introduces is not an assertion about how many of a thing exist.
//
// This is not a guess at what reads badly. It is three false alarms the tool
// produced on correct comments while it was being written, and all three have
// this shape: "When two analyzers disagree, the stronger evidence wins",
// "because a finding was below the confidence threshold or two analyzers
// disagreed", and "so that ten analyzers reading package.json read the disk
// once". None of the fifteen real findings has a subordinator before its
// count, which is what makes this safe to apply to the whole sentence rather
// than to the clause alone.
var hypothetical = regexp.MustCompile(`(?i)\b(when|whenever|if|unless|because|since|where|while|so that|suppose)\b`)

// checkCounts is rule 1: a stated number of members of a set must be its real
// size.
//
// It returns the number of counted phrases it considered as well as the
// findings, because the caller reports what was examined. A rule that has
// stopped matching anything reports zero findings, and zero findings is what a
// clean tree looks like too.
func checkCounts(name, body string, members map[string][]string) ([]finding, int) {
	flat, lineAt := flatten(body)
	var out []finding
	seen := map[string]bool{}
	considered := 0

	for _, s := range sentences(flat) {
		for _, m := range counted.FindAllStringSubmatchIndex(s.text, -1) {
			word := strings.ToLower(s.text[m[2]:m[3]])
			rest := s.text[m[1]:]
			// A number joined to the token before it by a hyphen is part of
			// that token, not a count. Tailwind writes leading-3, gap-4 and
			// text-black/35, and a class attribute in TSX puts several of
			// them within three words of any noun that happens to follow, so
			// "leading-3 tracking-extra-tight text-black/35"> The verdict"
			// was read as a claim that there are three verdicts. Found by
			// declaring a set whose noun appears in markup, which none of the
			// earlier sets did.
			if m[2] > 0 && s.text[m[2]-1] == '-' {
				continue
			}
			if h := hypothetical.FindStringIndex(s.text); h != nil && h[0] < m[0] {
				continue
			}

			n, ok := numberValue[word]
			if !ok {
				v, err := strconv.Atoi(word)
				if err != nil {
					continue
				}
				n = v
			}
			if n < 2 {
				continue
			}
			for _, set := range sets {
				between, ok := nounFollows(set.noun, rest)
				if !ok {
					continue
				}
				if set.context != nil &&
					!set.context.MatchString(s.text) && !set.context.MatchString(between) {
					continue
				}
				considered++
				real := len(members[set.name])
				if n == real {
					continue
				}
				line := lineAt(s.start + m[0])
				// One finding per line per set. A sentence that states the
				// same wrong count twice teaches nobody anything the first
				// report did not.
				key := fmt.Sprintf("%d/%s", line, set.name)
				if seen[key] {
					continue
				}
				seen[key] = true
				out = append(out, finding{
					file: name, line: line,
					why: fmt.Sprintf(
						"says there are %d %s. There are %d: %s. Say %d, or say which ones without a count.",
						n, set.name, real, strings.Join(members[set.name], ", "), real),
					text: excerpt(s.text, m[0]),
				})
			}
		}
	}
	return out, considered
}

type sentence struct {
	text  string
	start int
}

// flatten joins the document into one line and returns a function mapping an
// offset back to a line number.
//
// The claims this looks for cross line breaks: a wrapped markdown paragraph
// puts "the six lint" on one line and "rules" on the next, and a line based
// check would see two fragments and no claim in either.
func flatten(body string) (string, func(int) int) {
	lines := strings.Split(body, "\n")
	var b strings.Builder
	starts := make([]int, 0, len(lines))
	for _, l := range lines {
		starts = append(starts, b.Len())
		b.WriteString(l)
		b.WriteByte(' ')
	}
	return b.String(), func(off int) int {
		lo, hi := 0, len(starts)-1
		for lo < hi {
			mid := (lo + hi + 1) / 2
			if starts[mid] <= off {
				lo = mid
			} else {
				hi = mid - 1
			}
		}
		return lo + 1
	}
}

// sentenceEnd is a full stop, question mark or colon.
//
// A table cell boundary was tried as a fourth and removed. The argument for it
// was that a reference table puts a whole claim in each cell, so the context
// word from one cell should not license a count in the next. It changed
// nothing on the tree either way, no test of it could be made to fail, and it
// only ever made the tool less sensitive: the real findings all carry their
// context word in the same cell as their count, while a row reading
// "| migration_lint | warn | six rules |" would have been missed. A guard with
// no observed false alarm behind it is a guess, and this file keeps only the
// guards a real false alarm demanded.
var sentenceEnd = regexp.MustCompile(`(?:[.!?:]\s+)`)

func sentences(flat string) []sentence {
	var out []sentence
	start := 0
	for _, loc := range sentenceEnd.FindAllStringIndex(flat, -1) {
		if loc[1] > start {
			out = append(out, sentence{text: flat[start:loc[1]], start: start})
		}
		start = loc[1]
	}
	if start < len(flat) {
		out = append(out, sentence{text: flat[start:], start: start})
	}
	return out
}

func excerpt(s string, off int) string {
	lo := off - 40
	if lo < 0 {
		lo = 0
	}
	hi := off + 140
	if hi > len(s) {
		hi = len(s)
	}
	return strings.TrimSpace(strings.Join(strings.Fields(s[lo:hi]), " "))
}

// tableUnder returns the body rows of the first markdown table after a
// heading.
//
// It errors rather than returning nothing when the heading or the table is
// missing, because a renamed heading would otherwise turn rule 2 into a check
// that reads nothing and passes.
func tableUnder(body, heading string) ([][]string, error) {
	lines := strings.Split(body, "\n")
	at := -1
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if !strings.HasPrefix(t, "#") {
			continue
		}
		if strings.TrimSpace(strings.TrimLeft(t, "# ")) == heading {
			at = i
			break
		}
	}
	if at < 0 {
		return nil, fmt.Errorf("no heading %q", heading)
	}

	var rows [][]string
	inTable, header := false, false
	for _, l := range lines[at+1:] {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "#") {
			break
		}
		if !strings.HasPrefix(t, "|") {
			// A blank line inside a table ends it. Prose before the table is
			// skipped, which is how the heading and the table can be separated
			// by the paragraph that introduces them.
			if inTable {
				break
			}
			continue
		}
		inTable = true
		cells := splitRow(t)
		if isDivider(cells) {
			continue
		}
		// The first row is the column names, not a member.
		if !header {
			header = true
			continue
		}
		rows = append(rows, cells)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("no table rows under heading %q", heading)
	}
	return rows, nil
}

func splitRow(line string) []string {
	t := strings.Trim(line, "|")
	parts := strings.Split(t, "|")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.TrimSpace(p))
	}
	return out
}

func isDivider(cells []string) bool {
	for _, c := range cells {
		if strings.Trim(c, "-: ") != "" {
			return false
		}
	}
	return len(cells) > 0
}

var backticked = regexp.MustCompile("^`([a-z0-9_.]+)`$")

// checkTable is rule 2: a table that is the reference for a set has one row
// per member.
//
// Two ways of reading it, chosen by what the first column holds. Where the
// cells are backticked identifiers the rows are compared to the members by
// name, which says exactly which one is missing. Where they are English, as
// the lint rules table is ("**NOT NULL column added with no default**"), only
// the count can be compared. The weaker half is still worth having: the page
// that documents all seventeen rules states no number anywhere, so rule 1 is
// silent over it and an eighteenth rule would be documented nowhere at all.
func checkTable(file, setName string, rows [][]string, members []string) (finding, bool) {
	have := map[string]bool{}
	ids := 0
	for _, r := range rows {
		if len(r) == 0 {
			continue
		}
		if m := backticked.FindStringSubmatch(r[0]); m != nil {
			have[m[1]] = true
			ids++
		}
	}

	if ids == len(rows) && ids > 0 {
		var missing []string
		for _, m := range members {
			if !have[m] {
				missing = append(missing, m)
			}
		}
		if len(missing) > 0 {
			return finding{
				file: file, line: 1,
				why: fmt.Sprintf(
					"is the reference table for the %s and does not list %s. It has %d rows for %d members.",
					setName, strings.Join(missing, ", "), len(rows), len(members)),
				text: "add a row per member, or move the table out from under this heading",
			}, true
		}
		return finding{}, false
	}

	if len(rows) != len(members) {
		return finding{
			file: file, line: 1,
			why: fmt.Sprintf(
				"is the reference table for the %s and has %d rows for %d members: %s.",
				setName, len(rows), len(members), strings.Join(members, ", ")),
			text: "add a row per member, or move the table out from under this heading",
		}, true
	}
	return finding{}, false
}
