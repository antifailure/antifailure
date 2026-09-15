package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ConsoleCall is one tRPC procedure path the console asks the control plane for,
// or one call whose path this command could not read.
type ConsoleCall struct {
	File string
	Line int
	Path string
	Text string
}

// WHY THE CONSOLE NEEDS ITS OWN CHECK, and why it is this shape.
//
// The console reaches the control plane through query() and mutate() in
// console/lib/api.ts, which fetch `${BASE}/trpc/${path}`. The path is an
// ordinary string. Nothing on either side joins it to the procedure the server
// registers, so a path with no procedure behind it is not a compile error, not
// a type error and not a lint finding: it is a 404 at run time, on that screen
// only, for every visitor.
//
// That is not hypothetical. console/app/(app)/exits/page.tsx asked for
// `account.exits` from the day it was written. The server's accountRouter
// registers `context` and `close`. The name was a placeholder for one the
// enterprise side had not chosen yet, the enterprise side chose `context`, and
// nothing ever reported the gap. Every render of the data export and account
// deletion screen, which is also the primary button a lapsed customer is given,
// drew its error card instead. It shipped, and it stayed shipped, because no
// instrument in this repository read console call sites at all: routecheck
// walked www and the repository root and nothing else.
//
// The rule is the same inversion the www half uses. The console is not allowed
// to name a procedure the server does not register, and a path this command
// cannot read is a failure rather than a silence, because a gate that skips
// what it cannot parse reports a clean run over exactly the call site that was
// going to break.

var (
	// const EXITS_READ = "account.context";
	consoleConstRe = regexp.MustCompile(`\bconst\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*=\s*"([^"]+)"`)
	// query(EXITS_READ, ...) and mutate("account.close", ...)
	consoleCallRe = regexp.MustCompile(`\b(?:query|mutate)\s*(?:<[^()]*>)?\s*\(\s*([^,)]+)`)
	// A tRPC path: dotted lowerCamel segments, two or more.
	trpcPathRe = regexp.MustCompile(`^[a-z][A-Za-z0-9]*(?:\.[a-z][A-Za-z0-9]*)+$`)
	// A declared parameter, so that the transport layer is not read as a call
	// site: console/lib/api.ts declares query(path: string, ...) and
	// console/lib/admin.ts forwards its own path through mutate(path, ...).
	// Neither names a procedure; the paths are at their callers, which this
	// does read. Counting these as unreadable would have failed the gate on a
	// tree where nothing was wrong, which is the same defect as passing one
	// where something is.
	paramRe = regexp.MustCompile(`\b([A-Za-z_$][A-Za-z0-9_$]*)\s*:\s*(?:string|T)\b`)
)

// FindConsoleCalls reports every procedure path the console asks for, and every
// call whose path it could not resolve to a string.
//
// A path can be written at the call site or held in a const beside it, which is
// what the console does, so both are read. Anything else, a path assembled from
// an expression or arriving in a parameter, is returned as unresolved rather
// than ignored.
func FindConsoleCalls(consoleRoot string) (calls, unresolved, forwarders []ConsoleCall, err error) {
	err = filepath.WalkDir(consoleRoot, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", ".next", "out", ".git":
				return filepath.SkipDir
			}
			return nil
		}
		switch filepath.Ext(path) {
		case ".ts", ".tsx":
		default:
			return nil
		}
		f, openErr := os.Open(path)
		if openErr != nil {
			return openErr
		}
		// The read is what matters and a failed close on a read only file says
		// nothing, which is how the rest of tools/ writes it: surfacecheck,
		// sbomcheck, reltar and notices all discard it explicitly rather than
		// leaving errcheck to find an unchecked one.
		defer func() { _ = f.Close() }()

		// Two passes over the file's code, comments stripped first so a path in
		// a sentence is not read as a call and a call is not lost behind one.
		var lines []string
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		st := scanState{}
		for sc.Scan() {
			var code string
			code, st = stripComments(sc.Text(), st)
			lines = append(lines, code)
		}
		if scanErr := sc.Err(); scanErr != nil {
			return fmt.Errorf("%s: %w", path, scanErr)
		}

		consts := map[string]string{}
		params := map[string]bool{}
		for _, code := range lines {
			for _, m := range consoleConstRe.FindAllStringSubmatch(code, -1) {
				consts[m[1]] = m[2]
			}
			if strings.Contains(code, "function") || strings.Contains(code, "=>") {
				for _, m := range paramRe.FindAllStringSubmatch(code, -1) {
					params[m[1]] = true
				}
			}
		}
		rel := path
		if r, relErr := filepath.Rel(filepath.Dir(consoleRoot), path); relErr == nil {
			rel = r
		}
		for i, code := range lines {
			for _, m := range consoleCallRe.FindAllStringSubmatch(code, -1) {
				arg := strings.TrimSpace(m[1])
				// A declaration site reads `query<T>(path: string, ...)`, so the
				// captured argument carries its type. The name is what matters.
				if i := strings.IndexByte(arg, ':'); i > 0 && !strings.HasPrefix(arg, `"`) {
					arg = strings.TrimSpace(arg[:i])
				}
				got := ConsoleCall{File: rel, Line: i + 1, Text: strings.TrimSpace(code)}
				switch {
				case strings.HasPrefix(arg, `"`) && strings.HasSuffix(arg, `"`) && len(arg) > 1:
					got.Path = strings.Trim(arg, `"`)
				case consts[arg] != "":
					got.Path = consts[arg]
				case params[arg]:
					// The transport layer forwarding a path it was handed.
					forwarders = append(forwarders, got)
					continue
				default:
					unresolved = append(unresolved, got)
					continue
				}
				if !trpcPathRe.MatchString(got.Path) {
					// Not a procedure path. query() and mutate() are also the
					// names of local helpers in places, and a path is always
					// dotted, so this is a name rather than a route.
					continue
				}
				calls = append(calls, got)
			}
		}
		return nil
	})
	if err != nil {
		return nil, nil, nil, err
	}
	return calls, unresolved, forwarders, nil
}

var (
	// A declaration that holds procedure keys: a router, or a plain object of
	// procedures spread into one later.
	declRe = regexp.MustCompile(`\bconst\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*=\s*(?:(?:router|t\.router)\s*\(\s*)?\{\s*$`)
	// `tenants: router({` opens a nested router written in place.
	inlineRouterRe = regexp.MustCompile(`^\s*([A-Za-z_$][A-Za-z0-9_$]*)\s*:\s*(?:router|t\.router)\s*\(\s*\{\s*$`)
	// `context: orgProcedure(...` or `customers: customersRouter,`
	keyRe = regexp.MustCompile(`^\s*([A-Za-z_$][A-Za-z0-9_$]*)\s*:\s*(.*)$`)
	// `...organizationSettings,` spreads another declaration's keys in.
	spreadRe = regexp.MustCompile(`^\s*\.\.\.([A-Za-z_$][A-Za-z0-9_$]*)\s*,?\s*$`)
	// A value that is just another declaration's name.
	refRe = regexp.MustCompile(`^([A-Za-z_$][A-Za-z0-9_$]*)\s*,?$`)
)

// decl is one declaration that holds procedure keys. Keys are dotted, so a
// router written in place inside another one is held as "tenants.list".
type decl struct {
	keys    map[string]string // dotted key -> the value's text
	spreads []spread
}

// spread is one `...symbol` and the dotted namespace it was written in. The
// namespace matters: admin's nested audit router spreads auditChainRoutes, so
// those procedures are admin.audit.verify and not admin.verify, and a parser
// that dropped the prefix called the real one dead.
type spread struct {
	prefix string
	symbol string
}

// parseDecls reads every declaration in one file that holds procedure keys.
//
// Line based with a stack, because the shapes in this repository are all of
// them written one key per line: a nested router opens with `tenants: router({`
// and a mounted one reads `customers: customersRouter,`. The stack is what makes
// arbitrary depth work, and admin goes four segments deep
// (admin.customers.notes.list).
func parseDecls(text string, into map[string]*decl) {
	st := scanState{}
	var current string
	depth := 0
	var stack []struct {
		name  string
		depth int
	}
	for _, raw := range strings.Split(text, "\n") {
		var code string
		code, st = stripComments(raw, st)
		if current == "" {
			if m := declRe.FindStringSubmatch(code); m != nil {
				current = m[1]
				if into[current] == nil {
					into[current] = &decl{keys: map[string]string{}}
				}
				depth = 1
				stack = stack[:0]
			}
			continue
		}
		prefix := ""
		for _, s := range stack {
			prefix += s.name + "."
		}
		switch {
		case spreadRe.MatchString(code):
			into[current].spreads = append(into[current].spreads, spread{prefix: prefix, symbol: spreadRe.FindStringSubmatch(code)[1]})
		case inlineRouterRe.MatchString(code):
			name := inlineRouterRe.FindStringSubmatch(code)[1]
			stack = append(stack, struct {
				name  string
				depth int
			}{name, depth})
		default:
			if m := keyRe.FindStringSubmatch(code); m != nil {
				into[current].keys[prefix+m[1]] = strings.TrimSpace(m[2])
			}
		}
		depth += strings.Count(code, "{") - strings.Count(code, "}")
		for len(stack) > 0 && depth <= stack[len(stack)-1].depth {
			stack = stack[:len(stack)-1]
		}
		if depth <= 0 {
			current = ""
			depth = 0
			stack = stack[:0]
		}
	}
}

// ParseServerProcedures reads every procedure the control plane registers, as
// dotted "mount.procedure" paths, plus the set of mounts, from the API source.
//
// It follows the appRouter mount table rather than guessing from file names: a
// router's variable name and the name the console addresses it by are not the
// same thing, and only the table says which is which. It follows spreads, since
// a router assembled as `...organizationSettings` holds every procedure of that
// object. It walks the whole tree, since a mounted router can be declared
// outside routers/ and admin's is, two directories away.
//
// Two parser mistakes were caught here by pointing it at a tree where nothing
// was wrong and requiring silence. Counting parentheses as well as braces ended
// a router at its first `.query(async ({ ctx }) => {` and called 58 live call
// sites dead. Reading only one level of nesting called every admin path dead.
// Both would have been invisible on a tree that really was broken.
func ParseServerProcedures(apiSrc string) (procedures map[string]bool, mounts map[string]bool, err error) {
	var files []string
	if walkErr := filepath.WalkDir(apiSrc, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", "dist", ".git":
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) == ".ts" && !strings.HasSuffix(path, ".test.ts") {
			files = append(files, path)
		}
		return nil
	}); walkErr != nil {
		return nil, nil, walkErr
	}
	decls := map[string]*decl{}
	for _, file := range files {
		b, readErr := os.ReadFile(file)
		if readErr != nil {
			return nil, nil, readErr
		}
		parseDecls(string(b), decls)
	}

	// keysFor resolves one declaration's own keys plus everything it spreads in.
	var keysFor func(symbol string, seen map[string]bool) map[string]string
	keysFor = func(symbol string, seen map[string]bool) map[string]string {
		out := map[string]string{}
		d, ok := decls[symbol]
		if !ok || seen[symbol] {
			return out
		}
		seen[symbol] = true
		for _, sp := range d.spreads {
			for k, v := range keysFor(sp.symbol, seen) {
				out[sp.prefix+k] = v
			}
		}
		for k, v := range d.keys {
			out[k] = v
		}
		return out
	}

	procedures = map[string]bool{}
	mounts = map[string]bool{}
	// register walks one router's keys under a path prefix, following a key
	// whose value is another declaration's name.
	var register func(symbol, prefix string, seen map[string]bool)
	register = func(symbol, prefix string, seen map[string]bool) {
		if seen[symbol+"@"+prefix] {
			return
		}
		seen[symbol+"@"+prefix] = true
		for key, value := range keysFor(symbol, map[string]bool{}) {
			path := prefix + key
			procedures[path] = true
			if m := refRe.FindStringSubmatch(strings.TrimSpace(value)); m != nil {
				if _, isDecl := decls[m[1]]; isDecl {
					mounts[path] = true
					register(m[1], path+".", seen)
				}
			}
			// Every dotted key opens a namespace of its own.
			if i := strings.LastIndex(path, "."); i > 0 {
				mounts[path[:i]] = true
			}
		}
	}
	root := keysFor("appRouter", map[string]bool{})
	if len(root) == 0 {
		return nil, nil, fmt.Errorf("%s: no appRouter mount table could be read. Without it every console path would read as valid, which is the silence this check exists to remove", apiSrc)
	}
	for mount, value := range root {
		mounts[mount] = true
		if m := refRe.FindStringSubmatch(strings.TrimSpace(value)); m != nil {
			register(m[1], mount+".", map[string]bool{})
		}
	}
	if len(procedures) == 0 {
		return nil, nil, fmt.Errorf("%s: the mount table was read and no procedures came back. An empty set would pass every console call site", apiSrc)
	}
	return procedures, mounts, nil
}

// CheckConsoleCalls refuses a console call site naming a procedure the server
// does not register, and a call site whose path could not be read.
func CheckConsoleCalls(calls, unresolved []ConsoleCall, procedures, mounts map[string]bool) error {
	var b strings.Builder
	if len(unresolved) > 0 {
		fmt.Fprintf(&b, "%d console call site(s) build a procedure path this command cannot read:\n", len(unresolved))
		for _, c := range unresolved {
			fmt.Fprintf(&b, "  %s:%d: %s\n", c.File, c.Line, c.Text)
		}
		b.WriteString("\nPass the path as a string literal, or as a const beside the call, so that this\n")
		b.WriteString("command can prove the control plane registers it.\n\n")
	}
	var dead []string
	for _, c := range calls {
		if procedures[c.Path] {
			continue
		}
		seg := strings.Split(c.Path, ".")
		if !mounts[seg[0]] {
			// Not a control plane router at all.
			continue
		}
		dead = append(dead, fmt.Sprintf("  %s:%d: %s, and %s registers no %s", c.File, c.Line, c.Path, seg[0], strings.Join(seg[1:], ".")))
	}
	if len(dead) > 0 {
		fmt.Fprintf(&b, "%d console call site(s) name a procedure the control plane does not register:\n%s\n", len(dead), strings.Join(dead, "\n"))
		b.WriteString("\nA path with no procedure behind it is a 404 at run time on that screen only.\n")
		b.WriteString("It is not a compile error and no type sees it, which is how the exits page\n")
		b.WriteString("shipped asking for account.exits while the server registered account.context.\n")
	}
	if b.Len() > 0 {
		return fmt.Errorf("%s", strings.TrimRight(b.String(), "\n"))
	}
	return nil
}
