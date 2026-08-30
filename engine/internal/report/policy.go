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
	return p
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
