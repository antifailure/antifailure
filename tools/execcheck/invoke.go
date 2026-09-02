package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// invocation is one place a command line runs a file this repository tracks.
type invocation struct {
	// script is the tracked path of the file being run.
	script string
	// source is the workflow or justfile that runs it, and line is where.
	source string
	line   int
	// text is the token as written, so the reader can find it by eye. It is
	// not always equal to script: a workflow says `tools/site/check-tls.sh`
	// and the tracked path is the same here, but a step with a
	// working-directory says a shorter path than the one git records.
	text string
}

// workflows is where the pipelines live. Every *.yml under it is read.
const workflows = ".github/workflows"

// invocations finds every tracked file that a workflow or a recipe runs by
// path.
//
// It refuses an empty result from either surface separately, rather than only
// from the pair. A workflow directory that has been renamed and a justfile that
// has stopped using recipes are different failures, and a single count would
// let one of them hide behind the other.
func invocations(root string, modes map[string]string) ([]invocation, error) {
	index := suffixIndex(modes)

	var out []invocation

	entries, err := os.ReadDir(filepath.Join(root, workflows))
	if os.IsNotExist(err) {
		// A missing directory and an empty one are the same answer, and it is
		// the loud one: this tool is looking somewhere the pipelines are not.
		entries = nil
	} else if err != nil {
		return nil, fmt.Errorf("reading %s: %w", workflows, err)
	}
	files := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || (!strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".yaml")) {
			continue
		}
		body, err := os.ReadFile(filepath.Join(root, workflows, name))
		if err != nil {
			return nil, fmt.Errorf("reading %s/%s: %w", workflows, name, err)
		}
		files++
		out = append(out, found(workflows+"/"+name, workflowShell(string(body)), index)...)
	}
	if files == 0 {
		return nil, fmt.Errorf("found no workflow under %s, so this check is looking in the wrong place", workflows)
	}

	body, err := os.ReadFile(filepath.Join(root, "justfile"))
	if err != nil {
		return nil, fmt.Errorf("reading justfile: %w", err)
	}
	recipes := justShell(string(body))
	if len(recipes) == 0 {
		return nil, fmt.Errorf("found no recipe body in the justfile, so this check is looking in the wrong place")
	}
	out = append(out, found("justfile", recipes, index)...)

	sort.Slice(out, func(i, j int) bool {
		if out[i].source != out[j].source {
			return out[i].source < out[j].source
		}
		return out[i].line < out[j].line
	})
	return out, nil
}

// found turns one file's shell lines into the invocations they contain.
func found(source string, lines []shellLine, index map[string][]string) []invocation {
	var out []invocation
	for _, l := range commandLines(lines) {
		for _, tok := range commandTokens(l.text) {
			for _, script := range resolve(tok, index) {
				out = append(out, invocation{script: script, source: source, line: l.num, text: tok})
			}
		}
	}
	return out
}

// shellLine is one line of shell, and where it came from.
type shellLine struct {
	num  int
	text string
}

// suffixIndex maps every tracked path, and every suffix of it that starts at a
// path separator, to the files that carry it.
//
// The suffix is what makes a `working-directory:` or a `cd` unnecessary here.
// A step that sets working-directory to www and runs `scripts/build.sh` is
// naming www/scripts/build.sh, and the alternative to matching on the suffix is
// tracking the directory through the shell, which gatecheck does and which is
// the most delicate code in this repository. This tool does not need the answer
// to be unique. It needs to know that the token names a file whose bit matters,
// and if two files in the tree end in scripts/build.sh then both of them are
// run by a line of that shape somewhere and both should carry the bit.
func suffixIndex(modes map[string]string) map[string][]string {
	index := map[string][]string{}
	for p, mode := range modes {
		if mode != executable && mode != regular {
			continue
		}
		index[p] = append(index[p], p)
		for i := 0; i < len(p); i++ {
			if p[i] == '/' {
				index[p[i+1:]] = append(index[p[i+1:]], p)
			}
		}
	}
	for k := range index {
		sort.Strings(index[k])
	}
	return index
}

// notAToken rejects a token that cannot be a path in this repository.
//
// The shell writes a value it works out at run time with `$`, a glob with `*`
// or `?`, and just writes an interpolation with `{{`. None of them is a path
// this can resolve, and guessing at one would be worse than skipping it.
var notAToken = regexp.MustCompile(`[$*?` + "`" + `]|\{\{`)

// resolve returns the tracked files a command position token names, if any.
func resolve(tok string, index map[string][]string) []string {
	tok = strings.Trim(tok, `"'`)
	if tok == "" || strings.HasPrefix(tok, "-") || strings.HasPrefix(tok, "/") {
		return nil
	}
	if !strings.Contains(tok, "/") {
		return nil // a name resolved on PATH, not a file here
	}
	if notAToken.MatchString(tok) {
		return nil
	}
	tok = strings.TrimPrefix(tok, "./")
	if strings.HasPrefix(tok, "../") || tok == "" {
		return nil
	}
	// Matched from the tail in both directions, because a workflow's idea of
	// where the repository starts is not always the repository root. The status
	// page's job checks this tree out into `main/` and runs
	// `main/deploy/status/probe.sh`, and a step with a working-directory names
	// a path shorter than the one git records. Leading segments come off the
	// token until what is left is the tail of a tracked path, which means the
	// two agree about every segment either of them wrote down.
	for {
		if hits := index[tok]; len(hits) > 0 && namesAPath(tok, hits) {
			return hits
		}
		cut := strings.IndexByte(tok, '/')
		if cut < 0 {
			return nil
		}
		tok = tok[cut+1:]
	}
}

// namesAPath rejects a match that has come down to a bare file name.
//
// `${dir%/tsconfig.json}` strips to `tsconfig.json`, which is the tail of
// twelve tracked files and an invocation of none of them. A tail worth
// believing either keeps a directory in it, so that the token and the tracked
// path agree about more than one segment, or is the whole of a tracked path,
// which is what `./install.sh` at the repository root is.
func namesAPath(tok string, hits []string) bool {
	if strings.Contains(tok, "/") {
		return true
	}
	for _, h := range hits {
		if h == tok {
			return true
		}
	}
	return false
}

// runners are words that stand in front of the command they run, so the token
// worth reading is the one after them.
//
// `if` and `while` and `until` are here because `if tools/x.sh; then` really
// does exec the script. `then`, `else` and `do` are here for the same reason on
// the other side of the separator this splits on.
var runners = map[string]bool{
	"!": true, "command": true, "do": true, "elif": true, "else": true,
	"env": true, "exec": true, "if": true, "nohup": true, "sudo": true,
	"then": true, "time": true, "until": true, "while": true,
}

// envAssign matches the NAME=value that a shell allows in front of a command.
var envAssign = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// justPrefix matches the `@` and `-` that just allows in front of a recipe
// line, meaning do not echo it and ignore its exit status.
var justPrefix = regexp.MustCompile(`^[@-]+`)

// commandTokens returns the first word of every command on one shell line.
//
// Only the first word matters, because that is the file the kernel is asked to
// exec. `bash tools/x.sh`, `source tools/x.sh` and `cat tools/x.sh` all name
// the script in a later position and none of them needs the bit, and reading
// only the head is what tells those apart from running it.
func commandTokens(line string) []string {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return nil
	}
	line = justPrefix.ReplaceAllString(line, "")

	var out []string
	for _, seg := range segments(line) {
		fields := strings.Fields(seg)
		for len(fields) > 0 && (runners[fields[0]] || envAssign.MatchString(fields[0])) {
			fields = fields[1:]
		}
		if len(fields) > 0 {
			out = append(out, fields[0])
		}
	}
	return out
}

// segments splits a line at the operators that start a new command, ignoring
// any that fall inside quotes.
//
// This is not a shell parser and does not try to be. It splits generously and
// then throws away every head that is not a tracked path, so an operator it
// reads too eagerly costs a token that resolves to nothing, and one it misses
// costs a command it does not look inside. Both are quiet, which is what the
// refusal to pass over an empty result exists to catch.
func segments(line string) []string {
	var out []string
	var cur strings.Builder
	var quote byte

	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			out = append(out, s)
		}
		cur.Reset()
	}

	for i := 0; i < len(line); i++ {
		c := line[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			cur.WriteByte(c)
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
			cur.WriteByte(c)
		case '$':
			// A `${...}` is one value, not a brace group. Without this the
			// justfile's `${dir%/package-lock.json}` splits at the braces and
			// leaves `dir%/package-lock.json` looking like a command, which is
			// the only false match this tool produced on this repository.
			cur.WriteByte(c)
			if i+1 < len(line) && line[i+1] == '{' {
				for i++; i < len(line); i++ {
					cur.WriteByte(line[i])
					if line[i] == '}' {
						break
					}
				}
			}
		case ';', '|', '&', '(', ')', '{', '}', '`':
			flush()
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return out
}

// heredoc matches the start of a here document, and captures its delimiter.
//
// `<<<` is a here string, whose body is on the same line, so the negative
// lookahead that a Go regexp cannot spell is done by hand in openHeredoc.
var heredoc = regexp.MustCompile(`^<<-?\s*['"]?([A-Za-z_][A-Za-z0-9_]*)['"]?`)

// openHeredoc returns the delimiter of a here document this line opens, or the
// empty string.
//
// The redirection has to be outside quotes, and that is not a nicety. A line
// reading `echo "use << to redirect"` would otherwise open a document whose
// delimiter never arrives, and every command after it in the block would be
// swallowed as data. That failure is silent, and silent is the one thing this
// tool is not allowed to be.
func openHeredoc(line string) string {
	var quote byte
	for i := 0; i < len(line); i++ {
		c := line[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
		case '<':
			if i+1 >= len(line) || line[i+1] != '<' {
				continue
			}
			if i+2 < len(line) && line[i+2] == '<' {
				return "" // a here string, whose body is on this line
			}
			if m := heredoc.FindStringSubmatch(line[i:]); m != nil {
				return m[1]
			}
			return ""
		}
	}
	return ""
}

// commandLines drops the lines of a block that are not command position.
//
// Two shapes are not. The body of a here document is data, and a repository
// that writes a config file with a heredoc would otherwise have its contents
// read as commands. A line continued from the one above with a trailing
// backslash carries arguments, not a command, and dropping it is what keeps the
// second line of `go run ./tools/relnotes \` from being read as one.
func commandLines(lines []shellLine) []shellLine {
	var out []shellLine
	continued := false
	delim := ""

	for _, l := range lines {
		trimmed := strings.TrimSpace(l.text)

		if delim != "" {
			if trimmed == delim {
				delim = ""
			}
			continue
		}

		wasContinued := continued
		continued = strings.HasSuffix(trimmed, `\`)

		delim = openHeredoc(trimmed)
		if wasContinued {
			continue
		}
		out = append(out, l)
	}
	return out
}

// runKey matches the `run:` of a workflow step, wherever the step's dash puts
// it, and captures the column the key starts at.
var runKey = regexp.MustCompile(`^(\s*(?:-\s+)?)run:(.*)$`)

// workflowShell returns the shell lines of every `run:` in one workflow.
//
// Deliberately not a YAML parse, in the same spirit as gatecheck, and it needs
// less than gatecheck does: the directory a step runs in does not matter here,
// because a token is matched against the tracked paths by suffix. What it does
// need is the line number, which is what a person uses to go and look, and
// which a decoded document does not carry.
//
// A block scalar's body is every line indented further than its key. A blank
// line does not end it, because YAML allows one inside a block scalar and the
// justfile and the workflows both contain them.
func workflowShell(source string) []shellLine {
	var out []shellLine
	keyIndent := -1

	for i, line := range strings.Split(source, "\n") {
		num := i + 1
		trimmed := strings.TrimSpace(line)

		if keyIndent >= 0 {
			if trimmed == "" {
				out = append(out, shellLine{num: num})
				continue
			}
			if indentOf(line) > keyIndent {
				out = append(out, shellLine{num: num, text: line})
				continue
			}
			keyIndent = -1
		}
		if trimmed == "" {
			continue
		}

		m := runKey.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		rest := strings.TrimSpace(m[2])
		if rest == "" || strings.HasPrefix(rest, "|") || strings.HasPrefix(rest, ">") {
			keyIndent = len(m[1])
			continue
		}
		// A `run:` that carries its command on the same line. The quotes YAML
		// puts around a scalar are not the shell's, so they come off.
		out = append(out, shellLine{num: num, text: strings.Trim(rest, `"'`)})
	}
	return out
}

func indentOf(line string) int {
	return len(line) - len(strings.TrimLeft(line, " \t"))
}

// recipeHeader matches a justfile recipe. Taken from gatecheck, including the
// `_` in the leading class, because `_generated` and `_reports` are recipes.
var recipeHeader = regexp.MustCompile(`^([a-z_][\w-]*)((?: [\w"'=.-]+)*):(.*)$`)

// justShell returns the shell lines of every justfile recipe body.
//
// A body is every indented line under a header, up to the next line at column
// zero that is neither blank nor a comment. The comment exception is what keeps
// a divider or a doc comment between two recipes from looking like the end of a
// body.
func justShell(source string) []shellLine {
	var out []shellLine
	inRecipe := false

	for i, line := range strings.Split(source, "\n") {
		num := i + 1
		trimmed := strings.TrimSpace(line)
		indented := line != "" && (line[0] == ' ' || line[0] == '\t')

		if !indented && trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			inRecipe = recipeHeader.MatchString(line)
			continue
		}
		if inRecipe && indented {
			out = append(out, shellLine{num: num, text: line})
		}
	}
	return out
}
