// Command classcheck refuses a Tailwind class that is written on an element and
// then loses to another class on the same element, so it changes nothing.
//
// THE DEFECT THIS EXISTS FOR. `cn` in this repository is a plain join, not
// tailwind-merge. A className passed to a component does not replace the
// component's own class, it lands beside it, and the cascade picks between
// them. At equal specificity the cascade picks whichever Tailwind emitted last,
// which has nothing to do with which the author wrote. So a class can read in a
// diff exactly as though it works, review as though it works, and do nothing.
//
// Four of these were live on the marketing site at once:
//
//   - SiteHeader marked the current page with text-black over a text-black/70
//     default. text-black is emitted first, so it lost, and the navigation
//     marked nothing at all. It was invisible for years because hover:text-black
//     is a pseudo-class and outranks the loser, so the nav looked correct the
//     instant anyone touched it and failed only at rest.
//   - The same cn() call dimmed the other triggers with text-gray-new-50, which
//     is emitted after text-black/70 and did work. One conditional in one call
//     worked and the one on the next line did not.
//   - TwinFilm asked for #1A1A1A on a live environment card. Every arbitrary
//     colour is emitted before every text-black/N and in file order against
//     another arbitrary colour, so it lost to the component's grey and the
//     card's live state and idle state rendered identically.
//   - SafeState marked the sanitized column of the masking panel green. Same
//     cause, and the only marker on that panel not carrying its signal.
//
// The rule people reach for, that a darker class wins, is true only inside the
// text-black/N family, where Tailwind sorts by ascending opacity. It does not
// generalise. Between two arbitrary colours the winner is whichever the
// stylesheet happens to name first, and TwinFilm is a darkening intent that
// lost. So the order is not something to reason about. It is something to read
// out of the emitted stylesheet, which is what this does.
//
// WHAT IT READS. The built site, not the source, and that is deliberate. The
// question is not which classes a file mentions, it is which classes land on
// one element together, and only the renderer knows that. www prerenders to
// static HTML, so the answer is on disk after a build with no browser needed.
//
// WHAT IT COMPARES. Only unconditional utilities: a rule whose selector is a
// bare class, outside any @media, with no pseudo-class. A hover, focus or
// breakpoint variant is supposed to override the base and is not in the race.
// Classes are grouped by the property they set, so two classes only conflict
// when they set the same one.
//
// WHERE IT IS BLIND, and this is a floor rather than a ceiling. It sees only
// what prerenders. A menu that opens on hover, a film frame at t=5s, and any
// branch behind client state never reach the HTML, so a losing class there is
// not caught. The live sweep that found the four above used a browser for
// exactly that reason. This catches the resting state of every page on every
// build, which is where the navigation defect lived.
package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// rule is one unconditional Tailwind utility: the property it sets and the byte
// offset it was emitted at. Later offset wins at equal specificity.
type rule struct {
	prop string
	at   int
}

// exemption is one deliberately kept pair, with the reason it is deliberate.
type exemption struct {
	dead   string
	winner string
	reason string
	line   int
	used   bool
}

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	code, out := run(root)
	fmt.Print(out)
	os.Exit(code)
}

func run(root string) (int, string) {
	var b strings.Builder

	cssFiles, _ := filepath.Glob(filepath.Join(root, "www", ".next", "static", "chunks", "*.css"))
	htmlFiles, _ := filepath.Glob(filepath.Join(root, "www", ".next", "server", "app", "*.html"))
	more, _ := filepath.Glob(filepath.Join(root, "www", ".next", "server", "app", "*", "*.html"))
	htmlFiles = append(htmlFiles, more...)
	more, _ = filepath.Glob(filepath.Join(root, "www", ".next", "server", "app", "*", "*", "*.html"))
	htmlFiles = append(htmlFiles, more...)

	if len(cssFiles) == 0 || len(htmlFiles) == 0 {
		fmt.Fprintf(&b, "classcheck: no built site under www/.next. Build it first:\n\n    (cd www && npm run build)\n\n")
		return 1, b.String()
	}

	rules := map[string]rule{}
	for _, f := range cssFiles {
		src, err := os.ReadFile(f)
		if err != nil {
			fmt.Fprintf(&b, "classcheck: %s: %v\n", f, err)
			return 1, b.String()
		}
		for k, v := range parseCSS(string(src)) {
			if _, seen := rules[k]; !seen {
				rules[k] = v
			}
		}
	}

	exemptPath := filepath.Join(root, "tools", "docs", "classcheck-exemptions.tsv")
	exempt, err := readExemptions(exemptPath)
	if err != nil {
		fmt.Fprintf(&b, "classcheck: %s: %v\n", exemptPath, err)
		return 1, b.String()
	}

	type finding struct {
		file, dead, winner, prop, sample string
	}
	var findings []finding
	elements := 0

	for _, f := range htmlFiles {
		src, err := os.ReadFile(f)
		if err != nil {
			fmt.Fprintf(&b, "classcheck: %s: %v\n", f, err)
			return 1, b.String()
		}
		for _, attr := range classAttrs(string(src)) {
			elements++
			toks := strings.Fields(attr)
			byProp := map[string][]int{}
			for idx, tok := range toks {
				r, ok := rules[tok]
				if !ok {
					continue
				}
				byProp[r.prop] = append(byProp[r.prop], idx)
			}
			for prop, idxs := range byProp {
				if len(idxs) < 2 {
					continue
				}
				// `cn` joins its arguments in order and React writes the
				// attribute verbatim, so a component's own classes come first
				// and a call site's override comes last. The class written LAST
				// is the one whose author meant it to apply.
				//
				// A default losing to an override is how overriding is supposed
				// to look and is not reported: an earlier version of this gate
				// flagged all 119 of those on this site and would have been
				// muted within a day. Only the inverse is a defect, where a
				// class written last loses to one written before it, so the
				// author's intent is on the element and does nothing.
				last := idxs[len(idxs)-1]
				winner := idxs[0]
				for _, k := range idxs[1:] {
					if rules[toks[k]].at > rules[toks[winner]].at {
						winner = k
					}
				}
				// A strict comparison, so the same class written twice on one
				// element is redundant rather than dead and is not reported.
				if rules[toks[winner]].at <= rules[toks[last]].at {
					continue
				}
				if markExempt(exempt, toks[last], toks[winner]) {
					continue
				}
				findings = append(findings, finding{
					file: rel(root, f), dead: toks[last], winner: toks[winner], prop: prop, sample: sample(attr),
				})
			}
		}
	}

	sort.Slice(findings, func(i, j int) bool {
		if findings[i].dead != findings[j].dead {
			return findings[i].dead < findings[j].dead
		}
		return findings[i].file < findings[j].file
	})

	// An allowlist that stops matching is a claim nobody is checking any more.
	var stale []string
	for _, e := range exempt {
		if !e.used {
			stale = append(stale, fmt.Sprintf("%s:%d %s loses to %s", rel(root, exemptPath), e.line, e.dead, e.winner))
		}
	}

	seen := map[string]bool{}
	shown := 0
	for _, f := range findings {
		k := f.file + "|" + f.dead + "|" + f.winner
		if seen[k] {
			continue
		}
		seen[k] = true
		shown++
		fmt.Fprintf(&b, "%s\n    %s is written here and never applies: %s wins on %s\n    %s\n",
			f.file, f.dead, f.winner, f.prop, f.sample)
	}

	if shown > 0 || len(stale) > 0 {
		for _, s := range stale {
			fmt.Fprintf(&b, "%s\n    exempted but nothing matches it any more, so delete the row\n", s)
		}
		fmt.Fprintf(&b, "\nclasscheck: %d files, %d elements, %d classes that never apply, %d stale exemptions\n",
			len(htmlFiles), elements, shown, len(stale))
		fmt.Fprintf(&b, "\nA class written beside another that sets the same property does not replace it.\n")
		fmt.Fprintf(&b, "Choose one with a ternary so only one is ever emitted, or give the component a\n")
		fmt.Fprintf(&b, "prop for the variant. Reaching for a value that happens to win is not a fix.\n")
		return 1, b.String()
	}

	fmt.Fprintf(&b, "classcheck: %d files, %d elements, 0 classes that never apply\n", len(htmlFiles), elements)
	return 0, b.String()
}

func markExempt(ex []*exemption, dead, winner string) bool {
	for _, e := range ex {
		if e.dead == dead && e.winner == winner {
			e.used = true
			return true
		}
	}
	return false
}

func rel(root, p string) string {
	r, err := filepath.Rel(root, p)
	if err != nil {
		return p
	}
	return r
}

func sample(attr string) string {
	a := strings.Join(strings.Fields(attr), " ")
	if len(a) > 150 {
		a = a[:150] + "..."
	}
	return a
}

var classAttrRe = regexp.MustCompile(`class="([^"]*)"`)

func classAttrs(html string) []string {
	var out []string
	for _, m := range classAttrRe.FindAllStringSubmatch(html, -1) {
		out = append(out, m[1])
	}
	return out
}

// parseCSS records every unconditional single-class rule and the property it
// sets, keyed by class name and ordered by the byte offset it was emitted at.
//
// Rules inside @media, @supports and @container are skipped. A breakpoint or a
// feature query is meant to override a base and is not part of the silent race.
// Skipping @supports is safe for ordering even though those blocks are what a
// modern browser actually applies: Tailwind emits each utility's base rule and
// its @supports twin adjacently, in the same order, so if A is emitted before B
// then A's twin is emitted before B's twin and the winner is unchanged.
// @layer is descended into, because a layer is unconditional.
func parseCSS(src string) map[string]rule {
	type frame struct{ conditional bool }
	var stack []frame
	conditional := 0
	out := map[string]rule{}

	i := 0
	for i < len(src) {
		switch src[i] {
		case '@':
			j := i + 1
			for j < len(src) && src[j] != '{' && src[j] != ';' {
				j++
			}
			name := src[i+1:]
			if k := strings.IndexAny(name, " \t\n({;"); k >= 0 {
				name = name[:k]
			}
			if j < len(src) && src[j] == '{' {
				cond := name == "media" || name == "supports" || name == "container" ||
					name == "keyframes" || name == "font-face" || name == "property"
				stack = append(stack, frame{conditional: cond})
				if cond {
					conditional++
				}
				i = j + 1
				continue
			}
			i = j + 1
			continue
		case '}':
			if n := len(stack); n > 0 {
				if stack[n-1].conditional {
					conditional--
				}
				stack = stack[:n-1]
			}
			i++
			continue
		case ' ', '\t', '\n', '\r', ';':
			i++
			continue
		}

		// A rule: selector up to the opening brace, then its declarations.
		start := i
		j := i
		for j < len(src) && src[j] != '{' && src[j] != '}' && src[j] != '@' {
			j++
		}
		if j >= len(src) || src[j] != '{' {
			// Not a rule after all. Hand the character back to the switch so an
			// at-rule is never walked as though it were top level, which is the
			// bug that made an earlier version of this parse three rules out of
			// nine hundred and report a clean site.
			i = j
			if i == start {
				i++
			}
			continue
		}
		selector := strings.TrimSpace(src[start:j])
		k := j + 1
		braces := 1
		for k < len(src) && braces > 0 {
			if src[k] == '{' {
				braces++
			} else if src[k] == '}' {
				braces--
			}
			k++
		}
		body := src[j+1 : max(j+1, k-1)]
		if conditional == 0 {
			if name, ok := plainClass(selector); ok {
				for _, prop := range props(body) {
					key := name + "\x00" + prop
					if _, seen := out[key]; !seen {
						out[key] = rule{prop: prop, at: start}
					}
				}
			}
		}
		i = k
	}

	flat := map[string]rule{}
	for key, r := range out {
		name := key[:strings.IndexByte(key, 0)]
		prev, seen := flat[name]
		if !seen || r.at < prev.at {
			flat[name] = r
		}
	}
	return flat
}

// plainClass reports the class name when the selector is exactly one class with
// no pseudo-class, no combinator and no second simple selector.
func plainClass(sel string) (string, bool) {
	sel = strings.TrimSpace(sel)
	if !strings.HasPrefix(sel, ".") {
		return "", false
	}
	var name strings.Builder
	for i := 1; i < len(sel); i++ {
		c := sel[i]
		if c == '\\' {
			if i+1 < len(sel) {
				i++
				name.WriteByte(sel[i])
				continue
			}
			return "", false
		}
		if c == ':' || c == ' ' || c == '>' || c == '+' || c == '~' || c == ',' || c == '.' || c == '[' || c == '*' {
			return "", false
		}
		name.WriteByte(c)
	}
	if name.Len() == 0 {
		return "", false
	}
	return name.String(), true
}

var interesting = map[string]bool{
	"color":            true,
	"background-color": true,
	"border-color":     true,
	"fill":             true,
	"stroke":           true,
}

func props(body string) []string {
	var out []string
	for _, decl := range strings.Split(body, ";") {
		c := strings.IndexByte(decl, ':')
		if c < 0 {
			continue
		}
		p := strings.TrimSpace(decl[:c])
		if interesting[p] {
			out = append(out, p)
		}
	}
	return out
}

func readExemptions(path string) ([]*exemption, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []*exemption
	sc := bufio.NewScanner(f)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(sc.Text(), "\t")
		if len(parts) < 3 || strings.TrimSpace(parts[2]) == "" {
			return nil, fmt.Errorf("line %d: an exemption needs a dead class, the class that wins, and a reason", n)
		}
		out = append(out, &exemption{
			dead:   strings.TrimSpace(parts[0]),
			winner: strings.TrimSpace(parts[1]),
			reason: strings.TrimSpace(parts[2]),
			line:   n,
		})
	}
	return out, sc.Err()
}
