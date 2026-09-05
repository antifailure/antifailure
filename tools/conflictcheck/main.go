// Command conflictcheck refuses a repository carrying an unresolved merge.
//
// THE FAILURE THAT EARNED IT. A pull request merged with literal conflict
// markers committed into docs/src/content/docs/reference/control-plane.md:
// `<<<<<<< HEAD`, `=======`, and `>>>>>>> 406000d0` around six table rows, two
// of which were the same variable described twice. It reached main, it was in
// the release being prepared, and it was published documentation for the one
// page an operator reads to configure the product.
//
// Every gate this repository has was green about it. Nothing looks for the
// markers, and everything that could have noticed was answering a different
// question: markdown does not fail to parse, prosecheck reads punctuation,
// varcheck and config-docs.test.ts ask whether a variable is DOCUMENTED, and a
// row inside a conflict block still reads as documented to all three. The one
// gate that did go red, wirecheck, went red for a consequence rather than the
// cause: it reported a variable as documented with no way to set it, which sent
// two people looking at Terraform. The cause was four lines of git output in a
// table.
//
// WHY IT IS NOT PART OF ANOTHER TOOL. scanrepo walks the same tree with the
// same skip list and would have been the cheap place to put this. It answers
// "does this repository carry a live credential", using the engine's own
// detector so the check and the proxy cannot disagree. This answers "is any
// file in this repository a merge somebody did not finish". Two questions, and
// a tool that answers two questions reports one number for both.
//
// WHAT IT CANNOT CATCH, said here rather than discovered later. It sees the
// markers git writes and nothing else. A conflict resolved by keeping one side
// whole, when the correct resolution was the union of both, leaves no marker
// and is invisible to this and to git: that is the merge-only defect this
// repository has already shipped, and the instrument for it is diffing the
// resolution against BOTH parents rather than reading it. A file that
// legitimately contains a marker, a document about resolving conflicts for
// instance, needs a row in the exemption file, because a rule with no way to
// say "this one is deliberate" gets deleted the first time it is wrong.
package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// markers are what git writes into a file it could not merge. The trailing
// space matters on two of the three: a markdown heading of seven equals signs
// is not a conflict, and neither is a line of angle brackets in a diff quoted
// inside a code fence. `=======` is exact-match for the same reason: a table
// separator row or a rule of equals signs is longer or shorter than seven.
var markers = []struct {
	prefix string
	exact  bool
}{
	{prefix: "<<<<<<< "},
	{prefix: "||||||| "},
	{prefix: ">>>>>>> "},
	{prefix: "=======", exact: true},
}

// skipDirs are not source, and one of them is the reason this is not a plain
// grep: .git holds real conflict markers in its own objects and index.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "dist": true, "bin": true,
	".next": true, "vendor": true, "test-results": true, "playwright-report": true,
	"out": true, "coverage": true,
}

// exemptionFile lists paths that may carry a marker, with the reason. Same
// mechanism as tools/docs/figure-exemptions.tsv and the wiring exemptions: the
// reason is mandatory, and a row matching nothing is reported, so the file
// cannot rot into a permanent allowance for a file that has since been fixed.
const exemptionFile = "tools/docs/conflict-exemptions.tsv"

const maxFile = 4 << 20

type finding struct {
	path string
	line int
	text string
}

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}

	exempt, err := readExemptions(filepath.Join(root, exemptionFile))
	if err != nil {
		fmt.Fprintf(os.Stderr, "conflictcheck: %v\n", err)
		os.Exit(1)
	}

	var findings []finding
	files := 0
	used := map[string]bool{}

	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > maxFile || !info.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)

		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer func() { _ = f.Close() }()

		files++
		s := bufio.NewScanner(f)
		s.Buffer(make([]byte, 0, 64*1024), maxFile)
		n := 0
		for s.Scan() {
			n++
			line := s.Text()
			if !isMarker(line) {
				continue
			}
			if _, ok := exempt[rel]; ok {
				used[rel] = true
				continue
			}
			findings = append(findings, finding{path: rel, line: n, text: line})
		}
		return nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "conflictcheck: %v\n", err)
		os.Exit(1)
	}

	// A row that no longer matches anything is dead, and dead rows in an
	// exemption file read as protection that is not there.
	var stale []string
	for path := range exempt {
		if !used[path] {
			stale = append(stale, path)
		}
	}
	sort.Strings(stale)

	if len(findings) == 0 && len(stale) == 0 {
		fmt.Printf("conflictcheck: %d files, no unresolved merge, %d exemptions all still needed\n",
			files, len(exempt))
		return
	}

	for _, f := range findings {
		fmt.Printf("%s:%d carries a merge conflict marker\n    %s\n", f.path, f.line, f.text)
	}
	for _, p := range stale {
		fmt.Printf("%s is exempt in %s and carries no marker, so the row can go\n", p, exemptionFile)
	}
	if len(findings) > 0 {
		fmt.Printf("\nconflictcheck: %d files, %d unresolved merge markers\n", files, len(findings))
		fmt.Printf("Finish the merge. If both sides were meant to survive, the resolution is the\n")
		fmt.Printf("union of them, and neither git nor the compiler can tell you that: diff the\n")
		fmt.Printf("result against BOTH parents rather than reading it.\n")
	}
	os.Exit(1)
}

func isMarker(line string) bool {
	for _, m := range markers {
		if m.exact {
			if line == strings.TrimSuffix(m.prefix, " ") {
				return true
			}
			continue
		}
		if strings.HasPrefix(line, m.prefix) {
			return true
		}
	}
	return false
}

// readExemptions reads path<TAB>reason. A missing file is not an error: the
// honest state today is that nothing needs exempting.
func readExemptions(path string) (map[string]string, error) {
	out := map[string]string{}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	for i, line := range strings.Split(string(b), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[1]) == "" {
			return nil, fmt.Errorf("%s:%d has no reason. An exemption with no argument behind it cannot be told apart from somebody silencing a finding they did not understand", path, i+1)
		}
		out[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
	}
	return out, nil
}

// osWriteFile is os.WriteFile, named so the test can write a fixture without
// importing os for one call.
func osWriteFile(path string, b []byte, perm os.FileMode) error {
	return os.WriteFile(path, b, perm)
}
