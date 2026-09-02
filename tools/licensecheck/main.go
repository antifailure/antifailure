// Command licensecheck proves the repository's license is still detectable and
// that the enterprise carve out is still stated where a reader will find it.
//
// WHY THIS EXISTS. LICENSE held the MIT text followed by one paragraph naming
// the `ee` carve out. The paragraph was correct, it was doing real legal work,
// and it made the license undetectable: GitHub identifies a license with
// Licensee, which normalizes the file and compares it to known texts, and
// appended prose is folded into that comparison. The repository reported
// NOASSERTION for an MIT project. No license in the sidebar, absent from the
// license filter, and to anyone scanning it, all rights reserved.
//
// That is the shape of failure this whole tree keeps finding: a true statement
// in the wrong place, with nothing watching. It was invisible because nobody
// looks at a badge, and it would come back the first time somebody appends a
// well meant sentence to LICENSE.
//
// So two properties, checked together, because either alone is a trap:
//
//  1. LICENSE is the MIT text and NOTHING else. Adding a line to it breaks
//     detection, and the one line somebody would add is a pointer to the carve
//     out, which is exactly the mistake this replaces.
//
//  2. The carve out is still stated outside LICENSE, and every source file
//     under ee/ still says it is not MIT. Property 1 alone would be satisfied
//     by deleting the carve out entirely, which is the failure that matters.
//     A license nobody can detect is a smaller problem than a paid directory
//     nobody knows is paid.
//
// The MIT text is embedded rather than fetched, because a gate that needs the
// network is a gate that goes red on a bad morning for reasons that are not
// about the repository.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// mit is the canonical text, with the copyright line left out: that line is
// the one part a project is meant to change, and Licensee ignores it when
// matching. Everything else must be present and nothing may follow it.
const mit = `Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.`

// The header every source file under ee/ carries.
const eeHeader = "Antifailure Enterprise License"

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	problems, checkedEE := check(root)
	for _, p := range problems {
		fmt.Fprintln(os.Stderr, "licensecheck:", p)
	}
	if len(problems) > 0 {
		os.Exit(1)
	}
	// What was actually checked, not what this usually checks. Saying "and the
	// ee carve out is stated" on a tree where it was skipped would be a gate
	// reporting on itself rather than on the repository, which is the failure
	// the whole file is about.
	if checkedEE {
		fmt.Println("licensecheck: LICENSE is detectable MIT and the ee carve out is stated")
		return
	}
	fmt.Println("licensecheck: LICENSE is detectable MIT; there is no ee/ in this tree to carve out")
}

// check returns the problems found, and whether the ee/ half ran at all.
func check(root string) (problems []string, checkedEE bool) {
	problems = append(problems, checkLicense(root)...)

	// A tree with no ee/ is a community build, which the edition boundary job
	// makes by deleting the directory and building from what is left. There is
	// no carve out to state when there is nothing carved out, and failing there
	// would be a gate complaining that a deliberate tree is not the other one.
	//
	// Skipped LOUDLY rather than silently. A skip that prints nothing reads
	// exactly like a pass, and the whole reason this gate exists is that
	// nobody noticed a license going undetected for months.
	if _, err := os.Stat(filepath.Join(root, "ee")); os.IsNotExist(err) {
		return problems, false
	}

	problems = append(problems, checkCarveOut(root)...)
	problems = append(problems, checkEEHeaders(root)...)
	return problems, true
}

// checkLicense holds LICENSE to the MIT text with nothing appended.
func checkLicense(root string) []string {
	raw, err := os.ReadFile(filepath.Join(root, "LICENSE"))
	if err != nil {
		return []string{"LICENSE could not be read: " + err.Error()}
	}
	body := string(raw)
	if !strings.Contains(body, mit) {
		return []string{
			"LICENSE is not the MIT text. GitHub matches it with Licensee against known " +
				"license texts, so a reworded license is an undetected one.",
		}
	}
	// Nothing after the final line. This is the actual regression: the file
	// stays recognisably MIT to a human and stops matching for a tool.
	after := strings.TrimSpace(body[strings.Index(body, mit)+len(mit):])
	if after != "" {
		return []string{fmt.Sprintf(
			"LICENSE has %d characters after the MIT text, beginning %q. Licensee folds "+
				"appended prose into the text it compares, so this makes the repository "+
				"report NOASSERTION: no license in the sidebar and none in the license "+
				"filter. Put it in LICENSING.md, which is where the ee carve out lives "+
				"for exactly this reason.",
			len(after), first(after, 60))}
	}
	return nil
}

// checkCarveOut holds the statement that ee/ is not MIT to somewhere a reader
// reaches, now that LICENSE cannot carry it.
func checkCarveOut(root string) []string {
	var problems []string
	// README.md is in this list for a reason that is not obvious, and it is
	// about the RELEASE ARCHIVE rather than the repository.
	//
	// tools/release/build.sh copies exactly LICENSE and README.md into the
	// tarball. It does not copy LICENSING.md, and it should not: the archive
	// contains no ee/ code, so a document about ee/ inside it would point at a
	// directory that is not there. That is the dangling reference the old
	// LICENSE already had, naming ee/LICENSE.md in an archive with no ee/.
	//
	// Which leaves README.md as the only file that both states the carve out
	// AND travels with the release. Somebody rewriting the README could drop
	// that sentence without ever thinking about the tarball, and the carve out
	// would silently stop shipping. So it is held here.
	for _, f := range []string{"LICENSING.md", "README.md", "ee/LICENSE.md", "ee/README.md"} {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f)))
		if err != nil {
			problems = append(problems, f+" is missing, and it is one of the places the "+
				"ee carve out is stated now that LICENSE cannot state it")
			continue
		}
		if !strings.Contains(string(raw), "ee") || !mentionsEnterprise(string(raw)) {
			problems = append(problems, f+" no longer says that ee/ is separately licensed")
		}
	}
	return problems
}

func mentionsEnterprise(s string) bool {
	return strings.Contains(s, eeHeader) || strings.Contains(s, "Enterprise License")
}

// checkEEHeaders holds every source file under ee/ to saying so itself.
//
// The per file header is what a license scanner walking the tree finds, and it
// is what makes the carve out survive a file being copied out of context.
func checkEEHeaders(root string) []string {
	var problems []string
	skip := regexp.MustCompile(`(^|/)(node_modules|dist|build)(/|$)`)
	err := filepath.Walk(filepath.Join(root, "ee"), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel := filepath.ToSlash(path)
		if info.IsDir() {
			if skip.MatchString(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		switch filepath.Ext(path) {
		case ".go", ".ts", ".tsx":
		default:
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		head := raw
		if len(head) > 400 {
			head = head[:400]
		}
		if !strings.Contains(string(head), eeHeader) {
			problems = append(problems, rel+" carries no enterprise license header, so a "+
				"reader who opens only this file is told nothing about the boundary")
		}
		return nil
	})
	if err != nil {
		problems = append(problems, "walking ee/: "+err.Error())
	}
	return problems
}

func first(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
