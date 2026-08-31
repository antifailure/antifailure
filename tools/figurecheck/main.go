// Command figurecheck refuses a number that reads as a measurement unless
// somebody has written down where it came from.
//
// THE DEFECT THIS EXISTS FOR. The marketing site rendered `fid 87%` on
// /product/architecture and /product/safe-state. It was a fidelity score, it
// was invented, and nothing in the product computes it. Its twin scene had the
// same number taken out with a comment saying it does not exist, and this one
// kept it, which is the shape of most of these: two things that should have
// moved together and only one did.
//
// What made it survive is the reason this gate reads source. The number is
// drawn client side, so `curl` on either page finds no "87" anywhere and every
// cheap audit comes back clean. A gate over the built HTML would have missed
// it completely. So this reads the source, where the string is a literal.
//
// WHAT IT LOOKS FOR, and the scoping is the whole design. Not every integer: a
// gap, a viewBox, an array index and a duration in milliseconds are not claims
// and a gate that flagged them would be muted within a day. It looks for the
// shapes that only ever mean a measurement in copy:
//
//   - a percentage, `87%` or `81 percent`
//   - a count against a denominator, `17 of 21` or `3 out of 5`
//
// A bare `410ms` is deliberately not one of them. It is a duration in an
// example, it carries no ratio, and including it would have put forty more
// rows in the allowlist for no extra safety. A percentage is different: in
// user-facing copy it is almost always a claim about how good or how complete
// something is, which is exactly the claim nobody can check.
//
// WHERE IT LOOKS. Percentages are also how CSS is written, and the site has
// nearly three hundred of them in gradients, keyframes and widths. So
// `className` and `style` are removed before the scan, and a line carrying
// other CSS or SVG geometry is skipped by the rules in cssContext below. That
// list is a denylist of STYLING shapes rather than an allowlist of copy
// shapes, deliberately: a new prop that carries words is then covered the day
// it is written, and only a new way of spelling CSS needs a line here. A
// styling shape that is missed produces a false positive, which is noisy and
// safe. The opposite arrangement fails silently, which is how the site got
// here.
//
// Comments are skipped, and that is a real difference from prosecheck, which
// reads them closely. There the comment is prose a person wrote and the tell
// hides in it. Here the question is only what a reader of the page sees, and a
// comment renders nowhere. This file's own paragraphs above are the proof:
// they say "87%" repeatedly and none of it reaches a buyer.
//
// THE ALLOWLIST IS THE IMPORTANT HALF. tools/docs/figure-exemptions.tsv names
// a file and a figure and demands a reason, the same way
// tools/docs/manifest-exemptions.tsv does, and an entry that stops being
// needed fails the gate so the list cannot rot. The reason is the mechanism.
// A number from a shaped example says so; a number computed at build time says
// where from; a number quoting somebody else's published report names it. A
// number nobody can source cannot be given a truthful reason, and the answer
// then is to delete it rather than to write a false one. `fid 87%` would have
// needed a row here and there was nothing true to put in it.
package main

import (
	"bufio"
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

// trees are the sources scanned. The console shows a customer their own data
// and makes no claims about the product, so it is not here yet.
var trees = []string{"www"}

var exts = map[string]bool{".ts": true, ".tsx": true}

const exemptionsPath = "tools/docs/figure-exemptions.tsv"

// figure matches the two shapes that read as a measurement.
var figure = regexp.MustCompile(`\d[\d.,]*\s*%|\b\d[\d.,]*\s+percent\b|\b\d[\d.,]*\s+out\s+of\s+\d[\d.,]*\b|\b\d[\d.,]*\s+of\s+\d[\d.,]*\b`)

// classAttr and styleAttr are where CSS legitimately lives in this codebase.
// Removed before the scan rather than used to skip the line, so a figure
// elsewhere on the same line is still found.
var (
	classAttr = regexp.MustCompile(`(?s)className\s*=\s*(?:"[^"]*"|\{(?:[^{}]|\{[^{}]*\})*\})`)
	styleAttr = regexp.MustCompile(`(?s)style\s*=\s*\{\{(?:[^{}]|\{[^{}]*\})*\}\}`)
)

// CSS is masked out of a line before the scan rather than used to skip the
// line, so a figure sitting beside a style on the same line is still found.
//
// An earlier draft skipped any line holding a brace and a colon, on the theory
// that this is what a CSS declaration looks like. It is also what every object
// literal looks like, and it silently swallowed `share: "18%"` and four other
// real data rows. A rule that suppresses a true finding is worse than one that
// reports a false one, so each of these now names the styling shape it masks
// and masks only that.
//
// Widening this list widens what the gate ignores, so it lives in reviewed
// source rather than in the data file. The allowlist is where a CLAIM is
// excused, with a reason; this is only where a STYLE is recognised. Keeping
// the two apart is what stops a number being laundered through the cheaper one.
var (
	// A Tailwind arbitrary value: max-w-[46%], mx-[10%].
	tailwindArbitrary = regexp.MustCompile(`\[[^\]]*%[^\]]*\]`)
	// The value of a CSS property, in a style object or a template literal:
	// bottom: "100%", width: "38%", fill: ${GREEN}, opacity: 0.28.
	cssDeclaration = regexp.MustCompile(`(?i)\b(?:width|height|top|bottom|left|right|inset|size|gap|flex|flex-basis|opacity|fill|stroke|stroke-width|strokeWidth|strokeDasharray|stopColor|stop-color|stopOpacity|offset|offsetDistance|margin|padding|max-width|maxWidth|min-width|minWidth|max-height|maxHeight|min-height|minHeight|font-size|fontSize|line-height|lineHeight|border-radius|borderRadius|background|background-position|background-size|backgroundSize|backgroundPosition|translate|scale|rotate)\s*:\s*[^,;}\n]*`)
	// A keyframe selector: the percentages that open a rule.
	keyframeSelector = regexp.MustCompile(`(?m)^\s*\d[\d.,%\s]*%(?:\s*,\s*\d[\d.,%\s]*%)*\s*\{`)
)

// cssLines are the shapes that never carry copy at all, so the whole line goes.
var cssLines = []*regexp.Regexp{
	regexp.MustCompile(`(?i)linear-gradient|radial-gradient|conic-gradient`),
	regexp.MustCompile(`(?i)mask-image|transform-origin|transform-box|\btransform\s*:`),
	regexp.MustCompile(`(?i)\bcalc\(`),
	regexp.MustCompile(`(?i)\bstyle\.\w+\s*=`),
	// An SVG gradient stop or a coordinate given as a percentage.
	regexp.MustCompile(`(?i)\b(?:x1|y1|x2|y2|cx|cy|fx|fy|r|offset|startOffset|width|height)\s*=\s*"`),
}

type finding struct {
	file, fig, line string
	num             int
}

type key struct{ file, fig string }

func main() {
	root := flag.String("root", ".", "repository root")
	flag.Parse()
	if args := flag.Args(); len(args) > 0 {
		*root = args[0]
	}
	if err := run(*root, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "\nfigurecheck: %v\n", err)
		os.Exit(1)
	}
}

func run(root string, out io.Writer) error {
	files, err := collect(root)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		// A gate that checks nothing reports green, which is the failure this
		// whole tool is a response to.
		return fmt.Errorf("found no TypeScript under %v in %s, so this check is looking in the wrong place", trees, root)
	}

	exempt, err := readExemptions(filepath.Join(root, exemptionsPath))
	if err != nil {
		return err
	}
	used := map[key]bool{}

	var found []finding
	for _, f := range files {
		body, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			return err
		}
		for _, hit := range Check(f, string(body)) {
			k := key{hit.file, hit.fig}
			if _, ok := exempt[k]; ok {
				used[k] = true
				continue
			}
			found = append(found, hit)
		}
	}

	sort.Slice(found, func(i, j int) bool {
		if found[i].file != found[j].file {
			return found[i].file < found[j].file
		}
		return found[i].num < found[j].num
	})
	report := func(format string, args ...any) { _, _ = fmt.Fprintf(out, format, args...) }

	for _, f := range found {
		report("%s:%d  %q reads as a measurement and nothing says where it comes from.\n    %s\n",
			f.file, f.num, f.fig, strings.TrimSpace(f.line))
	}

	// An excused figure that has gone is reported too, so the list cannot rot
	// into a set of permissions nobody remembers granting.
	var stale []string
	for k, reason := range exempt {
		if !used[k] {
			stale = append(stale, fmt.Sprintf(
				"%s: %q in %s is excused and no longer there (%s)", exemptionsPath, k.fig, k.file, reason))
		}
	}
	sort.Strings(stale)
	for _, s := range stale {
		report("%s\n", s)
	}

	report("figurecheck: %d files, %d sourced %s, %d unsourced\n",
		len(files), len(used), plural(len(used), "figure", "figures"), len(found))

	if n := len(found) + len(stale); n > 0 {
		if len(found) > 0 {
			report("\nWrite the number's source into %s, or delete the number. "+
				"A figure nobody can source is a claim to a buyer that nothing stands behind.\n", exemptionsPath)
		}
		return fmt.Errorf("%d %s the site cannot account for", n, plural(n, "figure", "figures"))
	}
	return nil
}

// Check reports the unaccounted figures in one file. Exported so a test can
// drive it without touching the filesystem.
func Check(name, body string) []finding {
	var out []finding
	inBlockComment := false

	for i, raw := range strings.Split(body, "\n") {
		line := raw
		if inBlockComment {
			if idx := strings.Index(line, "*/"); idx >= 0 {
				inBlockComment = false
				line = line[idx+2:]
			} else {
				continue
			}
		}
		// Comments render nowhere, so they cannot mislead a reader.
		if open := strings.Index(line, "/*"); open >= 0 {
			if close := strings.Index(line[open:], "*/"); close >= 0 {
				line = line[:open] + line[open+close+2:]
			} else {
				inBlockComment = true
				line = line[:open]
			}
		}
		if idx := strings.Index(line, "//"); idx >= 0 {
			line = line[:idx]
		}
		if strings.HasPrefix(strings.TrimSpace(line), "*") {
			continue
		}

		if isCSS(line) {
			continue
		}
		line = styleAttr.ReplaceAllString(classAttr.ReplaceAllString(line, " "), " ")
		line = tailwindArbitrary.ReplaceAllString(line, " ")
		line = keyframeSelector.ReplaceAllString(line, " ")
		line = cssDeclaration.ReplaceAllString(line, " ")
		for _, m := range figure.FindAllString(line, -1) {
			out = append(out, finding{file: name, num: i + 1, fig: normalise(m), line: raw})
		}
	}
	return out
}

func isCSS(line string) bool {
	for _, re := range cssLines {
		if re.MatchString(line) {
			return true
		}
	}
	return false
}

// normalise collapses the whitespace inside a figure so that "17 of 21" and
// "17  of  21" are one entry in the allowlist rather than two.
func normalise(s string) string { return strings.Join(strings.Fields(s), " ") }

// readExemptions reads the allowlist: file, figure, reason, tab separated.
func readExemptions(path string) (map[key]string, error) {
	out := map[key]string{}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	defer f.Close()

	s := bufio.NewScanner(f)
	for n := 1; s.Scan(); n++ {
		line := s.Text()
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 3 || strings.TrimSpace(parts[2]) == "" {
			return nil, fmt.Errorf("%s:%d: want three tab separated fields, the third a reason: %q", path, n, line)
		}
		out[key{strings.TrimSpace(parts[0]), normalise(parts[1])}] = strings.TrimSpace(parts[2])
	}
	return out, s.Err()
}

func collect(root string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, dir := range trees {
		base := filepath.Join(root, dir)
		err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				name := d.Name()
				if path != base && (name == "node_modules" || name == "dist" || name == "out" ||
					name == "vendor" || strings.HasPrefix(name, ".")) {
					return fs.SkipDir
				}
				return nil
			}
			if !exts[filepath.Ext(path)] {
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

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
