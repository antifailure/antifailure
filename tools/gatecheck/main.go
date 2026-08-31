// Command gatecheck proves that `just gate` runs what CI runs.
//
// CONTRIBUTING.md makes a specific promise: "That runs every quality gate the
// CI runs, in the same order, with the same tool versions. If it is green
// locally it is green in CI." That promise is worth having and it rots without
// anything watching it. Somebody adds a job to the workflow, the justfile does
// not learn about it, and from then on a green local run means less than the
// document says it does. Nobody finds out until a pull request that passed
// locally fails in CI, which is exactly the moment the promise was supposed to
// prevent.
//
// So this compares the two. It is deliberately not a full YAML or justfile
// parser: it looks for the commands that constitute a gate, on both sides, and
// reports anything CI runs that `just gate` does not. Being approximate is
// fine; being silent is not.
//
// A gate is a command AND the directory it runs in. Keyed on the command
// alone, `go test ./...` in engine and the same in tools are one gate, and
// covering either covered both; so are `npm test` in web and in ee/web, and
// `npm run build` in www, docs and console. The directory is in neither
// command: CI carries it in `working-directory:` or a `cd` inside a `run:`
// block, and the justfile in `cd`, `--prefix` or `-C`. Reading it means
// reading each side in blocks rather than in lines, which is what blocks.go
// and scan.go do.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// A gate is a command worth failing over. Matching on these rather than on
// every line keeps `cd`, `echo`, and shell plumbing out of the comparison,
// which would otherwise make this noisy enough that somebody deletes it.
//
// `cmd` anchors each one at a word boundary so that a substring of a longer
// token is not a match, and quoted spans are removed before matching. Both are
// there for one observed failure: `echo "go test is not being run here"`
// registered as a gate called `gotest is`, which is how a check starts
// reporting drift that does not exist and gets deleted for being noisy.
const cmd = "(?:^|[\\s;&|(])"

// The npm class carries `"` and `$` because a quoted span is replaced with `""`
// rather than dropped, and an argument can be a shell variable. Without them
// `npx --prefix "$root" tsc` reads as no gate at all, and `just typecheck`
// became invisible here the day it started deriving its projects from the tree
// instead of naming them. A gate this cannot see is a gate it silently stops
// pairing, which is the failure this tool exists to prevent.
var gatePatterns = []*regexp.Regexp{
	regexp.MustCompile(cmd + `go run \./tools/(\w+)`),
	regexp.MustCompile(cmd + `go (test|vet|build) ([^\s|;&]+)`),
	regexp.MustCompile(cmd + `(npm|npx) [\w\s./"$-]*?(test|tsc)\b`),
	regexp.MustCompile(cmd + `gofmt -l`),
	regexp.MustCompile(cmd + `node --test`),
}

// quoted spans, removed before matching so that a command named inside a
// message is not mistaken for a command being run.
var quoted = regexp.MustCompile(`"[^"]*"|'[^']*'`)

// gate is one thing that must happen on both sides.
//
// dir is part of the identity because the directory is what tells two
// otherwise identical gates apart, and it is in neither command: CI carries it
// in `working-directory:` and the justfile in `cd` or `--prefix`. Without it
// `go test ./...` in engine and `go test ./...` in tools are one gate, and
// covering either covers both.
//
// The exception is `go run ./tools/X`, which carries no directory. Those tools
// take the tree to scan as an argument, so `cd engine && go run ../tools/scanrepo ..`
// and `go run ./tools/scanrepo .` scan the same thing from different places.
// Keying them on the directory would report drift that is not drift, which is
// how a check starts being ignored.
type gate struct {
	kind string // "tool", "gotest", "govet", "gobuild", "npm", "gofmt", "nodetest"
	arg  string
	dir  string // where it runs; empty for kinds the directory does not identify
}

func (g gate) String() string {
	s := g.kind
	if g.arg != "" {
		s += " " + g.arg
	}
	if g.dir != "" {
		s += " in " + g.dir
	}
	return s
}

// same reports whether two gates are the same command, ignoring where it ran.
func (g gate) same(other gate) bool {
	return g.kind == other.kind && g.arg == other.arg
}

func main() {
	root := flag.String("root", ".", "repository root")
	flag.Parse()
	if args := flag.Args(); len(args) > 0 {
		*root = args[0]
	}

	justPath := filepath.Join(*root, "justfile")

	// Every workflow that runs on a pull request, not just ci.yml. The promise
	// being kept is "a green `just gate` means a green CI", and that promise is
	// about whatever runs against a contributor's branch. Reading one file by
	// name would mean a second workflow could carry gates the justfile has
	// never heard of, and nothing would say so.
	//
	// Workflows that do not run on pull requests are out of scope by the same
	// reasoning rather than by an exception: release.yml runs on a tag, long
	// after the gate had its say.
	workflows, err := pullRequestWorkflows(filepath.Join(*root, ".github", "workflows"))
	if err != nil {
		fail("reading workflows: %v", err)
	}
	if len(workflows.paths) == 0 {
		fail("found no workflow that runs on pull requests. Either they stopped " +
			"running on pull requests, or this check has stopped recognising them.")
	}

	just, err := os.ReadFile(justPath)
	if err != nil {
		fail("reading %s: %v\n\nCONTRIBUTING.md promises `just gate`. Without a "+
			"justfile that promise is a lie in the first document a contributor reads.", justPath, err)
	}

	ciPath := strings.Join(workflows.names(), ", ")

	// A workflow this stops reading is worse than one it never read: the gates
	// go quiet and the count still looks healthy because five other files
	// carry it. Nothing said so while the comparison was line based, because
	// there was no structure to lose. There is now, so a file that has `run:`
	// steps and yields none of them is a parse failure and says which file.
	for i, source := range workflows.sources {
		if hasRunStep(source) && len(workflowBlocks(workflows.paths[i], source)) == 0 {
			fail("read no steps out of %s, which has `run:` steps in it. This check "+
				"has stopped recognising the shape of that workflow, and every gate "+
				"in it is now invisible rather than reported.", workflows.paths[i])
		}
	}

	ciGates := scan(workflows.blocks())

	recipes := justRecipes(string(just))
	reachable := reachableFromGate(recipes)
	justGates := scan(recipeBlocks(recipes))

	if len(ciGates) == 0 {
		fail("found no gates in %s. Either the workflow stopped running any, or "+
			"this check has stopped recognising them. Both are worth stopping for.", ciPath)
	}
	if len(justGates) == 0 {
		fail("found no gates in %s, which cannot be right if `just gate` exists.", justPath)
	}

	var missing []gap
	var stale []string
	var loose []string
	usedExemption := map[string]bool{}
	for _, key := range sortedEntries(ciGates) {
		// Exemption first. Whether the justfile happens to carry a recipe for
		// an exempt gate is beside the point: the exemption records that `gate`
		// does not run it, and `just vuln` existing is what makes that bearable
		// rather than what makes the exemption unnecessary. Checking justGates
		// first would leave the exemption looking unused and report it stale.
		if _, ok := exemptFromGate[key]; ok {
			usedExemption[key] = true
			continue
		}
		switch how, where := pairedWith(ciGates[key].gate, justGates, reachable); how {
		case pairedExactly:
			// Nothing to say. This is the ordinary case.
		case pairedByRuntimeDir:
			loose = append(loose, key+"  <-  "+where)
		default:
			missing = append(missing, gapFor(ciGates[key].gate, key, justGates, reachable))
		}
	}
	// An exemption that no longer matches anything is dead code in a policy: it
	// reads as a considered decision about a gate that is not there any more,
	// and it would silently cover a future gate that happened to take the same
	// name.
	for _, g := range sortedExemptions() {
		if !usedExemption[g] {
			stale = append(stale, g)
		}
	}

	// The `gate` recipe has to actually invoke the individual recipes, or the
	// justfile could define every gate and run none of them.
	uncalled := uncalledByGate(recipes, reachable)

	if len(missing) == 0 && len(uncalled) == 0 && len(stale) == 0 {
		report(len(workflows.paths), ciGates, len(usedExemption), loose)
		return
	}

	if len(missing) > 0 {
		fmt.Fprintf(os.Stderr, "gatecheck: CI runs %d %s `just gate` does not:\n",
			len(missing), plural(len(missing), "thing", "things"))
		for _, m := range missing {
			fmt.Fprintf(os.Stderr, "  %s\n", m.ci)
			fmt.Fprintf(os.Stderr, "      %s\n", m.reason)
		}
		fmt.Fprintf(os.Stderr, "\nA gate is the command AND the directory it runs in: `go test ./...`\n"+
			"in engine and in tools are two gates, and so is `npm test` in web and in\n"+
			"ee/web. Add a recipe that runs it where CI runs it, and call it from `gate`.\n")
	}
	if len(stale) > 0 {
		fmt.Fprintf(os.Stderr, "\ngatecheck: these gates are exempt from `just gate` but no workflow runs them:\n")
		for _, g := range stale {
			fmt.Fprintf(os.Stderr, "  %s\n", g)
		}
		fmt.Fprintf(os.Stderr, "\nRemove the exemption. A reason to skip a gate that is gone "+
			"describes nothing, and it would quietly cover the next gate to take that name.\n")
	}
	if len(uncalled) > 0 {
		fmt.Fprintf(os.Stderr, "\ngatecheck: these recipes exist and `just gate` never calls them:\n")
		for _, u := range uncalled {
			fmt.Fprintf(os.Stderr, "  %s\n", u)
		}
		fmt.Fprintf(os.Stderr, "\nA gate the one command does not run is a gate nobody runs.\n")
	}
	fmt.Fprintf(os.Stderr, "\nCONTRIBUTING.md says a green `just gate` means a green CI. "+
		"That is only true while these agree.\n")
	os.Exit(1)
}

// runStep matches a step's `run:` key, which is the only thing in a workflow
// that carries a gate.
var runStep = regexp.MustCompile(`(?m)^\s+-?\s*run:`)

func hasRunStep(source string) bool { return runStep.MatchString(source) }

// report says what was checked, in the terms it was checked in.
//
// The sentence this replaced said "every one reachable from `just gate`" and
// checked two weaker things: that the command appeared somewhere in the
// justfile, and, separately, that every gate-shaped recipe was called. A
// command sitting in a recipe `gate` never runs satisfied the first and was
// never held to the second, so the line claimed a property it did not test.
// This one names the number of workflows read, the fact that the directory is
// part of the pairing, the gates that are exempt rather than paired, and the
// gates whose directory could not be compared because the justfile works it
// out at run time. A reader should not be able to come away believing anything
// stronger than what ran.
func report(workflows int, ciGates map[string]*entry, exempt int, loose []string) {
	fmt.Printf("gatecheck: %d gates in %d pull request workflows.\n", len(ciGates), workflows)
	fmt.Printf("  %d paired by command and directory to a recipe `just gate` calls.\n",
		len(ciGates)-exempt-len(loose))
	if len(loose) > 0 {
		fmt.Printf("  %d paired by command only, against a justfile command whose directory\n"+
			"    is computed at run time, so for these the directory was not compared:\n", len(loose))
		for _, l := range loose {
			fmt.Printf("      %s\n", l)
		}
	}
	fmt.Printf("  %d exempt by name in exemptFromGate, with the reason recorded there.\n", exempt)
}

// pairing is how a CI gate was matched on the justfile side.
type pairing int

const (
	notPaired pairing = iota
	pairedExactly
	pairedByRuntimeDir
)

// pairedWith looks for the justfile command that covers a CI gate.
//
// Exactly first: the same command in the same directory, in a recipe `just
// gate` reaches. Failing that, the same command in a directory the justfile
// computes at run time, which is what `just typecheck` does when it finds its
// tsconfig files in the tree rather than naming them. That match is real
// coverage and refusing it would report drift that does not exist, but it is
// weaker than the other, so it is counted and printed rather than folded in.
func pairedWith(g gate, justGates map[string]*entry, reachable map[string]bool) (pairing, string) {
	if e, ok := justGates[g.String()]; ok && anyReachable(e.blocks, reachable) {
		return pairedExactly, ""
	}
	if g.dir == "" {
		return notPaired, ""
	}
	for _, key := range sortedEntries(justGates) {
		e := justGates[key]
		if !e.gate.same(g) || !anyReachable(e.blocks, reachable) {
			continue
		}
		// The justfile's directory is a run-time value, so it may or may not
		// be this one, or CI's is and the justfile's is a literal. Either way
		// there is nothing left to compare.
		if e.gate.dir == unknownDir || g.dir == unknownDir {
			return pairedByRuntimeDir, "justfile: " + key + ", in " + strings.Join(e.blocks, ", ")
		}
	}
	return notPaired, ""
}

// gap describes one CI gate `just gate` does not cover, and says which of the
// three ways it does not.
type gap struct {
	ci     string
	reason string
}

func gapFor(g gate, key string, justGates map[string]*entry, reachable map[string]bool) gap {
	var elsewhere []string   // the same command, run in another directory
	var unreachable []string // the same gate, in a recipe `gate` never calls
	for _, k := range sortedEntries(justGates) {
		e := justGates[k]
		if !e.gate.same(g) {
			continue
		}
		if !anyReachable(e.blocks, reachable) {
			unreachable = append(unreachable, k+" in "+strings.Join(e.blocks, ", "))
			continue
		}
		elsewhere = append(elsewhere, k)
	}
	switch {
	case len(elsewhere) > 0:
		return gap{key, "the justfile runs this only in " + strings.Join(elsewhere, ", ")}
	case len(unreachable) > 0:
		return gap{key, "the justfile runs this in a recipe `just gate` never calls: " +
			strings.Join(unreachable, ", ")}
	default:
		return gap{key, "nothing in the justfile runs this"}
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func anyReachable(blocks []string, reachable map[string]bool) bool {
	for _, b := range blocks {
		if reachable[b] {
			return true
		}
	}
	return false
}

// justCall matches one recipe calling another.
var justCall = regexp.MustCompile(`just ([a-z_][\w-]*)`)

// reachableFromGate returns the recipes `just gate` runs, directly or through
// another recipe.
//
// Both routes count: a dependency in the header, and a `just <name>` in the
// body, which is how the `gate` recipe itself calls everything so that it can
// keep going after one of them fails.
func reachableFromGate(recipes []recipe) map[string]bool {
	byName := map[string]recipe{}
	for _, r := range recipes {
		byName[r.name] = r
	}

	reachable := map[string]bool{}
	queue := []string{"gate"}
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if reachable[name] {
			continue
		}
		reachable[name] = true
		r, ok := byName[name]
		if !ok {
			continue
		}
		queue = append(queue, r.deps...)
		for _, line := range r.lines {
			for _, m := range justCall.FindAllStringSubmatch(line, -1) {
				queue = append(queue, m[1])
			}
		}
	}
	return reachable
}

// recipeBlocks is the recipes as plain blocks, for scanning.
func recipeBlocks(recipes []recipe) []block {
	out := make([]block, 0, len(recipes))
	for _, r := range recipes {
		out = append(out, r.block)
	}
	return out
}

// gatesIn finds every gate on one line, given the directory that line runs in.
//
// The line arrives raw. Quoted spans are removed before the patterns run, so a
// command named inside a message is not read as a command being run, but the
// raw text is what `--prefix` and `-C` have to be read from: after the
// substitution `npx --prefix "$root"` is `npx --prefix ""`, which cannot be
// told apart from a directory this simply failed to parse.
func gatesIn(raw, dir string) []gate {
	line := quoted.ReplaceAllString(raw, `""`)
	var out []gate
	for _, re := range gatePatterns {
		for _, m := range re.FindAllStringSubmatch(line, -1) {
			// The anchor is part of m[0]; the command starts at "go", "npm",
			// "npx", "gofmt" or "node".
			whole := strings.TrimLeft(m[0], " \t;&|($")
			switch {
			case strings.HasPrefix(whole, "go run ./tools/"):
				out = append(out, gate{kind: "tool", arg: m[1]})
			case strings.HasPrefix(whole, "go test"):
				out = append(out, gate{"gotest", normalizeTarget(m[2]), goDir(raw, dir)})
			case strings.HasPrefix(whole, "go vet"):
				out = append(out, gate{"govet", normalizeTarget(m[2]), goDir(raw, dir)})
			case strings.HasPrefix(whole, "go build"):
				// A build is covered by the tests that follow it, and the
				// edition boundary builds with flags this cannot usefully
				// compare. Recorded as one gate per directory rather than per
				// target.
				out = append(out, gate{"gobuild", "", goDir(raw, dir)})
			case strings.HasPrefix(whole, "gofmt -l"):
				out = append(out, gate{"gofmt", "", dir})
			case strings.HasPrefix(whole, "node --test"):
				out = append(out, gate{"nodetest", "", dir})
			default:
				out = append(out, gate{"npm", m[2], npmDir(raw, dir)})
			}
		}
	}
	return out
}

// npmDir is where an npm command's package.json is: `--prefix X` if it names
// one, and otherwise the directory the line runs in.
func npmDir(raw, dir string) string {
	if m := npmPrefix.FindStringSubmatch(raw); m != nil {
		return joinDir(dir, normalizeDir(m[1]))
	}
	return dir
}

// goDir is the same for the go toolchain's `-C`.
//
// Read only from a line whose command is `go`, because `-C` means something
// else nearly everywhere else: `grep -C 3` asks for context lines, and a
// pattern that read it as a directory would move the shell somewhere the shell
// never went.
func goDir(raw, dir string) string {
	trimmed := strings.TrimLeft(raw, " \t(")
	if !strings.HasPrefix(trimmed, "go ") {
		return dir
	}
	if m := goChdir.FindStringSubmatch(raw); m != nil {
		return joinDir(dir, normalizeDir(m[1]))
	}
	return dir
}

var (
	npmPrefix = regexp.MustCompile(`--prefix[=\s]+([^\s;&|]+)`)
	goChdir   = regexp.MustCompile(`\s-C[=\s]+([^\s;&|]+)`)
)

// normalizeTarget reduces a Go package pattern to what is worth comparing.
//
// The directory the command ran in is compared separately and exactly; this is
// only about the pattern. `./internal/secrets/...` and `./...` reach different
// sets of packages but both mean "everything under here", and CI spells the
// same run both ways in two workflows.
func normalizeTarget(target string) string {
	target = strings.Trim(target, `"'`)
	switch {
	case strings.Contains(target, "..."):
		return "./..."
	case strings.HasPrefix(target, "./internal/"), strings.HasPrefix(target, "./license"):
		return target
	default:
		return target
	}
}

// uncalledByGate reports recipes that define a gate and that `just gate` never
// invokes, directly or through another recipe.
//
// It takes the same reachability set the pairing uses, so the two cannot
// disagree about what `just gate` runs. They did not have to agree before: a
// gate could be counted as covered because the command appeared somewhere in
// the justfile, while the recipe holding it was reported here as one nothing
// calls, and the passing sentence would still have claimed everything was
// reachable.
func uncalledByGate(recipes []recipe, reachable map[string]bool) []string {
	if !hasRecipe(recipes, "gate") {
		return []string{"(no `gate` recipe at all)"}
	}

	// Recipes that `gate` does not call because the gate itself is exempt.
	// Kept apart from the convenience list below on purpose: a convenience is
	// something that is not a gate at all, and calling `vuln` one would be
	// untrue in the direction that matters. It is a gate; it runs in a workflow;
	// it is out of `gate` for the reason recorded in exemptFromGate.
	exemptRecipes := map[string]bool{
		"vuln": true,
		// The npm half of the same scan, out of `gate` for the same reason and
		// running in the same workflow.
		"npmaudit": true,
		// The getting started path end to end. It needs a daemon and takes
		// minutes, so it runs on a schedule rather than on every branch, the
		// same reasoning as the external link check: a check that costs
		// everybody ten minutes for a property that changes weekly is a check
		// people learn to skip.
		"walkthrough": true,
		// The disaster recovery drill. Same reasoning as walkthrough: it needs
		// a daemon, and it takes minutes because it really does take a dump,
		// create a database, restore into it, and interrogate the result
		// through the unprivileged role. It runs weekly in drill.yml. What IS
		// a function of the tree is the drill's own behaviour, and
		// `just test-web` covers that inside `gate`: the suite breaks a
		// restored database in each of the ways that matter and asserts the
		// drill notices.
		"drill": true,
	}

	// Recipes that are gates rather than conveniences. A recipe that mutates
	// (fmt, generate, db, build, clean) is not something `gate` should run.
	convenience := map[string]bool{
		"default": true, "setup": true, "db": true, "db-down": true, "deps": true,
		"build": true, "build-release": true, "test": true, "test-short": true,
		"fmt": true, "generate": true, "clean": true, "gate": true, "leaks": true,
		// Produces the coverage profile that `coverage` then checks, which
		// makes it the same kind of thing as `generate`: it writes an artifact
		// rather than deciding anything. It is out of `gate` because it runs
		// the whole engine suite with -coverpkg and takes the better part of an
		// hour. `coverage`, which is the gate, IS in `gate`.
		"coverage-profile": true,
		// Turns this repository's commit hooks on. It writes to the clone's git
		// config, which is the definition of a convenience here, and the
		// property it helps with -- every commit carrying a sign-off -- is
		// already a gate in CI that does not depend on anybody having run it.
		"hooks": true,
	}

	var uncalled []string
	for _, r := range recipes {
		name := r.name
		if strings.HasPrefix(name, "_") || convenience[name] || exemptRecipes[name] || reachable[name] {
			continue
		}
		uncalled = append(uncalled, name)
	}
	sort.Strings(uncalled)
	return uncalled
}

func hasRecipe(recipes []recipe, name string) bool {
	for _, r := range recipes {
		if r.name == name {
			return true
		}
	}
	return false
}

// sortedEntries keeps every listing stable, so a failure reads the same twice.
func sortedEntries(m map[string]*entry) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "gatecheck: "+format+"\n", args...)
	os.Exit(1)
}

// exemptFromGate lists gates that a pull request workflow runs and that
// `just gate` deliberately does not, each with the reason.
//
// The bar for adding one is high, and "it is slow" is not on its own enough:
// `just gate` is the promise a contributor reads first, and every exemption
// makes it a slightly smaller promise. What qualifies is a gate whose answer is
// not a function of the tree, because that is the case where running it locally
// would not mean what the gate contract says it means.
//
// An exemption naming a gate no workflow runs fails the build. See the loop
// that fills `stale`.
var exemptFromGate = map[string]string{
	"tool vulncheck": "" +
		"Its answer does not come from this repository. tools/vulncheck asks the " +
		"Go vulnerability database which known advisories are reachable from our " +
		"code, so the same commit is clean today and not clean tomorrow when an " +
		"advisory lands against a dependency it already had. A gate in `just gate` " +
		"is supposed to mean that a green run here is a green run there, and this " +
		"one cannot promise that in either direction. It also needs the network, " +
		"and `just gate` has to work on a plane. " +
		"It runs on every pull request and on a daily schedule in security.yml, " +
		"which is where a scan whose input is a moving database belongs. Run it by " +
		"hand with `just vuln`.",

	"tool npmaudit": "" +
		"The same reasoning as vulncheck's, for the JavaScript half. " +
		"tools/npmaudit asks the npm registry's advisory database what is known " +
		"against the packages in each lockfile, so its answer moves without the " +
		"tree moving, and it needs the network. What IS a function of the tree " +
		"is every decision the tool makes about a report, and `go test " +
		"./tools/npmaudit` covers it inside `gate`: a string element in npm's " +
		"`via` union does not become a phantom finding, an unaccepted advisory " +
		"fails, an expired acceptance fails, an acceptance that matches nothing " +
		"fails, npm refusing to run is told apart from a clean tree, and a " +
		"workspace member is not reported as uncovered. " +
		"It runs beside vulncheck in security.yml. Run it by hand with `just " +
		"npmaudit`.",
	"tool dogfood": "" +
		"Its input is not in the tree either, and it is not one thing. tools/dogfood " +
		"runs the product against itself: it needs a container runtime, a staging " +
		"database to copy, a browser, and about twenty minutes, and what it " +
		"produces is a report about the product rather than a verdict on the diff. " +
		"`just gate` has to work on a plane and has to finish while somebody is " +
		"still looking at it, and this does neither. " +
		"What IS a function of the tree is the harness's own behaviour, and " +
		"`go test ./tools/dogfood` covers it inside `just test-tools`, which " +
		"`gate` runs: that the budgets are well formed, that a phase is timed " +
		"from the events that bound it, and that a run with a leak is not green. " +
		"It runs on every pull request and nightly in dogfood.yml, which is where " +
		"a check whose input is a live environment belongs.",

	"tool azguard": "" +
		"Its answer is a property of the SUBSCRIPTION, not of the tree. " +
		"`azguard region` asks Azure whether a region can actually create this " +
		"stack's PostgreSQL flexible server, and that is a third gate beyond " +
		"quota and Azure Policy which neither a plan nor a policy can see: " +
		"eastus returns supportedServerVersions: [] with \"Provisioning is " +
		"restricted in this region\", and an apply there got twenty six of " +
		"twenty seven resources in before finding out. The same question " +
		"answered on a laptop with no cloud account is not an answer, and " +
		"`just gate` has to work on a plane. " +
		"What IS a function of the tree is the decision the tool makes about a " +
		"capability document, and `go test ./tools/azguard` covers it inside " +
		"`gate`: the restricted-region document is refused with Azure's own " +
		"reason quoted, an unavailable version and an unavailable SKU are " +
		"refused, four separate ways of not knowing are each refused rather " +
		"than passed, and a positive control asserts a good region IS allowed " +
		"so that a guard which refuses everything cannot pass the suite. " +
		"It runs in infra.yml's plan job, which has a credential.",
	"tool cost": "" +
		"Its input is not in the tree. tools/cost reads a Terraform plan, and a " +
		"plan only exists after authenticating to Azure and resolving every " +
		"resource against a live subscription, so there is nothing for it to read " +
		"on a laptop with no cloud account. The engine is meant to run without " +
		"one, and `just gate` has to work on a plane. " +
		"What IS a function of the tree is the estimator's own behaviour, and that " +
		"is covered by `go test ./tools/cost`, which `just test-tools` runs inside " +
		"`gate`: it asserts the pricing file parses, that a known SKU is priced " +
		"from it, and that an unrecognised resource is reported UNKNOWN rather " +
		"than silently costed at zero. " +
		"It runs on every pull request touching infra/ in infra.yml, against the " +
		"plan produced there, with --budget so an over-budget plan fails.",
}

// workflowSet is the workflows that run on a pull request, kept with their
// names so a failure can say which file a gate came from.
type workflowSet struct {
	paths   []string
	sources []string
}

func (w workflowSet) names() []string { return w.paths }

// sortedExemptions keeps the failure output stable. sortedEntries works on the
// gate sets, which carry a gate each; this map carries reasons.
func sortedExemptions() []string {
	out := make([]string, 0, len(exemptFromGate))
	for k := range exemptFromGate {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// blocks is every step of every workflow, read one file at a time.
//
// One file at a time matters: the sources used to be joined into a single
// string and scanned line by line, which is fine while a gate is only a
// command, and wrong the moment a directory is attached to one. Concatenated,
// the last step of ci.yml and the first step of dogfood.yml are adjacent lines,
// and a `cd` in one would carry into the other.
func (w workflowSet) blocks() []block {
	var out []block
	for i, source := range w.sources {
		out = append(out, workflowBlocks(w.paths[i], source)...)
	}
	return out
}

// runsOnPullRequest matches a `pull_request` trigger at the top level of a
// workflow's `on:` block.
//
// Deliberately approximate, in the same spirit as the rest of this file, but
// approximate in the safe direction: a workflow this fails to recognise is one
// whose gates go unchecked, so the match is loose rather than strict, and the
// empty-set check in main catches the case where it stops matching anything.
var pullRequestTrigger = regexp.MustCompile(`(?m)^\s{2,}pull_request:`)

func pullRequestWorkflows(dir string) (workflowSet, error) {
	var set workflowSet

	entries, err := os.ReadDir(dir)
	if err != nil {
		return set, err
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || (!strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".yaml")) {
			continue
		}
		path := filepath.Join(dir, name)
		body, err := os.ReadFile(path)
		if err != nil {
			return set, err
		}
		if !pullRequestTrigger.Match(body) {
			continue
		}
		set.paths = append(set.paths, name)
		set.sources = append(set.sources, string(body))
	}
	return set, nil
}
