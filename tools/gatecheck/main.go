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
// reports anything CI runs that the justfile does not. Being approximate is
// fine; being silent is not.
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

var gatePatterns = []*regexp.Regexp{
	regexp.MustCompile(cmd + `go run \./tools/(\w+)`),
	regexp.MustCompile(cmd + `go (test|vet|build) ([^\s|;&]+)`),
	regexp.MustCompile(cmd + `(npm|npx) [\w\s./-]*?(test|tsc)\b`),
	regexp.MustCompile(cmd + `gofmt -l`),
	regexp.MustCompile(cmd + `node --test`),
}

// quoted spans, removed before matching so that a command named inside a
// message is not mistaken for a command being run.
var quoted = regexp.MustCompile(`"[^"]*"|'[^']*'`)

// gate is one thing that must happen on both sides.
type gate struct {
	kind string // "tool", "gotest", "govet", "gobuild", "npm", "gofmt", "nodetest"
	arg  string
}

func (g gate) String() string {
	if g.arg == "" {
		return g.kind
	}
	return g.kind + " " + g.arg
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
	ciGates := collect(workflows.text())
	justGates := collect(string(just))

	if len(ciGates) == 0 {
		fail("found no gates in %s. Either the workflow stopped running any, or "+
			"this check has stopped recognising them. Both are worth stopping for.", ciPath)
	}
	if len(justGates) == 0 {
		fail("found no gates in %s, which cannot be right if `just gate` exists.", justPath)
	}

	var missing []string
	var stale []string
	usedExemption := map[string]bool{}
	for _, g := range sortedKeys(ciGates) {
		// Exemption first. Whether the justfile happens to carry a recipe for
		// an exempt gate is beside the point: the exemption records that `gate`
		// does not run it, and `just vuln` existing is what makes that bearable
		// rather than what makes the exemption unnecessary. Checking justGates
		// first would leave the exemption looking unused and report it stale.
		if _, ok := exemptFromGate[g]; ok {
			usedExemption[g] = true
			continue
		}
		if _, ok := justGates[g]; !ok {
			missing = append(missing, g)
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
	uncalled := uncalledByGate(string(just))

	if len(missing) == 0 && len(uncalled) == 0 && len(stale) == 0 {
		fmt.Printf("gatecheck: %d gates in CI, every one reachable from `just gate`\n", len(ciGates))
		return
	}

	if len(missing) > 0 {
		fmt.Fprintf(os.Stderr, "gatecheck: CI runs %d things the justfile does not:\n", len(missing))
		for _, m := range missing {
			fmt.Fprintf(os.Stderr, "  %s\n", m)
		}
		fmt.Fprintf(os.Stderr, "\nAdd a recipe for each, and call it from `gate`.\n")
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

// collect finds every gate in a file.
func collect(text string) map[string]struct{} {
	found := map[string]struct{}{}
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		trimmed = quoted.ReplaceAllString(trimmed, `""`)
		for _, g := range gatesIn(trimmed) {
			found[g.String()] = struct{}{}
		}
	}
	return found
}

func gatesIn(line string) []gate {
	var out []gate
	for _, re := range gatePatterns {
		for _, m := range re.FindAllStringSubmatch(line, -1) {
			// The anchor is part of m[0]; the command starts at "go", "npm",
			// "npx", "gofmt" or "node".
			whole := strings.TrimLeft(m[0], " \t;&|($")
			switch {
			case strings.HasPrefix(whole, "go run ./tools/"):
				out = append(out, gate{"tool", m[1]})
			case strings.HasPrefix(whole, "go test"):
				out = append(out, gate{"gotest", normalizeTarget(m[2])})
			case strings.HasPrefix(whole, "go vet"):
				out = append(out, gate{"govet", normalizeTarget(m[2])})
			case strings.HasPrefix(whole, "go build"):
				// A build is covered by the tests that follow it, and the
				// edition boundary builds with flags this cannot usefully
				// compare. Recorded as one gate rather than per target.
				out = append(out, gate{"gobuild", ""})
			case strings.HasPrefix(whole, "gofmt -l"):
				out = append(out, gate{"gofmt", ""})
			case strings.HasPrefix(whole, "node --test"):
				out = append(out, gate{"nodetest", ""})
			default:
				out = append(out, gate{"npm", m[2]})
			}
		}
	}
	return out
}

// normalizeTarget reduces a Go package pattern to what is worth comparing.
//
// CI and a justfile reach the same packages by different routes: one does
// `cd engine && go test ./...` and the other `go test ./...` from a recipe
// that already set the directory. Comparing the raw strings would report
// drift that is not drift.
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
// invokes. Read by looking at which `just <name>` calls appear inside the gate
// recipe, against the recipes the file defines.
func uncalledByGate(just string) []string {
	lines := strings.Split(just, "\n")

	// The body of the `gate` recipe: from its header to the next unindented
	// line that is not blank or a comment.
	var body []string
	in := false
	for _, line := range lines {
		if strings.HasPrefix(line, "gate:") || strings.HasPrefix(line, "gate ") {
			in = true
			continue
		}
		if in {
			if line != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") &&
				!strings.HasPrefix(line, "#") {
				break
			}
			body = append(body, line)
		}
	}
	if len(body) == 0 {
		return []string{"(no `gate` recipe at all)"}
	}

	called := map[string]bool{}
	callRe := regexp.MustCompile(`just ([a-z][\w-]*)`)
	for _, line := range body {
		for _, m := range callRe.FindAllStringSubmatch(line, -1) {
			called[m[1]] = true
		}
	}

	// Recipes that `gate` does not call because the gate itself is exempt.
	// Kept apart from the convenience list below on purpose: a convenience is
	// something that is not a gate at all, and calling `vuln` one would be
	// untrue in the direction that matters. It is a gate; it runs in a workflow;
	// it is out of `gate` for the reason recorded in exemptFromGate.
	exemptRecipes := map[string]bool{
		"vuln": true,
	}

	// Recipes that are gates rather than conveniences. A recipe that mutates
	// (fmt, generate, db, build, clean) is not something `gate` should run.
	convenience := map[string]bool{
		"default": true, "setup": true, "db": true, "db-down": true, "deps": true,
		"build": true, "build-release": true, "test": true, "test-short": true,
		"fmt": true, "generate": true, "clean": true, "gate": true, "leaks": true,
	}

	recipeRe := regexp.MustCompile(`^([a-z][\w-]*)(?: [\w"=]+)*:`)
	var uncalled []string
	for _, line := range lines {
		m := recipeRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		name := m[1]
		if strings.HasPrefix(name, "_") || convenience[name] || exemptRecipes[name] || called[name] {
			continue
		}
		uncalled = append(uncalled, name)
	}
	sort.Strings(uncalled)
	return uncalled
}

func sortedKeys(m map[string]struct{}) []string {
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

// sortedExemptions keeps the failure output stable. sortedKeys works on the
// gate sets, which are map[string]struct{}; this map carries reasons.
func sortedExemptions() []string {
	out := make([]string, 0, len(exemptFromGate))
	for k := range exemptFromGate {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (w workflowSet) text() string { return strings.Join(w.sources, "\n") }

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
