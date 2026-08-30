package build

import (
	"bufio"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"
)

// Ignore decides what a build context leaves out.
//
// It implements .dockerignore rather than reaching for a library, because the
// dependency allowlist is closed and the rules are small enough to state:
// patterns are relative to the context root, a leading ! makes an exception,
// the last pattern that matches a path wins, * stops at a separator and **
// does not, and a pattern that matches a directory excludes everything under
// it.
//
// Getting this wrong is not cosmetic. A missed rule sends node_modules or a
// .git directory to the daemon, which turns a two second build into a two
// minute one; a rule applied too eagerly drops a source file and produces an
// image that fails at runtime for a reason nothing in the build output
// explains. The second is why a pattern that cannot be compiled is refused
// rather than skipped.
type Ignore struct {
	rules []ignoreRule
}

type ignoreRule struct {
	// pattern is the text as written, for error messages and explanations.
	pattern string
	re      *regexp.Regexp
	negated bool
	// clean is the pattern with the leading !, the trailing slash, and any
	// leading ./ removed. Kept because `pattern` is the raw line, which is
	// what a message quotes, and comparing paths against a string that still
	// carries a `!` silently matches nothing.
	clean string
	// dirOnly is set for a pattern ending in a slash, which matches only a
	// directory.
	dirOnly bool
}

// alwaysSent are the files Docker requires regardless of the ignore file.
//
// The daemon reads the Dockerfile out of the context, and it reads
// .dockerignore to report what it excluded. A .dockerignore that excluded
// either of them would produce a build that fails with "Dockerfile not found"
// while the file sits plainly in the repository.
var alwaysSent = map[string]bool{".dockerignore": true}

// ParseIgnore reads a .dockerignore file.
//
// An empty or missing file is not an error; it is the common case, and it
// yields an Ignore that excludes nothing.
func ParseIgnore(r io.Reader) (*Ignore, error) {
	ig := &Ignore{}
	if r == nil {
		return ig, nil
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	line := 0
	for sc.Scan() {
		line++
		raw := strings.TrimSpace(sc.Text())
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		rule, err := compileIgnore(raw)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		ig.rules = append(ig.rules, rule)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return ig, nil
}

func compileIgnore(raw string) (ignoreRule, error) {
	r := ignoreRule{pattern: raw}
	p := raw
	if strings.HasPrefix(p, "!") {
		r.negated = true
		p = strings.TrimSpace(p[1:])
	}
	if strings.HasSuffix(p, "/") {
		r.dirOnly = true
		p = strings.TrimSuffix(p, "/")
	}
	// Patterns are relative to the context root whether or not they say so.
	p = strings.TrimPrefix(p, "./")
	p = strings.TrimPrefix(p, "/")
	p = path.Clean(p)
	if p == "." || p == "" {
		return r, fmt.Errorf("%q selects the whole context, which would exclude everything", raw)
	}
	r.clean = p
	re, err := regexp.Compile("^" + globToRegexp(p) + "$")
	if err != nil {
		return r, fmt.Errorf("%q is not a usable pattern: %w", raw, err)
	}
	r.re = re
	return r, nil
}

// globToRegexp converts a shell style pattern to an anchored expression.
//
// The distinction that matters is between * and **: a single star stops at a
// separator, so src/* is one level, and a double star does not, so src/** is
// the whole subtree. Treating them alike is the mistake that silently sends a
// nested node_modules to the daemon.
func globToRegexp(p string) string {
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		switch c := p[i]; c {
		case '*':
			if i+1 < len(p) && p[i+1] == '*' {
				i++
				// A trailing slash after ** means "this directory and
				// everything under it", so the separator is part of the
				// optional group rather than required after it.
				if i+1 < len(p) && p[i+1] == '/' {
					i++
					b.WriteString("(?:.*/)?")
				} else {
					b.WriteString(".*")
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		case '/':
			b.WriteByte('/')
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	return b.String()
}

// Excluded reports whether a path is left out of the context, and which
// pattern decided.
//
// The path is slash separated and relative to the context root. isDir says
// whether it is a directory, which a pattern ending in a slash needs to know.
//
// The last matching pattern wins, which is what makes an exception work: a
// .dockerignore that excludes node_modules and then re-includes
// !node_modules/.keep is read in order, and the exception is later.
func (ig *Ignore) Excluded(p string, isDir bool) (bool, string) {
	if alwaysSent[p] {
		return false, ""
	}
	excluded, by := false, ""
	for _, r := range ig.rules {
		if r.dirOnly && !isDir && !r.matchesAncestorOf(p) {
			continue
		}
		if !r.matches(p) {
			continue
		}
		excluded, by = !r.negated, r.pattern
	}
	return excluded, by
}

// MayHoldAnException reports whether some path inside a directory could be
// re-included by a negated rule.
//
// The walk skips an excluded directory whole, which is what makes ignoring
// node_modules fast rather than merely correct. It is also how an exception
// inside an excluded directory stops working: `deploy` followed by
// `!deploy/docker/app.Dockerfile` reads correctly, the last matching rule
// wins, and it never runs, because the walk pruned `deploy` before it ever
// looked at a file underneath it. The Dockerfile is then missing from the
// context and the daemon reports that it cannot locate a file that is plainly
// on disk.
//
// So a directory is only pruned when nothing below it could come back. This is
// deliberately generous: it asks whether the negated pattern's text begins
// inside this directory, rather than trying to decide which concrete paths it
// would match. Being wrong here costs a walk of a subtree that turns out to
// have nothing in it; being wrong the other way silently drops a file somebody
// asked for.
func (ig *Ignore) MayHoldAnException(dir string) bool {
	prefix := strings.TrimSuffix(dir, "/") + "/"
	for _, r := range ig.rules {
		if !r.negated {
			continue
		}
		p := r.clean
		if strings.HasPrefix(p, prefix) {
			return true
		}
		// A pattern with a wildcard in it can reach anywhere below its first
		// literal segment, and `**` can reach anywhere at all.
		if strings.Contains(p, "*") {
			literal := p
			if i := strings.IndexAny(p, "*?["); i >= 0 {
				literal = p[:i]
			}
			if literal == "" || strings.HasPrefix(prefix, literal) || strings.HasPrefix(literal, prefix) {
				return true
			}
		}
	}
	return false
}

// matches reports whether the rule covers the path itself or any directory
// above it. A pattern that names a directory excludes everything inside it,
// and the walk sees the children as their own paths.
func (r ignoreRule) matches(p string) bool {
	if r.re.MatchString(p) {
		return true
	}
	return r.matchesAncestorOf(p)
}

func (r ignoreRule) matchesAncestorOf(p string) bool {
	for i := strings.LastIndex(p, "/"); i > 0; i = strings.LastIndex(p[:i], "/") {
		if r.re.MatchString(p[:i]) {
			return true
		}
	}
	// The first segment, which the loop above cannot reach.
	if i := strings.Index(p, "/"); i > 0 {
		return r.re.MatchString(p[:i])
	}
	return false
}

// Patterns returns the rules as written, for af build explain.
func (ig *Ignore) Patterns() []string {
	out := make([]string, 0, len(ig.rules))
	for _, r := range ig.rules {
		out = append(out, r.pattern)
	}
	return out
}
