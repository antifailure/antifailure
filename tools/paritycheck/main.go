// Command paritycheck refuses a customer facing capability that exists on one
// surface and silently on no other.
//
// THE FAILURE IT WAS WRITTEN FOR. The concurrent SQL workload, which is the
// answer to "reproduce a production shaped concurrent workload", shipped as
// `af load sql` and was reachable from nothing else. `grep -rn SQLLoad
// engine/internal/mcp` returned nothing at all, so an agent driving the MCP
// server could rehearse a migration, send HTTP traffic, drive a browser and
// explore, and could not ask the one question somebody changing an index, a
// lock, a storage parameter or a query is actually asking. The capability was
// written, tested, documented and half delivered, and every instrument in this
// repository was green the whole time, because not one of them asks this
// question.
//
// WHY NOTHING ELSE CATCHES IT. All 80 tools in this directory were read before
// this one was written. tools/surfacecheck is about API STABILITY: which
// packages may be imported from outside the module and whether an export
// changed shape. tools/wirecheck is about whether a documented VARIABLE can be
// delivered by an installation route. tools/routecheck is about whether a
// route the site calls is served. Every one of them answers a nearby question,
// and a capability reachable from exactly one surface passes all of them
// cleanly. That is how this one survived: the instruments were green and could
// not have been anything else.
//
// WHAT A CAPABILITY IS HERE, and why it is read from the code rather than from
// a list somebody maintains. A hand written inventory of capabilities is the
// same drift with an extra step, and this repository has said so before. So
// the inventory is the exported method set of *env.Orchestrator, which is the
// engine's own capability API: the single type that both surfaces drive. That
// is not an assumption. Exactly three packages in the engine import
// engine/internal/env, and this gate reads that set from the imports rather
// than believing it:
//
//	engine/internal/cli       the command line
//	engine/internal/workload  the hosted workload runner, imported only by the cli
//	engine/internal/mcp       the MCP server
//
// A FOURTH importer fails this gate rather than being ignored. A new surface
// this gate cannot classify is "I could not look", which is a different fact
// from "I looked and it was fine", and conflating the two is the specific
// defect this repository keeps finding in its own instruments.
//
// THE RULE. Every capability is reachable from every surface, or it carries a
// row in tools/docs/surface-exemptions.tsv saying which kind of gap it is and
// why. A capability that is neither fails.
//
// THE KINDS ARE NOT FREE TEXT, and that is the half that makes the exemption
// file worth having. Each one carries a structural precondition this gate
// checks, so a row cannot be pasted onto an inconvenient capability to quiet
// it:
//
//	accessor   the method takes no context.Context. It reports a value its
//	           caller already has rather than doing work. SQLLoad takes a
//	           context, so it can never be filed as one.
//	cli-only   reachable from the command line and from no other surface.
//	           The row goes stale and fails the moment it is wired up.
//	mcp-only   the mirror image, and it is a real gap too: a capability an
//	           agent has and a person at a terminal does not.
//	unreached  reachable from no surface at all. Dead, or reached only from
//	           inside the engine, and it has to say which.
//
// WHAT THIS CANNOT SEE, stated here rather than left for somebody to discover,
// because a gate that names what it did not check is the only kind whose
// silence means anything.
//
//  1. It resolves a call site with the type checker, so a name it reports is
//     the Orchestrator's method and not another type's method of the same name.
//     The first version matched by NAME instead and reported 109 call sites for
//     Status, most of them another type's field: that is the false PASS
//     direction and it is why it was replaced. What remains is the interface
//     hop below. A call on an interface is counted when the Orchestrator
//     implements that interface, which is a full signature match and not a
//     name match, but it is not proof that an orchestrator is what is passed
//     in. That is the one place this gate can say yes on thin evidence, and
//     -list names the interface behind every such site.
//  2. Its axis is the Orchestrator. A capability implemented somewhere else
//     entirely, as explain_error and the documentation tools are, is outside
//     what it can see, and it says so in its own summary rather than implying
//     a coverage it does not have.
//  3. It says nothing about whether the two surfaces expose a capability WELL.
//     A tool that reaches SQLLoad with every knob nailed shut would pass here.
//     That is a review question and this is a reachability gate.
//  4. A method declared in engine/internal/env/export_test.go is not part of
//     the shipped package, so it is never loaded and never needs an excuse.
//     The six ForTest methods live there today. One declared in a shipped file
//     would appear here as reached by no surface, which is the right way round.
//  5. It reads the packages as they COMPILE. A package with a type error is
//     refused rather than read partially, because a scan of a broken tree that
//     found no gaps has not found that there are none.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

const (
	// modulePrefix is what turns an import path back into a repository path.
	modulePrefix = "github.com/antifailure/antifailure/"
	// capabilityPkg is the directory holding the capability API.
	capabilityPkg = "engine/internal/env"
	// capabilityType is the receiver whose exported methods are the inventory.
	capabilityType = "Orchestrator"
	// exemptionsPath carries the gaps that are allowed, with their reasons.
	exemptionsPath = "tools/docs/surface-exemptions.tsv"

	// minCapabilities is the floor under the inventory. A scan that found
	// nothing would otherwise report every surface complete, which is the
	// shape of check this repository keeps catching in its own instruments.
	// The method set was 73 when this was written.
	minCapabilities = 50
	// minCallSites is the floor under each surface's scan, for the same
	// reason. The command line resolved 85 and the MCP server 42.
	minCallSites = 20
)

var capabilityImport = modulePrefix + capabilityPkg

// surface is one way a customer reaches the engine.
//
// The trees are listed rather than discovered, and the importer check below is
// what stops the list being a lie: a package that imports the capability API
// and is not named here fails, so this cannot silently fall behind the tree.
type surface struct {
	key  string
	what string
	// dirs are the package directories that make up this surface.
	dirs []string
	// fix is the sentence printed when something is missing from it.
	fix string
}

var surfaces = []surface{
	{
		key: "cli", what: "the command line",
		dirs: []string{"engine/internal/cli", workloadPkg},
		fix:  "a cobra command under engine/internal/cli",
	},
	{
		key: "mcp", what: "the MCP server",
		dirs: []string{"engine/internal/mcp"},
		fix:  "a tool under engine/internal/mcp, registered in Serve",
	},
}

// workloadPkg is classified as part of the command line's surface rather than
// being one of its own.
//
// Checked rather than assumed, because the classification above rests on it.
// If something else starts importing the workload runner, the claim that it is
// part of the command line becomes false, and this gate would be reporting a
// capability as reachable from a surface a customer cannot reach.
const workloadPkg = "engine/internal/workload"

const workloadImportedOnlyBy = "engine/internal/cli"

// capability is one exported method on the capability type.
type capability struct {
	name string
	// where is the file and line it is declared at.
	where string
	// takesContext says the first parameter is a context.Context, which is
	// what separates a method that does work from one that reports a value.
	takesContext bool
	// internalOnly says the signature names a type unexported from the
	// capability package, so no other package can call it whatever it is
	// named.
	internalOnly bool
	// internalWhy names the type that makes it so.
	internalWhy string
}

// row is one line of the exemption file.
type row struct {
	name   string
	kind   string
	reason string
	line   int
}

// The kinds a row may claim. Each one is checked, and a kind outside this set
// is an error rather than a row that excuses everything.
const (
	kindAccessor  = "accessor"
	kindCLIOnly   = "cli-only"
	kindMCPOnly   = "mcp-only"
	kindUnreached = "unreached"
)

var knownKinds = []string{kindAccessor, kindCLIOnly, kindMCPOnly, kindUnreached}

func main() {
	list := flag.Bool("list", false,
		"print every capability, its reachability and the call sites behind it, then exit")
	exemptOnly := flag.Bool("exemptions", false, "print the exemption file's effective rows and exit")
	flag.Parse()
	root := "."
	if flag.NArg() > 0 {
		root = flag.Arg(0)
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		fail(err)
	}

	files, err := tracked(root)
	if err != nil {
		fail(err)
	}
	if err := checkSurfacesAreTheWholeStory(root, files); err != nil {
		fail(err)
	}

	loaded, err := load(abs)
	if err != nil {
		fail(err)
	}

	caps, recv, err := capabilities(loaded)
	if err != nil {
		fail(err)
	}
	if err := floorCheck(
		fmt.Sprintf("exported methods on *%s.%s", path.Base(capabilityPkg), capabilityType),
		len(caps), minCapabilities,
		"the capability API has moved or the load is broken"); err != nil {
		fail(err)
	}

	reach := map[string]map[string][]site{}
	for _, s := range surfaces {
		found := callSites(loaded, s.dirs, recv)
		total := 0
		for _, sites := range found {
			total += len(sites)
		}
		if err := floorCheck(
			fmt.Sprintf("call sites into the capability API from %s", strings.Join(s.dirs, ", ")),
			total, minCallSites, "that surface was not really read"); err != nil {
			fail(err)
		}
		reach[s.key] = found
	}

	rows, err := exemptions(root)
	if err != nil {
		fail(err)
	}

	if *exemptOnly {
		for _, r := range sortedRows(rows) {
			fmt.Printf("%s\t%s\t%s\n", r.name, r.kind, r.reason)
		}
		return
	}
	if *list {
		printList(caps, reach, rows)
		return
	}
	report(caps, reach, rows)
}

// load type checks the capability package and every surface package.
//
// Type checked rather than parsed, because the question is which method a call
// site actually reaches and a name cannot answer that. The cost is that the
// engine has to compile, which is the right dependency: a gate that reported a
// clean tree from source it could not type check would be reporting that it
// could not look.
func load(absRoot string) (*loadedTree, error) {
	patterns := []string{"./" + strings.TrimPrefix(capabilityPkg, "engine/")}
	for _, s := range surfaces {
		for _, d := range s.dirs {
			patterns = append(patterns, "./"+strings.TrimPrefix(d, "engine/"))
		}
	}
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedSyntax | packages.NeedTypes |
			packages.NeedTypesInfo | packages.NeedDeps | packages.NeedImports,
		Dir: filepath.Join(absRoot, "engine"),
	}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, fmt.Errorf("loading the engine's packages: %w", err)
	}

	tree := &loadedTree{root: absRoot, byDir: map[string]*packages.Package{}}
	for _, p := range pkgs {
		if len(p.Errors) > 0 {
			return nil, fmt.Errorf(
				"%s did not type check, so this gate could not read it and its silence "+
					"would mean nothing: %v", p.PkgPath, p.Errors[0])
		}
		if p.TypesInfo == nil || p.Types == nil {
			return nil, fmt.Errorf("%s loaded without type information", p.PkgPath)
		}
		tree.byDir[strings.TrimPrefix(p.PkgPath, modulePrefix)] = p
	}
	for _, want := range append([]string{capabilityPkg}, allSurfaceDirs()...) {
		if tree.byDir[want] == nil {
			return nil, fmt.Errorf("%s was asked for and did not load", want)
		}
	}
	return tree, nil
}

type loadedTree struct {
	root  string
	byDir map[string]*packages.Package
}

// at renders a position as a repository relative file and line.
func (t *loadedTree) at(fset *token.FileSet, pos token.Pos) string {
	p := fset.Position(pos)
	name := strings.TrimPrefix(strings.TrimPrefix(p.Filename, t.root), string(filepath.Separator))
	return fmt.Sprintf("%s:%d", filepath.ToSlash(name), p.Line)
}

func allSurfaceDirs() []string {
	var out []string
	for _, s := range surfaces {
		out = append(out, s.dirs...)
	}
	return out
}

// capabilities reads the exported method set of the capability type, and
// returns the receiver a call site has to resolve to.
func capabilities(t *loadedTree) ([]capability, types.Type, error) {
	pkg := t.byDir[capabilityPkg]
	obj := pkg.Types.Scope().Lookup(capabilityType)
	if obj == nil {
		return nil, nil, fmt.Errorf(
			"%s declares no type %s, so the capability API has moved and this gate is "+
				"measuring nothing", capabilityPkg, capabilityType)
	}
	named, isNamed := obj.Type().(*types.Named)
	if !isNamed {
		return nil, nil, fmt.Errorf("%s.%s is not a named type", capabilityPkg, capabilityType)
	}
	recv := types.NewPointer(named)

	var out []capability
	set := types.NewMethodSet(recv)
	for i := range set.Len() {
		fn, isFunc := set.At(i).Obj().(*types.Func)
		if !isFunc || !fn.Exported() {
			continue
		}
		sig, isSig := fn.Type().(*types.Signature)
		if !isSig {
			continue
		}
		c := capability{name: fn.Name(), where: t.at(pkg.Fset, fn.Pos())}
		if sig.Params().Len() > 0 {
			c.takesContext = types.TypeString(sig.Params().At(0).Type(), nil) == "context.Context"
		}
		c.internalWhy = unexportedTypeIn(sig, pkg.Types)
		c.internalOnly = c.internalWhy != ""
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, recv, nil
}

// unexportedTypeIn names the first type in the signature that the capability
// package does not export, or the empty string.
//
// A method whose signature names one of those cannot be called from another
// package at all, whatever it is named, so it is not a capability of any
// surface. A decidable narrowing rather than a judgement, which is the only
// kind worth having here: env.MaskingKey and env.ReportDecisions both take a
// *session, so no surface can call either and neither needs a written excuse.
func unexportedTypeIn(sig *types.Signature, own *types.Package) string {
	var found string
	var walk func(t types.Type, depth int)
	walk = func(t types.Type, depth int) {
		if found != "" || t == nil || depth > 8 {
			return
		}
		switch typ := t.(type) {
		case *types.Named:
			if obj := typ.Obj(); obj != nil && obj.Pkg() == own && !obj.Exported() {
				found = obj.Name()
				return
			}
		case *types.Pointer:
			walk(typ.Elem(), depth+1)
		case *types.Slice:
			walk(typ.Elem(), depth+1)
		case *types.Array:
			walk(typ.Elem(), depth+1)
		case *types.Map:
			walk(typ.Key(), depth+1)
			walk(typ.Elem(), depth+1)
		case *types.Chan:
			walk(typ.Elem(), depth+1)
		case *types.Signature:
			walkTuple(typ.Params(), walk, depth+1)
			walkTuple(typ.Results(), walk, depth+1)
		}
	}
	walkTuple(sig.Params(), walk, 0)
	walkTuple(sig.Results(), walk, 0)
	return found
}

func walkTuple(tuple *types.Tuple, walk func(types.Type, int), depth int) {
	if tuple == nil {
		return
	}
	for i := range tuple.Len() {
		walk(tuple.At(i).Type(), depth)
	}
}

// callSites finds where each capability is reached from a set of packages.
//
// A use rather than a call, because a method handed somewhere as a VALUE is a
// call waiting to happen and every MCP tool in this repository is built by
// handing exactly such a value to a constructor.
//
// TWO WAYS TO REACH ONE, and the second is not a convenience. Both surfaces
// call the orchestrator through small local INTERFACES so the wiring can be
// tested against a fake: engine/internal/cli declares accessProber,
// changeReader and reviewReader, and engine/internal/workload declares Runner.
// The first version of this counted only the concrete receiver and reported
// AccessProbe, BaselineTwin, CodeFiles, DependencyFiles and FileContent as
// reachable from no surface at all, every one of which the command line calls.
// Five false gaps would have meant five written excuses for capabilities that
// are not missing, and an exemption file full of those is one nobody reads.
//
// So a use of an INTERFACE method counts when the capability type implements
// that interface. types.Implements is a full signature match rather than a
// name match, which is what keeps this from being the name matching this gate
// replaced. What it cannot prove is that an orchestrator is what is actually
// passed in; -list names the interface behind every such site so the claim can
// be read.
func callSites(t *loadedTree, dirs []string, recv types.Type) map[string][]site {
	out := map[string][]site{}
	for _, d := range dirs {
		pkg := t.byDir[d]
		if pkg == nil {
			continue
		}
		for ident, obj := range pkg.TypesInfo.Uses {
			fn, isFunc := obj.(*types.Func)
			if !isFunc {
				continue
			}
			sig, isSig := fn.Type().(*types.Signature)
			if !isSig || sig.Recv() == nil {
				continue
			}
			found := site{where: t.at(pkg.Fset, ident.Pos())}
			switch {
			case types.Identical(sig.Recv().Type(), recv):
			case reachesThroughInterface(sig.Recv().Type(), recv):
				found.through = types.TypeString(sig.Recv().Type(), relativeTo(pkg.Types))
			default:
				continue
			}
			out[fn.Name()] = append(out[fn.Name()], found)
		}
	}
	for name := range out {
		sort.Slice(out[name], func(i, j int) bool { return out[name][i].where < out[name][j].where })
	}
	return out
}

// site is one place a capability is reached from, and how firmly.
//
// A struct rather than a formatted string, because the count of the thin ones
// is printed in the summary and a count derived from parsing a string this same
// file formatted would read zero the day somebody changed the format. Zero
// there says "every call site is concrete", which is the strongest claim this
// gate makes and would be made by accident.
type site struct {
	where string
	// through names the interface, when the call landed on one the capability
	// type implements rather than on the type itself. Empty for a concrete
	// call.
	through string
}

// onlyThroughInterfaces reports a surface that reaches a capability by no
// concrete call at all.
//
// This is the thin evidence. types.Implements is a full signature match, so the
// capability type CAN satisfy the interface, and that is not proof that one is
// what gets passed. Counted and printed rather than hidden, because a gate that
// says yes on weaker evidence than usual has to say which answers those were.
func onlyThroughInterfaces(sites []site) bool {
	if len(sites) == 0 {
		return false
	}
	for _, s := range sites {
		if s.through == "" {
			return false
		}
	}
	return true
}

// reachesThroughInterface reports whether a call on this receiver can land on
// the capability type.
func reachesThroughInterface(receiver, recv types.Type) bool {
	iface, isIface := receiver.Underlying().(*types.Interface)
	return isIface && types.Implements(recv, iface)
}

// relativeTo renders a type name the way somebody reading that package would
// write it.
func relativeTo(pkg *types.Package) types.Qualifier {
	return func(other *types.Package) string {
		if other == pkg {
			return ""
		}
		return other.Name()
	}
}

// printList is the evidence behind every claim this gate makes.
func printList(caps []capability, reach map[string]map[string][]site, rows map[string]row) {
	for _, c := range caps {
		marks := make([]string, 0, len(surfaces))
		for _, s := range surfaces {
			mark := s.key + "=no"
			if n := len(reach[s.key][c.name]); n > 0 {
				mark = fmt.Sprintf("%s=%d", s.key, n)
			}
			marks = append(marks, mark)
		}
		note := ""
		switch {
		case c.internalOnly:
			note = "\tinternal-only\t" + c.internalWhy
		case rows[c.name].kind != "":
			note = "\t" + rows[c.name].kind + "\t" + rows[c.name].reason
		}
		fmt.Printf("%s\t%s\t%s%s\n", c.name, c.where, strings.Join(marks, " "), note)
		for _, s := range surfaces {
			for _, st := range reach[s.key][c.name] {
				if st.through == "" {
					fmt.Printf("\t%s\t%s\n", s.key, st.where)
					continue
				}
				fmt.Printf("\t%s\t%s\tthrough %s\n", s.key, st.where, st.through)
			}
		}
	}
}

// floorCheck refuses an implausibly quiet read.
//
// A scan that found nothing reports every surface complete, which is the exact
// shape of check this repository keeps catching in its own instruments: the
// verdict is indistinguishable from a clean tree and it was measured over an
// empty set.
func floorCheck(what string, got, min int, because string) error {
	if got >= min {
		return nil
	}
	return fmt.Errorf(
		"only %d %s were found and at least %d were expected, so this scan read almost "+
			"nothing and its verdict would mean nothing: %s", got, what, min, because)
}

// gaps is the whole decision, separated from the printing so a test can put a
// tree to it and read the answer rather than the exit status.
func gaps(
	caps []capability, reach map[string]map[string][]site, rows map[string]row,
) (uncovered, wrongKind, stale []string, covered, exempt, internal int) {

	for _, c := range caps {
		if c.internalOnly {
			// Nothing outside the capability package can call it whatever it
			// is named, so it is not a capability of any surface. Counted and
			// reported rather than dropped in silence.
			internal++
			continue
		}
		missing := missingFrom(c, reach)
		r, hasRow := rows[c.name]

		if len(missing) == 0 {
			covered++
			if hasRow {
				stale = append(stale, fmt.Sprintf(
					"%s is exempt as %s at %s:%d and is now reachable from every surface, "+
						"so the row can go", c.name, r.kind, exemptionsPath, r.line))
			}
			continue
		}
		if !hasRow {
			uncovered = append(uncovered, fmt.Sprintf(
				"%s is declared at %s and is reachable from %s",
				c.name, c.where, describeReach(c, reach)))
			continue
		}
		exempt++
		if why := kindHolds(c, r, reach); why != "" {
			wrongKind = append(wrongKind, why)
		}
	}

	// A row for a capability that no longer exists. Staleness that is not
	// reported is an allowance that outlives its argument, which is the half
	// tools/wirecheck, tools/figurecheck and tools/varcheck all carry.
	known := map[string]bool{}
	for _, c := range caps {
		known[c.name] = true
	}
	for name, r := range rows {
		if !known[name] {
			stale = append(stale, fmt.Sprintf(
				"%s is exempt at %s:%d and *%s.%s has no such method any more, so the row "+
					"can go", name, exemptionsPath, r.line, path.Base(capabilityPkg), capabilityType))
		}
	}

	sort.Strings(uncovered)
	sort.Strings(stale)
	sort.Strings(wrongKind)
	return uncovered, wrongKind, stale, covered, exempt, internal
}

// report prints what gaps decided and exits.
func report(caps []capability, reach map[string]map[string][]site, rows map[string]row) {
	uncovered, wrongKind, stale, covered, exempt, internal := gaps(caps, reach, rows)

	if len(uncovered) == 0 && len(stale) == 0 && len(wrongKind) == 0 {
		fmt.Printf("paritycheck: %d capabilities on *%s.%s across %d surfaces\n",
			len(caps), path.Base(capabilityPkg), capabilityType, len(surfaces))
		fmt.Printf("  %d reachable from every surface, %d exempt with a checked reason, "+
			"%d uncallable from outside the package\n", covered, exempt, internal)
		if thin := thinlyCovered(caps, reach); len(thin) > 0 {
			fmt.Printf("  %d reached by no concrete call on some surface, counted on the "+
				"strength of the capability type implementing an interface rather than of "+
				"one provably being passed: %s\n", len(thin), strings.Join(thin, ", "))
		}
		fmt.Printf("  NOT CHECKED: a capability that is not a method on *%s.%s is outside "+
			"this gate's axis. Run -list for the line behind every claim, and the "+
			"interface behind every one that has one.\n",
			path.Base(capabilityPkg), capabilityType)
		return
	}

	for _, f := range uncovered {
		fmt.Fprintf(os.Stderr, "  %s\n", f)
	}
	for _, f := range wrongKind {
		fmt.Fprintf(os.Stderr, "  %s\n", f)
	}
	for _, f := range stale {
		fmt.Fprintf(os.Stderr, "  %s\n", f)
	}
	fmt.Fprintf(os.Stderr,
		"\nparitycheck: %d capabilities reachable from one surface and no other, "+
			"%d exemption rows whose kind does not hold, %d stale rows.\n",
		len(uncovered), len(wrongKind), len(stale))
	if len(uncovered) > 0 {
		for _, s := range surfaces {
			fmt.Fprintf(os.Stderr, "Reach it from %s by adding %s. ", s.what, s.fix)
		}
		fmt.Fprintf(os.Stderr,
			"\nOr add a row to %s naming the kind of gap and why it is allowed to stay.\n",
			exemptionsPath)
	}
	os.Exit(1)
}

// thinlyCovered names every capability whose reach from some surface is
// entirely through an interface.
//
// The summary prints the count. A gate that says yes on weaker evidence than
// usual has to say WHICH answers those were, or the weaker evidence quietly
// becomes the standard.
func thinlyCovered(caps []capability, reach map[string]map[string][]site) []string {
	var thin []string
	for _, c := range caps {
		for _, s := range surfaces {
			if onlyThroughInterfaces(reach[s.key][c.name]) {
				thin = append(thin, fmt.Sprintf("%s from %s", c.name, s.key))
			}
		}
	}
	sort.Strings(thin)
	return thin
}

// missingFrom names the surfaces a capability cannot be reached from.
func missingFrom(c capability, reach map[string]map[string][]site) []string {
	var missing []string
	for _, s := range surfaces {
		if len(reach[s.key][c.name]) == 0 {
			missing = append(missing, s.key)
		}
	}
	return missing
}

// describeReach names the surfaces a capability IS reachable from, for the
// failure message. "reachable from nothing" and "reachable from the command
// line only" are different bugs and the message has to say which.
func describeReach(c capability, reach map[string]map[string][]site) string {
	var have []string
	for _, s := range surfaces {
		if n := len(reach[s.key][c.name]); n > 0 {
			have = append(have, fmt.Sprintf("%s (%d call sites)", s.what, n))
		}
	}
	if len(have) == 0 {
		return "no surface at all"
	}
	return strings.Join(have, " and ") + " and from nothing else"
}

// kindHolds checks the structural precondition the row's kind claims, and
// returns the sentence to print when it does not.
//
// This is what stops the exemption file being a place to write anything. A
// kind is a claim about the shape of the method, and a claim that does not
// hold is worse than no row, because the row is what stops anybody asking.
func kindHolds(c capability, r row, reach map[string]map[string][]site) string {
	at := fmt.Sprintf("%s:%d", exemptionsPath, r.line)
	reachedBy := func(key string) bool { return len(reach[key][c.name]) > 0 }

	switch r.kind {
	case kindAccessor:
		if c.takesContext {
			return fmt.Sprintf(
				"%s is exempt at %s as an accessor and it takes a context.Context, so it "+
					"does work rather than reporting a value its caller already has. An "+
					"accessor is the one kind that cannot be claimed for a capability",
				c.name, at)
		}
	case kindCLIOnly:
		if !reachedBy("cli") {
			return fmt.Sprintf(
				"%s is exempt at %s as reachable from the command line only and the "+
					"command line does not reach it either", c.name, at)
		}
	case kindMCPOnly:
		if !reachedBy("mcp") {
			return fmt.Sprintf(
				"%s is exempt at %s as reachable from the MCP server only and the MCP "+
					"server does not reach it either", c.name, at)
		}
	case kindUnreached:
		for _, s := range surfaces {
			if reachedBy(s.key) {
				return fmt.Sprintf(
					"%s is exempt at %s as reached by no surface and %s reaches it at %s",
					c.name, at, s.what, reach[s.key][c.name][0].where)
			}
		}
	}
	return ""
}

// checkSurfacesAreTheWholeStory refuses a tree this gate's surface list no
// longer describes.
//
// Two ways it can go wrong and both are silent. A package that starts
// importing the capability API and is not listed here is a surface nobody is
// checking, and every capability it alone reaches would read as unreachable. A
// listed directory that imports nothing means the tree moved under the list,
// and the surface would be reported as complete having been read from an empty
// set.
func checkSurfacesAreTheWholeStory(root string, files []string) error {
	listed := map[string]string{}
	for _, s := range surfaces {
		for _, d := range s.dirs {
			listed[d] = s.key
		}
	}

	importers, err := importersOf(root, files, capabilityImport)
	if err != nil {
		return err
	}
	if len(importers) == 0 {
		return fmt.Errorf(
			"no package in the engine imports %s, so either the capability API has moved "+
				"or this scan read nothing", capabilityImport)
	}

	var unlisted, empty []string
	for dir := range importers {
		if _, ok := listed[dir]; !ok {
			unlisted = append(unlisted, dir)
		}
	}
	for dir := range listed {
		if !importers[dir] {
			empty = append(empty, dir)
		}
	}
	sort.Strings(unlisted)
	sort.Strings(empty)

	if len(unlisted) > 0 {
		return fmt.Errorf(
			"%s imports %s and is not one of the surfaces this gate knows, so it is a way "+
				"to reach the capability API that nothing is checking. Add it to the "+
				"surface list in tools/paritycheck, classified, rather than leaving this "+
				"gate to report a coverage it did not measure",
			strings.Join(unlisted, ", "), capabilityImport)
	}
	if len(empty) > 0 {
		return fmt.Errorf(
			"%s is listed as a surface and imports nothing from %s, so this gate would "+
				"have measured that surface against an empty set and called every "+
				"capability unreachable from it",
			strings.Join(empty, ", "), capabilityImport)
	}

	// The workload runner is classified as part of the command line's surface.
	// That is true only while the command line is the only thing that reaches
	// it, and this is where that is established rather than believed.
	reachers, err := importersOf(root, files, modulePrefix+workloadPkg)
	if err != nil {
		return err
	}
	for dir := range reachers {
		if dir != workloadImportedOnlyBy && dir != workloadPkg {
			return fmt.Errorf(
				"%s imports %s, which this gate counts as part of %s's surface because "+
					"only %s reached it. That classification is now false, and a capability "+
					"reached only through the workload runner would be reported as "+
					"reachable from a surface that cannot reach it",
				dir, workloadPkg, workloadImportedOnlyBy, workloadImportedOnlyBy)
		}
	}
	return nil
}

// importersOf reads the import graph from the index, and names the package
// directories that import one path.
func importersOf(root string, files []string, want string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, f := range files {
		if !isEngineGo(f) {
			continue
		}
		fset := token.NewFileSet()
		parsed, err := parser.ParseFile(fset, filepath.Join(root, f), nil, parser.ImportsOnly)
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", f, err)
		}
		for _, spec := range parsed.Imports {
			if strings.Trim(spec.Path.Value, `"`) == want {
				out[path.Dir(f)] = true
			}
		}
	}
	return out, nil
}

// exemptions reads the file, refusing a row this gate cannot act on.
func exemptions(root string) (map[string]row, error) {
	body, err := os.ReadFile(filepath.Join(root, exemptionsPath))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", exemptionsPath, err)
	}
	known := map[string]bool{}
	for _, k := range knownKinds {
		known[k] = true
	}

	rows := map[string]row{}
	for i, line := range strings.Split(string(body), "\n") {
		n := i + 1
		text := strings.TrimSpace(line)
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 3 {
			return nil, fmt.Errorf(
				"%s:%d has %d tab separated fields and needs exactly three, the method, "+
					"the kind and the reason. A row with no reason is the staleness this "+
					"file exists to prevent, arriving on day one",
				exemptionsPath, n, len(fields))
		}
		name := strings.TrimSpace(fields[0])
		kind := strings.TrimSpace(fields[1])
		reason := strings.TrimSpace(fields[2])
		if !known[kind] {
			return nil, fmt.Errorf(
				"%s:%d claims the kind %q and the kinds this gate checks are %s",
				exemptionsPath, n, kind, strings.Join(knownKinds, ", "))
		}
		if len(reason) < 20 {
			return nil, fmt.Errorf(
				"%s:%d gives the reason %q, which is too short to be one. The reason is "+
					"the whole value of the row: it is what the next person reads instead "+
					"of asking", exemptionsPath, n, reason)
		}
		if _, duplicate := rows[name]; duplicate {
			return nil, fmt.Errorf("%s:%d names %s a second time", exemptionsPath, n, name)
		}
		rows[name] = row{name: name, kind: kind, reason: reason, line: n}
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf(
			"%s parsed to no rows at all, so every gap in the tree would be reported as "+
				"unexcused. Either the file is empty or its shape has changed", exemptionsPath)
	}
	return rows, nil
}

func sortedRows(rows map[string]row) []row {
	out := make([]row, 0, len(rows))
	for _, r := range rows {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// isEngineGo is a shipped Go file in the engine. A test file reaches nothing a
// customer can reach.
func isEngineGo(f string) bool {
	return strings.HasPrefix(f, "engine/") &&
		strings.HasSuffix(f, ".go") && !strings.HasSuffix(f, "_test.go")
}

// tracked asks git for the index rather than walking the working tree.
//
// The same decision tools/gatecheck made after the version that walked the
// tree read an untracked scratch file and refused a tree CI was happy with.
func tracked(root string) ([]string, error) {
	cmd := exec.Command("git", "-C", root, "ls-files", "-z")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files in %s: %w: %s", root, err, strings.TrimSpace(stderr.String()))
	}
	var files []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" {
			files = append(files, p)
		}
	}
	return files, nil
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "paritycheck: %v\n", err)
	os.Exit(1)
}
