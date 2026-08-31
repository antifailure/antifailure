package main

import (
	"path"
	"regexp"
	"strings"
)

// A block is a run of shell lines that start in one directory: one step of a
// workflow job, or one recipe of the justfile.
//
// Blocks exist because the directory is what tells two otherwise identical
// gates apart, and the directory is never in the command. `npm run build` runs
// in www, in docs and in console; `go test ./...` runs in engine and in tools.
// CI carries the directory in `working-directory:`, which can sit either side
// of the `run:` it applies to, and the justfile carries it in `cd`, `--prefix`
// or `-C`. Reading either one line at a time cannot associate the two, and
// reading the whole file as one stream would let a directory leak from one
// step into the next.
type block struct {
	// name is what to call this in a failure: a step's `name:`, or a recipe's
	// name. A step with no name gets the workflow file it came from.
	name string
	// dir is where the block starts, before any `cd` inside it. Empty means
	// the repository root.
	dir string
	// lines are the shell lines, in order, with their original text. Quoted
	// spans are still present: gate matching removes them, and directory
	// reading needs them.
	lines []string
	// oneShell says whether a `cd` on one line is still in force on the next.
	//
	// A workflow's `run:` block is one script, so it is. A justfile recipe is
	// only one script when it opens with a shebang; without one, just runs
	// every line in a shell of its own and a `cd` dies with the line that made
	// it. Getting this wrong is not cosmetic: `fuzz-engine` starts both its
	// lines with `cd engine`, and carrying the first one forward read the
	// second as running in engine/engine, which paired with nothing.
	oneShell bool
}

// unknownDir stands for a directory that only exists at run time: `cd "$dir"`
// in a loop over examples/*/, or `--prefix "$root"` where the recipe worked
// the project out from the tree. Reading it as a literal would pair two gates
// that run in different places, and refusing to read it at all would report
// drift that does not exist, so it gets a name of its own and is compared
// loosely, deliberately and visibly. See pairedWith.
const unknownDir = "?"

// rootDir is the repository root, which is where a command with no `cd` and no
// `working-directory:` runs.
const rootDir = "."

// stepStart matches a YAML sequence item that begins a workflow step. Anchored
// on the key that follows the dash rather than on the dash alone, so a bare
// list of strings somewhere else in the file does not read as a step.
var stepStart = regexp.MustCompile(`^(\s*)-\s+([\w-]+):(.*)$`)

// yamlKey matches a mapping key at a known indent.
var yamlKey = regexp.MustCompile(`^(\s*)([\w-]+):(.*)$`)

// jobKey matches a job name inside `jobs:`. Two spaces exactly, no value.
var jobKey = regexp.MustCompile(`^  ([\w-]+):\s*$`)

// workflowBlocks reads one workflow file into its steps.
//
// Deliberately not a YAML parse, in the same spirit as the rest of this file,
// but structured enough to get the one thing a line scan cannot: which `run:`
// a `working-directory:` belongs to. The rules it needs are small. A step is a
// sequence item whose first key starts it. `working-directory:` at the step's
// own key indent sets that step's directory, whether it comes before or after
// the `run:` it applies to, which is why a step is buffered and resolved at
// its end. The same key nested deeper belongs to something else: ci.yml passes
// `working-directory: engine` to golangci-lint inside a `with:` block, and
// that step runs no shell at all.
//
// A `defaults: run: working-directory:` before the first step sets the
// directory for every step in the job that does not name its own.
func workflowBlocks(name, source string) []block {
	var out []block

	inJobs := false
	jobDir := ""
	cur := block{}
	haveStep := false
	stepIndent := 0
	inRun := false
	runIndent := 0

	flush := func() {
		if haveStep && len(cur.lines) > 0 {
			if cur.dir == "" {
				cur.dir = jobDir
			}
			if cur.name == "" {
				cur.name = name
			}
			cur.oneShell = true
			out = append(out, cur)
		}
		cur = block{}
		haveStep = false
		inRun = false
	}

	for _, line := range strings.Split(source, "\n") {
		if strings.TrimSpace(line) == "" {
			if inRun {
				cur.lines = append(cur.lines, "")
			}
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))

		// Inside a `run:` block scalar until the indent comes back out.
		if inRun {
			if indent > runIndent {
				cur.lines = append(cur.lines, line)
				continue
			}
			inRun = false
		}

		if !inJobs {
			if line == "jobs:" {
				inJobs = true
			}
			continue
		}

		if m := jobKey.FindStringSubmatch(line); m != nil {
			flush()
			jobDir = ""
			continue
		}

		if m := stepStart.FindStringSubmatch(line); m != nil {
			flush()
			haveStep = true
			stepIndent = len(m[1]) + 2
			readStepKey(&cur, m[2], m[3])
			if m[2] == "run" && isBlockScalar(m[3]) {
				inRun = true
				runIndent = stepIndent
			}
			continue
		}

		m := yamlKey.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		key, rest := m[2], m[3]

		if haveStep && indent == stepIndent {
			readStepKey(&cur, key, rest)
			if key == "run" && isBlockScalar(rest) {
				inRun = true
				runIndent = stepIndent
			}
			continue
		}

		// Before the first step of the job: the only key worth reading is the
		// job's default working directory. Depth is not checked because
		// `defaults: run: working-directory:` is the only place the key can
		// appear out here, and a job that sets it means it for every step.
		if !haveStep && key == "working-directory" {
			jobDir = literalDir(rest)
		}
	}
	flush()
	return out
}

// readStepKey records the two step keys that matter.
func readStepKey(b *block, key, rest string) {
	switch key {
	case "name":
		b.name = strings.TrimSpace(rest)
	case "working-directory":
		b.dir = literalDir(rest)
	case "run":
		if !isBlockScalar(rest) {
			if v := strings.TrimSpace(rest); v != "" {
				b.lines = append(b.lines, v)
			}
		}
	}
}

// isBlockScalar reports whether a `run:` opens a multi-line block rather than
// carrying its command on the same line. `|`, `>` and their chomping and
// indentation indicators all do.
func isBlockScalar(rest string) bool {
	v := strings.TrimSpace(rest)
	return v == "" || strings.HasPrefix(v, "|") || strings.HasPrefix(v, ">")
}

// literalDir reads a directory out of a YAML scalar.
func literalDir(rest string) string {
	v := strings.TrimSpace(rest)
	v = strings.Trim(v, `"'`)
	if v == "" {
		return ""
	}
	return normalizeDir(v)
}

// recipeHeader matches a justfile recipe. `_` is in the leading class because
// `_generated` and `_reports` are recipes, `just gate` calls the first of them,
// and it carries five gates. A parser that skipped them would report those
// five as things CI runs and the justfile does not.
var recipeHeader = regexp.MustCompile(`^([a-z_][\w-]*)((?: [\w"'=.-]+)*):(.*)$`)

// recipe is one justfile recipe: its block, and what it depends on.
type recipe struct {
	block
	deps []string
}

// justRecipes reads the justfile into its recipes.
//
// A recipe's body is every indented line under its header, up to the next line
// at column zero that is neither blank nor a comment. The comment exception is
// what keeps a `# ---` divider or a doc comment between two recipes from
// looking like the end of a body: it is skipped by the scanner either way, and
// treating it as a boundary would depend on whether somebody left a blank line.
func justRecipes(source string) []recipe {
	var out []recipe
	var cur *recipe

	for _, line := range strings.Split(source, "\n") {
		trimmed := strings.TrimSpace(line)
		indented := line != "" && (line[0] == ' ' || line[0] == '\t')

		if !indented && trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			if m := recipeHeader.FindStringSubmatch(line); m != nil {
				out = append(out, recipe{
					block: block{name: m[1]},
					deps:  strings.Fields(m[3]),
				})
				cur = &out[len(out)-1]
				continue
			}
			// An attribute line such as `[doc("...")]`, or anything else at
			// column zero that is not a recipe. Either way the previous body
			// has ended.
			cur = nil
			continue
		}
		if cur != nil && indented {
			// A recipe whose first line is a shebang is one script, so a `cd`
			// in it carries. The shebang itself is not a command.
			if len(cur.lines) == 0 && strings.HasPrefix(trimmed, "#!") {
				cur.oneShell = true
				continue
			}
			cur.lines = append(cur.lines, line)
		}
	}
	return out
}

// leadingCd matches a `cd` at the start of a command, with the directory it
// moves to. Read from the raw line rather than the quote-stripped one, because
// `cd "$dir"` has to be told apart from `cd engine`; anchoring at the start of
// the line is what keeps `echo "cd /tmp"` from moving anything.
var leadingCd = regexp.MustCompile(`^\(?\s*cd\s+([^\s;&|]+)`)

// normalizeDir reduces a directory to what is worth comparing: no leading
// `./`, no trailing slash, and the repository root spelled one way. A path
// that is not a literal becomes unknownDir, because a value the shell works
// out is one this cannot read.
func normalizeDir(dir string) string {
	dir = strings.Trim(dir, `"'`)
	if dir == "" {
		return unknownDir
	}
	if strings.ContainsAny(dir, "$*?`") || strings.Contains(dir, "{{") {
		return unknownDir
	}
	if strings.HasPrefix(dir, "/") {
		return unknownDir
	}
	dir = path.Clean(dir)
	if dir == "" || dir == "." {
		return rootDir
	}
	return dir
}

// joinDir resolves a directory named inside a block against the one the block
// started in.
func joinDir(base, arg string) string {
	if base == unknownDir || arg == unknownDir {
		return unknownDir
	}
	if base == "" {
		base = rootDir
	}
	joined := path.Clean(path.Join(base, arg))
	if joined == "" || joined == "." {
		return rootDir
	}
	// A path that climbs above the repository root is not something this can
	// pair, and pretending otherwise would compare two different trees.
	if strings.HasPrefix(joined, "..") {
		return unknownDir
	}
	return joined
}
