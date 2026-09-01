// Command statuscheck holds docs/plan/STATUS.md to the rule it states about
// itself.
//
// WHY THIS EXISTS. STATUS.md opens by saying every component carries one of
// three states "and nothing else". Four rows carried a fourth word, and they
// carried THREE DIFFERENT fourth words: `partial` twice, `built` once, `mixed`
// once. Three spellings of one idea is worse than breaking the rule once,
// because a reader cannot tell whether they mean three different things, and
// `built` is not a point on that scale at all.
//
// That file is the page this project points at when somebody asks "does it do
// X yet". It is the answer to a due diligence question, so a word in it that
// nobody defined is a claim nobody can check. Nothing read it before this.
//
// The rule this enforces is narrow on purpose, and it is the file's own rule:
//
//  1. Every numbered row's state is one of the four defined words.
//  2. Every one of those four words is actually defined in the table at the
//     top, so the definitions and the usage cannot drift apart.
//  3. A `mixed` row names at least two of the three base states in its prose,
//     because `mixed` without saying mixed of what is the fourth word all
//     over again under a new spelling.
//
// It deliberately does NOT try to decide whether a `proven` row is really
// proven. No gate can read a sentence. What it can do is refuse a vocabulary
// that lets a row mean nothing, which is how `built` survived.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// statusFile is the one document this tool is about.
const statusFile = "docs/plan/STATUS.md"

// states are the words a row may carry. The tool asserts each one is defined
// in the file rather than trusting this list, so adding a word here without
// documenting it fails too.
var states = []string{"proven", "written", "planned", "mixed"}

// base are the three points on the scale. `mixed` is not one of them: it is a
// statement that a row spans some of them, which is why a mixed row has to
// name which.
var base = []string{"proven", "written", "planned"}

// numberedRow matches a component row: a table row whose first cell begins
// with a sub-phase number, such as "| 13.5 Audit export | proven | ... |".
//
// Anchored on the number because STATUS.md contains several other tables, and
// a gate that failed on the gate table's "enforced" or on a prose table would
// be a gate somebody turns off.
var numberedRow = regexp.MustCompile(`^\|\s*([0-9]+\.[0-9]+[^|]*)\|([^|]*)\|(.*)$`)

// definitionRow matches the state table at the top: | **proven** | ... |
var definitionRow = regexp.MustCompile(`^\|\s*\*\*([a-z]+)\*\*\s*\|`)

func main() {
	root := flag.String("root", ".", "repository root")
	flag.Parse()
	if args := flag.Args(); len(args) > 0 {
		*root = args[0]
	}
	if err := run(*root, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "\nstatuscheck: %v\n", err)
		os.Exit(1)
	}
}

func run(root string, out io.Writer) error {
	body, err := os.ReadFile(filepath.Join(root, statusFile))
	if err != nil {
		return fmt.Errorf("reading %s: %w", statusFile, err)
	}
	lines := strings.Split(string(body), "\n")

	defined := map[string]bool{}
	for _, line := range lines {
		if m := definitionRow.FindStringSubmatch(line); m != nil {
			defined[m[1]] = true
		}
	}
	var undefined []string
	for _, st := range states {
		if !defined[st] {
			undefined = append(undefined, st)
		}
	}
	if len(undefined) > 0 {
		sort.Strings(undefined)
		return fmt.Errorf("%s accepts %s but its own table at the top defines neither, "+
			"so a row could carry a word the file never explains",
			statusFile, strings.Join(undefined, " and "))
	}

	allowed := map[string]bool{}
	for _, st := range states {
		allowed[st] = true
	}

	var wrong, vague []string
	rows := 0
	mixed := 0

	for i, line := range lines {
		m := numberedRow.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		state := strings.TrimSpace(m[2])
		// The state cell is sometimes bolded for emphasis in this file.
		state = strings.TrimSpace(strings.Trim(state, "*"))
		if state == "" || strings.HasPrefix(state, "---") {
			continue
		}
		rows++
		where := fmt.Sprintf("%s:%d", statusFile, i+1)
		component := strings.TrimSpace(m[1])

		if !allowed[state] {
			wrong = append(wrong, fmt.Sprintf("%s  %s carries %q", where, component, state))
			continue
		}
		if state != "mixed" {
			continue
		}
		mixed++
		named := 0
		for _, b := range base {
			if strings.Contains(m[3], b) {
				named++
			}
		}
		if named < 2 {
			vague = append(vague, fmt.Sprintf(
				"%s  %s is mixed and its prose names %d of the three states, so it does not say mixed of what",
				where, component, named))
		}
	}

	if rows == 0 {
		return fmt.Errorf("%s has no numbered component rows, so the matcher has stopped matching "+
			"and this gate is checking nothing", statusFile)
	}

	if len(wrong) > 0 || len(vague) > 0 {
		if len(wrong) > 0 {
			report(out, "\nstatuscheck: a state this file does not define.\n")
			for _, w := range wrong {
				report(out, "  %s\n", w)
			}
			report(out, "  Use one of: %s\n", strings.Join(states, ", "))
		}
		if len(vague) > 0 {
			report(out, "\nstatuscheck: a mixed row that does not say what it is mixed of.\n")
			for _, v := range vague {
				report(out, "  %s\n", v)
			}
			report(out, "  Name at least two of proven, written and planned in the prose.\n")
		}
		return fmt.Errorf("%d row(s) with a state this file does not define, %d mixed row(s) that do not say of what",
			len(wrong), len(vague))
	}

	report(out, "statuscheck: %d component rows, %d of them mixed, every state defined and every mixed row says of what\n",
		rows, mixed)
	return nil
}

// report writes a line. The verdict is the exit code, not the report, so a
// broken pipe on stdout must not change whether the build fails.
func report(out io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(out, format, args...)
}
