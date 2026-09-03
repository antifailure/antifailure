// Command motioncheck refuses a shipped page that animates forever while the
// person looking at it does nothing.
//
// THE RULE. If a piece of UI is animating on an infinite loop while the user is
// idle, it is wrong. There is no carve out for a genuine real time event: a
// live score, an open connection and a running job all communicate the same
// state with a static chip, a colour or a number, none of which throb for
// attention. A looping animation is the single most reliable tell of an
// interface nobody art directed, and this repository has shipped three of them.
//
// WHY IT READS THE BUILD AND NOT THE SOURCE. This is the whole point of the
// tool and it was learned the expensive way. Two people read
// `www/app/globals.css` on the same day, both saw one `infinite` rule left in
// it, and both concluded it was harmless because nothing in that file appeared
// to use it. It was not harmless. `.hero-scan` was rendered by
// `HeroFilm.tsx`, on the front page, and had been scanning on a seven second
// loop behind the headline for weeks. The source says which rules exist. Only
// the render says which of them land on an element. A gate reading the source
// would have agreed with the wrong answer, twice.
//
// THE var() HOP, which a naive scan misses. Tailwind v4 does not write
// `animation: pulse 2s ... infinite` into a rule. It writes
// `--animate-pulse: pulse 2s ... infinite` once in its theme block, whether or
// not anything uses it, and then `.animate-pulse{animation:var(--animate-pulse)}`
// only if the class is actually used. So searching the stylesheet for
// declarations containing "infinite" finds the theme variable, which is a false
// positive present in every build, and misses the real usage, which is a false
// negative. Both directions are wrong. This resolves one level of var() before
// deciding, and ignores custom property definitions themselves, so the theme
// block is silent and a used `animate-pulse` is caught.
//
// WHAT COUNTS AS A FINDING.
//
//   - a rule in the built stylesheet whose animation runs unbounded, directly
//     or through a var()
//   - an inline style attribute in the built HTML doing the same, which is how
//     this site writes most of its artwork animations, and which no stylesheet
//     scan would ever see
//   - `animate-pulse` or `animate-ping` on any element, the same ban stated as
//     a class name, previously enforced by people remembering to grep
//
// An exemption needs a written reason, so that an animation which genuinely
// earns a loop is admitted by a decision somebody reads rather than by the gate
// being quietly widened.
package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// exempt admits an animation that has earned its loop. The key is the selector
// for a stylesheet finding and the class name for a class finding, exactly as
// the report prints it, so an exemption is copied from a failure rather than
// guessed at. Empty on purpose: nothing on this site has earned one yet.
var exempt = map[string]string{}

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	code, out := run(root)
	fmt.Print(out)
	os.Exit(code)
}

// builtSite is one prerendered application to inspect.
type builtSite struct {
	name string
	css  []string
	html []string
}

func run(root string) (int, string) {
	sites, missing := discover(root)
	if len(sites) == 0 && missing == "" {
		// No application at all, which is what an empty tree looks like. A gate
		// that reads a render and finds no render has checked nothing, and
		// reporting that as success is how a check that never ran becomes a
		// check that always passes.
		return 1, "motioncheck: no Next application found. Each one is a directory " +
			"with a next.config file, and each needs `npm run build` before this " +
			"gate has anything to read.\n"
	}
	if missing != "" {
		return 1, fmt.Sprintf("motioncheck: %s is not built. Run `npm run build` in %s "+
			"first; this gate reads the render, not the source, and has nothing to "+
			"say about an unbuilt tree.\n\nIt names the application rather than "+
			"skipping it, because skipping was how the console went unchecked.\n",
			missing, missing)
	}

	var findings []string
	files, elements := 0, 0

	for _, s := range sites {
		vars := map[string]string{}
		for _, f := range s.css {
			body, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			collectVars(string(body), vars)
		}

		// Which classes actually land on an element, gathered before the
		// stylesheet is judged, because a rule existing is not an element
		// carrying it. See usedClasses for what this prevents.
		used := map[string]bool{}
		for _, f := range s.html {
			body, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			files++
			collectClasses(string(body), used)
			n, found := scanHTML(s.name, rel(root, f), string(body))
			elements += n
			findings = append(findings, found...)
		}

		for _, f := range s.css {
			body, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			files++
			findings = append(findings, scanCSS(s.name, rel(root, f), string(body), vars, used)...)
		}
	}

	sort.Strings(findings)
	findings = dedupe(findings)

	if len(findings) > 0 {
		var b strings.Builder
		for _, f := range findings {
			fmt.Fprintf(&b, "%s\n", f)
		}
		verb := "animations run"
		if len(findings) == 1 {
			verb = "animation runs"
		}
		fmt.Fprintf(&b, "\n%d %s forever while the reader does nothing.\n", len(findings), verb)
		b.WriteString("Tie the motion to a state change and let it stop, or remove it. " +
			"A static chip says the same thing.\nIf one genuinely earns a loop, add " +
			"it to exempt in tools/motioncheck/main.go with the reason.\n")
		return 1, b.String()
	}

	return 0, fmt.Sprintf("motioncheck: %d built files, %d elements, 0 animations that never stop\n",
		files, elements)
}

// discover finds the prerendered output of each application. Next writes the
// same pages twice, to .next/server/app and to out/ for a static export, so
// only one is read: reporting a finding twice for one element reads as two
// defects.
//
// An application that is not built is an ERROR rather than a site to leave
// out, and that is the whole point of this function returning a second value.
// It used to append a site only when something was found on disk, so a missing
// build removed the application from the run in silence. In CI the console was
// installed and built AFTER this ran, so for as long as this check has claimed
// to cover the console it has been reading nothing there and reporting success
// over the files it did read. It said 42 built files instead of 64 and exited
// zero. A gate that is green about a surface it never opened is worse than no
// gate, because it is quoted.
func discover(root string) ([]builtSite, string) {
	var sites []builtSite
	for _, app := range nextApps(root) {
		s := builtSite{name: app}
		s.css, _ = filepath.Glob(filepath.Join(root, app, ".next", "static", "chunks", "*.css"))
		s.html = prerendered(filepath.Join(root, app, ".next", "server", "app"))
		if len(s.css) == 0 && len(s.html) == 0 {
			return nil, app
		}
		sites = append(sites, s)
	}
	return sites, ""
}

// nextApps names every Next application in the tree, by looking for the config
// file that makes one, rather than by holding a list.
//
// A list was the actual defect. This read `[]string{"www", "console"}` while
// the console was not built when it ran, so the console was dropped in silence.
// A hardcoded list has the same shape as that bug even after the silence is
// fixed: it is a claim about the repository that nothing checks, and the day a
// third application is added the gate keeps passing and stops meaning
// anything. Reading the tree cannot go stale.
func nextApps(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		for _, name := range []string{"next.config.ts", "next.config.js", "next.config.mjs"} {
			if _, err := os.Stat(filepath.Join(root, e.Name(), name)); err == nil {
				out = append(out, e.Name())
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// prerendered returns every .html under dir, at any depth.
//
// This was three fixed globs at one, two and three path segments, which was
// correct for what existed when it was written and silently short by one the
// moment a page nested deeper. The admin console nests four, as in
// admin/customers/users/organization, so the fixed list would have walked past
// the pages and reported a clean run over files it never opened. A walk cannot
// go out of date when somebody adds a directory.
func prerendered(dir string) []string {
	var out []string
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && strings.HasSuffix(path, ".html") {
			out = append(out, path)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

var (
	// A custom property definition, which is a declaration of intent and not a
	// use of it. Tailwind's theme block defines --animate-pulse in every build.
	varDef = regexp.MustCompile(`(--[a-zA-Z0-9_-]+)\s*:\s*([^;}]+)`)

	// A rule, captured as its selector and its body, from a minified stylesheet.
	cssRule = regexp.MustCompile(`([^{}]+)\{([^{}]*)\}`)

	// The two properties that can make an animation repeat without end.
	animDecl = regexp.MustCompile(`(?:^|;)\s*(animation|animation-iteration-count)\s*:\s*([^;]+)`)

	varUse = regexp.MustCompile(`var\(\s*(--[a-zA-Z0-9_-]+)\s*[^)]*\)`)

	classAttr  = regexp.MustCompile(`class="([^"]*)"`)
	styleAttr  = regexp.MustCompile(`style="([^"]*)"`)
	bannedAnim = regexp.MustCompile(`\banimate-(pulse|ping)\b`)
)

func collectVars(css string, into map[string]string) {
	for _, m := range varDef.FindAllStringSubmatch(css, -1) {
		into[m[1]] = m[2]
	}
}

// unbounded reports whether a declaration value ends up repeating forever,
// following one level of var() so that Tailwind's indirection is not a hiding
// place. One level is enough for every case this site produces and keeps the
// answer explainable; a chain deeper than that would be worth reporting on its
// own.
func unbounded(value string, vars map[string]string) bool {
	if strings.Contains(value, "infinite") {
		return true
	}
	for _, m := range varUse.FindAllStringSubmatch(value, -1) {
		if strings.Contains(vars[m[1]], "infinite") {
			return true
		}
	}
	return false
}

// collectClasses records every class that a built page actually puts on an
// element.
func collectClasses(html string, into map[string]bool) {
	for _, m := range classAttr.FindAllStringSubmatch(html, -1) {
		for _, c := range strings.Fields(m[1]) {
			into[c] = true
		}
	}
}

// simpleClass returns the class name of a selector that is exactly one class,
// and false for anything else. Only that shape can be checked against the
// render with certainty; a compound or pseudo selector is left to be reported.
func simpleClass(selector string) (string, bool) {
	s := strings.TrimSpace(selector)
	if !strings.HasPrefix(s, ".") {
		return "", false
	}
	name := s[1:]
	if name == "" || strings.ContainsAny(name, " .,:>+~[]#*()") {
		return "", false
	}
	return strings.ReplaceAll(name, `\`, ""), true
}

func scanCSS(site, path, css string, vars map[string]string, used map[string]bool) []string {
	var found []string
	for _, r := range cssRule.FindAllStringSubmatch(css, -1) {
		selector, body := strings.TrimSpace(r[1]), r[2]

		// The theme block is definitions, not uses. Skipping it here rather
		// than filtering the report keeps the count honest.
		if strings.HasPrefix(strings.TrimSpace(body), "--") && !animDecl.MatchString(body) {
			continue
		}

		// A rule can exist for a class no page uses. Tailwind v4 scans source
		// files as text, so it emitted `.animate-pulse` for the console off the
		// word inside the comment in ui.tsx explaining why that screen stopped
		// using it. Nothing carried the class; the rule was emitted by the
		// prose describing its removal. Reporting that is the same error this
		// tool exists to avoid, one level along: judging the stylesheet instead
		// of the render.
		if name, ok := simpleClass(selector); ok && !used[name] {
			continue
		}
		for _, d := range animDecl.FindAllStringSubmatch(";"+body, -1) {
			if !unbounded(d[2], vars) {
				continue
			}
			if reason, ok := exempt[selector]; ok {
				_ = reason
				continue
			}
			found = append(found, fmt.Sprintf(
				"%s %s: `%s` sets %s to run forever", site, path, selector, d[1]))
		}
	}
	return found
}

func scanHTML(site, path, html string) (int, []string) {
	var found []string
	elements := 0

	for _, m := range classAttr.FindAllStringSubmatch(html, -1) {
		elements++
		for _, b := range bannedAnim.FindAllStringSubmatch(m[1], -1) {
			name := "animate-" + b[1]
			if _, ok := exempt[name]; ok {
				continue
			}
			found = append(found, fmt.Sprintf(
				"%s %s: `%s` on an element, which throbs for attention forever", site, path, name))
		}
	}

	// Inline styles are how this site writes most of its artwork animation, so
	// a stylesheet-only gate would have missed the verdict card marquee
	// entirely. There is no selector to name here, so the animation name is the
	// identifier.
	for _, m := range styleAttr.FindAllStringSubmatch(html, -1) {
		style := strings.ReplaceAll(m[1], "&quot;", `"`)
		for _, d := range animDecl.FindAllStringSubmatch(";"+style, -1) {
			if !strings.Contains(d[2], "infinite") {
				continue
			}
			name := firstWord(d[2])
			if _, ok := exempt[name]; ok {
				continue
			}
			found = append(found, fmt.Sprintf(
				"%s %s: inline style runs `%s` forever", site, path, name))
		}
	}
	return elements, found
}

func firstWord(s string) string {
	for _, f := range strings.Fields(strings.TrimSpace(s)) {
		if f != "" {
			return f
		}
	}
	return strings.TrimSpace(s)
}

func dedupe(in []string) []string {
	var out []string
	for i, s := range in {
		if i == 0 || s != in[i-1] {
			out = append(out, s)
		}
	}
	return out
}

func rel(root, path string) string {
	if r, err := filepath.Rel(root, path); err == nil {
		return r
	}
	return path
}
