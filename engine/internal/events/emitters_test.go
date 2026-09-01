package events_test

// Which catalog types anything actually emits.
//
// The package comment says everything the engine does emits a typed event, the
// reference page under docs is generated from the catalog, and a customer
// filters on what that page lists. None of those three facts is checked against
// the engine, and the gap they hid was large: of fifty-two documented types the
// engine emitted nineteen. The dashboard's database pane was drawn from six
// that nothing produced, and the control plane sink mapped four more.
//
// A linter cannot see this. Every one of those constants is referenced: by the
// typeDocs map that documents it, by a display that switches on it, by a sink
// that translates it. Referenced is not emitted, and only the second one puts
// anything in front of a user.
//
// So this test finds the emit call sites by parsing the engine and compares
// them to the catalog. The exemption list below is the point of it: a type with
// no emitter is allowed, and it has to be written down with a reason, so that
// adding a documented type nothing produces is a decision somebody made rather
// than a page that quietly describes a stream nobody receives.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/events"
)

// engineRoot is this module's root, two directories up from internal/events.
const engineRoot = "../.."

// emitFuncs are the calls that put an event on a bus.
//
// Bus.Emit and the four level helpers are the whole public surface, and
// internal/env wraps them in event, eventErr and emit so that a nil session is
// tolerated at one place rather than at every call site. A new wrapper that is
// not listed here reads as "nothing emits this" and fails loudly, which is the
// right direction: the alternative is a wrapper that silently empties this
// check.
var emitFuncs = map[string]bool{
	"Emit": true, "Info": true, "Warn": true, "Error": true, "Debug": true,
	"event": true, "eventErr": true, "emit": true,
}

// unemitted are catalog types nothing produces, each with the reason.
//
// Every entry here is a documented capability that does not happen. They are
// listed rather than removed because the types are the design of features that
// are partly built, and deleting them would hide the gap instead of recording
// it. Emitting one is what takes it off this list.
var unemitted = map[events.Type]string{
	events.EnvSleeping: "nothing puts an environment to sleep. runtime.idle_sleep is " +
		"normalized, validated and printed by af explain, and read by nothing.",
	events.EnvWaking: "same: there is no sleep, so there is no wake.",
	events.ResourceLeaked: "there is no leak detector. The journal records creates and " +
		"deletes so that one has a ledger to compare provider inventory against; nothing " +
		"compares them.",
	events.GoldenFailed:    "a failed refresh returns an error and emits env.failed rather than this.",
	events.GoldenCollected: "retention deletes old goldens and says so nowhere on the stream.",
	events.DBReset:         "af db reset does not exist.",
	events.DBDestroyed: "a branch is deleted through the journal, which emits " +
		"resource.deleted with the kind. Nothing emits the database specific form.",
	events.ServiceLog: "deliberate, and the one entry here that is not a gap. Runtime " +
		"progress lines are engine.progress: the non-TTY fallback folds service.log away as " +
		"noise, correctly, and folding these away restored exactly the silence it exists to end.",
	events.ServiceRestart:   "the local runtime does not restart a service that exits.",
	events.CronFired:        "a cron service is started and its invocations are not reported.",
	events.EgressTripwire:   "no tripwire exists.",
	events.CaptureMessage:   "captured messages reach the inbox and not the stream.",
	events.WebhookQueued:    "internal/webhook names providers and events; nothing delivers one.",
	events.WebhookDelivered: "same.",
	events.WebhookFailed:    "same.",
	events.AgentStep: "the runner reports each step over its own JSON boundary, inside the " +
		"workflow result, and nothing turns those into events. The run, its verdicts and its " +
		"outcome do reach the bus now, through Orchestrator.reportRunStarted, reportVerdicts " +
		"and reportRunFinished; the individual steps do not.",
	events.InsightFinding: "insights are returned to the caller and rendered; they are " +
		"not put on the stream.",
	events.LoadSample:   "load results are returned to the caller in the same way.",
	events.LoadFinished: "same.",
	events.Error: "the error path uses env.failed at error level rather than a " +
		"generic engine.error.",
	events.Retry: "retries happen in the journal and in the providers and are not " +
		"reported as their own event.",
	events.SinkDropped: "drops are counted on the bus and read by Bus.Drops, which the " +
		"dashboard and af status print. Nothing emits an event about them, which would in " +
		"any case be an event delivered to the sink that is behind.",
}

// emittedTypes parses the engine and returns every catalog type passed to an
// emit call in non-test code.
func emittedTypes(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	fset := token.NewFileSet()

	err := filepath.WalkDir(engineRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// The root is skipped by name otherwise: WalkDir reports it as
			// "..", which starts with a dot and is not a hidden directory. The
			// first version of this did exactly that and scanned nothing, which
			// is why the test above refuses an empty result.
			if path == engineRoot {
				return nil
			}
			if name := d.Name(); name == "testdata" || strings.HasPrefix(name, ".") {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		// The proxy image embeds the engine's own source as a string constant,
		// so every identifier in the tree appears in it and a plain scan would
		// call everything emitted.
		if strings.HasSuffix(path, "sources.gen.go") {
			return nil
		}
		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return perr
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			scanFunc(fn.Body, out)
		}
		return nil
	})
	require.NoError(t, err)
	return out
}

// scanFunc records the catalog types one function emits.
//
// Two passes, because a type does not always reach the call directly. The
// readiness loop in internal/env picks service.ready or service.exited into a
// variable first and passes the variable, which is the right way to write it
// and invisible to a scan that only reads call arguments. The first version of
// this reported both as never emitted, which is the same class of wrong answer
// as the gap it exists to find.
func scanFunc(body *ast.BlockStmt, out map[string]bool) {
	// local variable -> the catalog types ever assigned to it.
	locals := map[string][]string{}
	ast.Inspect(body, func(n ast.Node) bool {
		var lhs, rhs []ast.Expr
		switch v := n.(type) {
		case *ast.AssignStmt:
			lhs, rhs = v.Lhs, v.Rhs
		default:
			return true
		}
		for i, l := range lhs {
			name, ok := l.(*ast.Ident)
			if !ok || i >= len(rhs) {
				continue
			}
			if ty := catalogType(rhs[i]); ty != "" {
				locals[name.Name] = append(locals[name.Name], ty)
			}
		}
		return true
	})

	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !emitFuncs[sel.Sel.Name] {
			return true
		}
		for _, arg := range call.Args {
			if ty := catalogType(arg); ty != "" {
				out[ty] = true
				continue
			}
			if id, ok := arg.(*ast.Ident); ok {
				for _, ty := range locals[id.Name] {
					out[ty] = true
				}
			}
		}
		return true
	})
}

// catalogType returns the identifier in an events.X expression, or "".
func catalogType(e ast.Expr) string {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "events" {
		return ""
	}
	return sel.Sel.Name
}

// The catalog and the engine agree, or the difference is written down.
//
// Failing in either direction on purpose. A type that starts being emitted must
// come off the exemption list, because a stale exemption is how a list like
// this stops meaning anything.
func TestEveryCatalogTypeIsEitherEmittedOrExemptWithAReason(t *testing.T) {
	emitted := emittedTypes(t)
	require.NotEmpty(t, emitted, "the scan found no emit calls at all, so it is measuring nothing")

	byName := map[string]events.Type{}
	for _, ty := range events.AllTypes() {
		byName[constName(ty)] = ty
	}

	var silent, staleExemption []string
	for name, ty := range byName {
		_, exempt := unemitted[ty]
		switch {
		case emitted[name] && exempt:
			staleExemption = append(staleExemption, string(ty))
		case !emitted[name] && !exempt:
			silent = append(silent, string(ty))
		}
	}
	sort.Strings(silent)
	sort.Strings(staleExemption)

	require.Empty(t, silent,
		"these types are documented on the events reference page and nothing emits them; "+
			"emit them, or add them to unemitted with the reason")
	require.Empty(t, staleExemption,
		"these types are emitted now and are still listed as unemitted; take them off the list")
}

// The masking pane's six, named on their own, because they are what this check
// was written after and a regression here empties the dashboard again silently.
func TestTheMaskingTypesAreEmitted(t *testing.T) {
	emitted := emittedTypes(t)
	for _, name := range []string{
		"MaskPlanned", "MaskProgress", "MaskApplied",
		"MaskVerifying", "MaskVerified", "MaskFinding",
	} {
		require.True(t, emitted[name],
			"%s is on the reference page and the dashboard's database pane draws it, "+
				"and nothing emits it", name)
	}
}

// constName maps a type's string back to its Go identifier, so the source scan
// and the catalog can be compared without a second hand-written table.
func constName(t events.Type) string {
	for _, candidate := range typeIdentifiers {
		if events.Type(candidate.value) == t {
			return candidate.name
		}
	}
	return ""
}

// typeIdentifiers pairs each catalog constant with its value.
//
// Written out rather than derived, because Go erases the identifier: a constant
// is its value at run time and there is no reflection that recovers the name.
// The test above fails on any catalog type missing from here, since a type with
// no identifier matches no emit call and reads as silent.
var typeIdentifiers = []struct {
	name  string
	value string
}{
	{"EnvCreating", "env.creating"}, {"EnvReady", "env.ready"}, {"EnvFailed", "env.failed"},
	{"EnvSleeping", "env.sleeping"}, {"EnvWaking", "env.waking"},
	{"EnvDestroying", "env.destroying"}, {"EnvDestroyed", "env.destroyed"},
	{"ResourceCreated", "resource.created"}, {"ResourceDeleted", "resource.deleted"},
	{"ResourceLeaked", "resource.leaked"},
	{"GoldenRefreshing", "golden.refreshing"}, {"GoldenReady", "golden.ready"},
	{"GoldenFailed", "golden.failed"}, {"GoldenCollected", "golden.collected"},
	{"DBBranching", "db.branching"}, {"DBBranched", "db.branched"},
	{"DBReset", "db.reset"}, {"DBDestroyed", "db.destroyed"},
	{"MaskPlanned", "mask.planned"}, {"MaskProgress", "mask.progress"},
	{"MaskApplied", "mask.applied"}, {"MaskVerifying", "mask.verifying"},
	{"MaskVerified", "mask.verified"}, {"MaskFinding", "mask.finding"},
	{"BuildStarted", "build.started"}, {"BuildLog", "build.log"},
	{"BuildFinished", "build.finished"}, {"BuildFailed", "build.failed"},
	{"ServiceStarting", "service.starting"}, {"ServiceReady", "service.ready"},
	{"ServiceLog", "service.log"}, {"ServiceExited", "service.exited"},
	{"ServiceRestart", "service.restarted"}, {"CronFired", "cron.fired"},
	{"EgressDecision", "egress.decision"}, {"EgressTripwire", "egress.tripwire"},
	{"CaptureMessage", "capture.message"},
	{"WebhookQueued", "webhook.queued"}, {"WebhookDelivered", "webhook.delivered"},
	{"WebhookFailed", "webhook.failed"},
	{"AgentStarted", "agent.started"}, {"AgentStep", "agent.step"},
	{"AgentVerdict", "agent.verdict"}, {"AgentFinished", "agent.finished"},
	{"InsightFinding", "insight.finding"},
	{"LoadSample", "load.sample"}, {"LoadFinished", "load.finished"},
	{"Progress", "engine.progress"}, {"Warning", "engine.warning"},
	{"Error", "engine.error"}, {"Retry", "engine.retry"},
	{"SinkDropped", "engine.sink_dropped"},
}

// The pairing above is a second copy of the catalog, so it is checked against
// the first one. A type added to the catalog and not here would otherwise read
// as silent for the wrong reason and be exempted for the wrong reason.
func TestTheIdentifierTableCoversTheCatalog(t *testing.T) {
	known := map[string]bool{}
	for _, c := range typeIdentifiers {
		known[c.value] = true
	}
	var missing []string
	for _, ty := range events.AllTypes() {
		if !known[string(ty)] {
			missing = append(missing, string(ty))
		}
	}
	require.Empty(t, missing, "add these to typeIdentifiers with their Go identifier")

	catalog := map[string]bool{}
	for _, ty := range events.AllTypes() {
		catalog[string(ty)] = true
	}
	var extra []string
	for _, c := range typeIdentifiers {
		if !catalog[c.value] {
			extra = append(extra, c.value)
		}
	}
	require.Empty(t, extra, "these are listed here and are not in the catalog")
}
