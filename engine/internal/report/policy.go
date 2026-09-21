package report

import (
	"fmt"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// Level is what one finding does to the check.
//
// Three levels rather than two. Before this the run had nowhere to put a real
// finding that should not stop a merge: everything the environment noticed
// either failed the build or was printed and forgotten, and a check that can
// only say fail or nothing is a check people learn to route around.
type Level string

const (
	// LevelIgnore drops the finding. It is neither printed nor counted.
	LevelIgnore Level = "ignore"
	// LevelWarn reports the finding and leaves the check passing.
	LevelWarn Level = "warn"
	// LevelFail reports the finding and fails the check.
	LevelFail Level = "fail"
)

// The defaults, which MIRROR engine/internal/manifest's. They are stated twice
// for the same reason the insights thresholds are: the manifest package fills
// in a manifest, this one fills in a policy that reached it some other way,
// and a test asserts the two agree.
const (
	DefaultLockWarnMS = 500.0
	DefaultLockFailMS = 2000.0
)

// The security exit codes, exported so a security family and the gate that
// turns its findings into a process exit agree on the same two numbers rather
// than each spelling them out. They mirror the catalog: 6 is a policy denial,
// a change refused on policy or configuration grounds, and 7 is a verification
// failure, a vulnerability the run proved by exercising the application. 8 is
// the test failure code, exported beside them so a family that ever needs it
// does not reintroduce a bare literal.
const (
	// ExitPolicyDenial is the exit code for a change refused on policy grounds.
	ExitPolicyDenial = 6
	// ExitVerification is the exit code for a proven vulnerability.
	ExitVerification = 7
	// ExitTestFailure is the exit code for a failed test.
	ExitTestFailure = 8
)

// PolicyKey is one security policy key, "security.<family>.<rule>", which is
// also the Rule a security finding carries. Its own type rather than a bare
// string so a caller cannot pass an ordinary finding rule where a security key
// belongs, and so the security namespace reads as one thing across the engine.
type PolicyKey string

// Policy is the manifest's policy block with every default resolved.
//
// It exists so that "does this finding stop the merge" is answered in one
// place. Every field is read by the functions that build findings, so a key
// added here without a reader is a key that does nothing, which is the shape
// this repository keeps shipping by accident.
type Policy struct {
	LockWarnMS       float64
	LockFailMS       float64
	MigrationFailed  Level
	MigrationRewrite Level
	MigrationLint    Level
	PlanRegression   Level
	QueryRegression  Level
	LoadRegression   Level
	EgressSurprise   Level
	Masking          Level
	Cleanup          Level
	// WorkflowsUnverified is a whole run in which no workflow reached a
	// verdict about the application. Not a per workflow level: one blocked
	// workflow stays uncounted, because a gap in our tooling must not read as
	// a broken application. All of them blocked is a different claim, and
	// exiting zero on it says "tested, fine" about a run that tested nothing.
	WorkflowsUnverified Level
	// Review is what a static code reviewer finding does to the check. It
	// defaults to warn, not fail, because the reviewer is an LLM reading a diff
	// and its findings are probabilistic: a warn advises without blocking a
	// merge on a model's say-so, and a project that trusts the reviewer can
	// raise it. Every review finding, whatever its category, carries this one
	// level, the same way every load finding carries LoadRegression.
	Review Level

	// ChaosFailure is what a fault's recovery being wrong does to the check: a
	// lost acknowledged commit, a phantom row, a replay that stopped short of
	// what the client saw flushed, a heap and an index that no longer agree.
	// Every one of those findings carries this one level, the way every load
	// finding carries LoadRegression.
	ChaosFailure Level
	// ChaosUnverified is what a fault run that could not establish its claim
	// does: nothing crashed, the log carries no replay, the control file could
	// not be read, amcheck is not installed. It is a separate level because
	// a finding that says "this is wrong" and one that says "I could not look"
	// have to be able to carry different weight, and collapsing them is how a
	// project learns to ignore both.
	ChaosUnverified Level

	// Security is the resolved level for each security policy key, keyed by the
	// full "security.<family>.<rule>" name.
	//
	// A map rather than a field per key, because the security families fan out
	// in parallel and three of them adding a struct field would collide on one
	// struct the way two lanes editing one file do. The map is filled by
	// overlaying the manifest's overrides onto the family defaults: the
	// security router resolves the family defaults from the registry (which the
	// families own) and Configure fills in the manifest's overrides here, so a
	// key set in the manifest wins and a key left unset keeps its family
	// default. It is nil until a security key is configured or a family default
	// is resolved into it.
	Security map[PolicyKey]Level
}

// Configure resolves the manifest block. A nil block is the default, which is
// the gate the product has always described: an unknown destination, a failed
// teardown, unmasked data and a migration that will not apply stop the merge,
// and everything else is reported.
func Configure(in *schema.Policy) Policy {
	p := Policy{
		LockWarnMS: DefaultLockWarnMS, LockFailMS: DefaultLockFailMS,
		MigrationFailed:  LevelFail,
		MigrationRewrite: LevelWarn,
		MigrationLint:    LevelWarn,
		PlanRegression:   LevelWarn,
		QueryRegression:  LevelWarn,
		LoadRegression:   LevelWarn,
		EgressSurprise:   LevelFail,
		Masking:          LevelFail,
		Cleanup:          LevelFail,
		// Fail by default, because the default has to be the one that does not
		// lie. A project that genuinely has no workflows yet can set this to
		// warn and have that choice recorded in its manifest, which is better
		// than the silent pass it used to get for free.
		WorkflowsUnverified: LevelFail,
		// Advisory by default: an LLM review must not fail a build on its own
		// account, so a project raises this deliberately when it trusts it.
		Review: LevelWarn,
		// Fail by default, and it is the only key besides the four above that
		// is. A commit that returned success and is not there after recovery is
		// not a matter of a project's appetite for risk, so the default is not
		// the advisory one every other new key gets.
		ChaosFailure: LevelFail,
		// Warn by default. Not being able to establish a recovery is a real
		// fact that somebody has to see, and it is not evidence that the change
		// under rehearsal broke anything.
		ChaosUnverified: LevelWarn,
	}
	if in == nil {
		return p
	}
	if in.MigrationLock != nil {
		if in.MigrationLock.WarnMS > 0 {
			p.LockWarnMS = in.MigrationLock.WarnMS
		}
		if in.MigrationLock.FailMS > 0 {
			p.LockFailMS = in.MigrationLock.FailMS
		}
	}
	set := func(dst *Level, in schema.PolicyLevel) {
		if l, ok := levelOf(in); ok {
			*dst = l
		}
	}
	set(&p.MigrationFailed, in.MigrationFailed)
	set(&p.MigrationRewrite, in.MigrationRewrite)
	set(&p.MigrationLint, in.MigrationLint)
	set(&p.PlanRegression, in.PlanRegression)
	set(&p.QueryRegression, in.QueryRegression)
	set(&p.LoadRegression, in.LoadRegression)
	set(&p.EgressSurprise, in.EgressSurprise)
	set(&p.Masking, in.Masking)
	set(&p.Cleanup, in.Cleanup)
	set(&p.WorkflowsUnverified, in.WorkflowsUnverified)
	set(&p.Review, in.Review)
	set(&p.ChaosFailure, in.ChaosFailure)
	set(&p.ChaosUnverified, in.ChaosUnverified)

	// The security overrides. Each is a "security.<family>.<rule>" key mapped to
	// a level. A value the manifest validator already refused never reaches
	// here, and levelOf refuses one again rather than coercing it, so a key
	// whose level will not parse is dropped rather than silently turned into a
	// warning. The key itself is NOT validated against the family registry
	// here: a family declares its keys when it lands, and until then a manifest
	// may name a security key ahead of the family that will read it, so the key
	// is carried and the level is what is checked. A key with no family and no
	// override is owned by nobody and Level reports it as ignore.
	for key, lvl := range in.Security {
		if resolved, ok := levelOf(lvl); ok {
			if p.Security == nil {
				p.Security = map[PolicyKey]Level{}
			}
			p.Security[PolicyKey(key)] = resolved
		}
	}
	return p
}

// Level is the level a security finding on this key carries.
//
// It returns the resolved value from the Security map when the key is there,
// which is the manifest's override if it set one and otherwise the family
// default the router overlaid. A key that is in neither, because no family owns
// it and the manifest did not name it, returns LevelIgnore rather than the
// empty string: an unowned key does nothing, which is a real level and not an
// absent one, so a caller comparing against a level never meets "".
func (p Policy) Level(key PolicyKey) Level {
	if lvl, ok := p.Security[key]; ok {
		return lvl
	}
	return LevelIgnore
}

// levelOf maps a manifest level onto a report level.
//
// An unrecognised value is refused rather than coerced, and the manifest
// validator refuses it first. Coercing would mean a manifest that says
// "block" quietly warns, and the first anybody heard of it would be a merge
// that should not have happened.
func levelOf(in schema.PolicyLevel) (Level, bool) {
	switch in {
	case schema.PolicyIgnore:
		return LevelIgnore, true
	case schema.PolicyWarn:
		return LevelWarn, true
	case schema.PolicyFail:
		return LevelFail, true
	default:
		return "", false
	}
}

// LockLevel is what a lock held this long does to the check.
//
// Compared against a sampled lower bound, so a lock that breaches really was
// held at least that long. The comparison is inclusive at both ends because
// the sample interval already rounds down.
func (p Policy) LockLevel(heldMS float64) Level {
	switch {
	case p.LockFailMS > 0 && heldMS >= p.LockFailMS:
		return LevelFail
	case p.LockWarnMS > 0 && heldMS >= p.LockWarnMS:
		return LevelWarn
	default:
		return LevelIgnore
	}
}

// Finding is one thing the run noticed about the change, with the level the
// policy gave it.
//
// Rule is the stable identifier and it deliberately carries no error code. A
// finding is not an error: it is evidence, and the thing a person greps for
// six months later is the rule name, which is also the manifest key that
// decides what it does.
type Finding struct {
	Rule   string
	Level  Level
	Title  string
	Detail string
	// Fix is what to write instead, when there is something to write.
	Fix string
	// Count is how many things this finding covers: hosts, resources,
	// thresholds. It is on the finding rather than recomputed from Where,
	// because the error code that names the exit needs it and splitting a
	// display string to get a number back is how a count goes wrong.
	Count int
	// Where locates it: a table, a host, a migration file.
	Where string
}

// findingRank orders findings for display. Failures first, because somebody
// scrolling to find the failure is the same as somebody not seeing it.
func findingRank(l Level) int {
	switch l {
	case LevelFail:
		return 0
	case LevelWarn:
		return 1
	default:
		return 2
	}
}

// Counts returns how many findings sit at each level.
func (r Run) Counts() (fail, warn int) {
	for _, f := range r.Findings {
		switch f.Level {
		case LevelFail:
			fail++
		case LevelWarn:
			warn++
		}
	}
	return fail, warn
}

// Worst returns the highest ranked finding, or false when there is none worth
// reporting. It is what names the exit code.
func (r Run) Worst() (Finding, bool) {
	best, found := Finding{}, false
	for _, f := range r.Findings {
		if f.Level == LevelIgnore {
			continue
		}
		if !found || findingRank(f.Level) < findingRank(best.Level) {
			best, found = f, true
		}
	}
	return best, found
}

// duration prints milliseconds the way somebody reads them: a lock held for
// 94000ms is one held for a minute and a half, and the second form is the one
// that makes somebody stop.
func duration(ms float64) string {
	switch {
	case ms < 1000:
		return fmt.Sprintf("%.0fms", ms)
	case ms < 60000:
		return fmt.Sprintf("%.1fs", ms/1000)
	default:
		return fmt.Sprintf("%dm%02ds", int(ms)/60000, (int(ms)%60000)/1000)
	}
}
