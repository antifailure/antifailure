package env

import (
	"context"
	"io"

	"github.com/antifailure/antifailure/engine/internal/events"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// ServiceFieldForTest exposes serviceField to the package's external tests.
//
// The function decides which service a runtime progress line belongs to, and
// getting it wrong puts a row in the dashboard named after a word that is not
// a service. That is worth a test, and the function has no reason to be part
// of the package's real surface.
func ServiceFieldForTest(line string, names map[string]bool) []events.Field {
	return serviceField(line, names)
}

// NonEmptyForTest exposes nonEmpty to the package's external tests.
func NonEmptyForTest(kv ...string) []events.Field { return nonEmpty(kv...) }

// UntarForTest exposes untar to the package's external tests.
//
// It writes a tar into a directory and is the one place in the oracle's path
// that reads bytes from outside this process. A path that escapes the
// directory would write into the user's repository, and the escape is not
// hypothetical: git archive can carry a symlink, and a crafted commit can carry
// a name with parent segments in it. That is worth a test and the function has
// no reason to be part of the package's surface.
func UntarForTest(dir string, r io.Reader) error { return untar(dir, r) }

// ResolveBaselineForTest exposes resolveBaseline to the package's external
// tests, so the git resolution can be exercised against a real repository.
func ResolveBaselineForTest(root string, source schema.BaselineSource, ref string) (string, string, error) {
	return resolveBaseline(root, source, ref)
}

// BaselineTreeForTest exposes baselineTree to the package's external tests, so
// that the archive of a manifest in a subdirectory can be checked against a
// real repository rather than reasoned about.
func (o *Orchestrator) BaselineTreeForTest(ctx context.Context, rev string) (string, func(), error) {
	return o.baselineTree(ctx, rev)
}

// PickGoldenForTest exposes pickGolden to the package's external tests.
//
// Which golden a branch is made from is the decision this test suite most
// needs to be able to reach directly. Reaching it through Up would need a
// provider, a runtime, a journal and a lock, and the property being asserted
// is a property of one loop.
func PickGoldenForTest(goldens []provider.GoldenVersion, want string) (string, int) {
	return pickGolden(goldens, want)
}

// GoldenProvenanceForTest is the identity an orchestrator computes for its own
// project, as the digest a golden records.
//
// Exported because the property this file's tests exist for is a property of
// two orchestrators taken together: that two projects compute different
// identities, and that one project computes the same one on every branch. A
// test of the selection loop alone cannot see either, and the selection loop
// was never the part that was wrong.
func (o *Orchestrator) GoldenProvenanceForTest() (string, error) {
	return o.GoldenIdentity()
}

// RulesDigestForTest is the masking rules digest, which used to be the whole
// of golden selection.
//
// Exported so a test can state the defect as a property rather than describe
// it: two unrelated projects with no masking.yaml compute the SAME rules
// digest, which is why that value could never have been a project key.
func (o *Orchestrator) RulesDigestForTest() (string, error) {
	_, hash, err := o.rules()
	return hash, err
}

// AttestedProvenanceForTest exposes attestedProvenance, which is the check a
// machine pulling from a shared store makes before it restores anything.
func AttestedProvenanceForTest(attestation string) string {
	return attestedProvenance(attestation)
}

// RunSeedForTest exposes runSeed to the package's external tests.
//
// The seed runs inside a provider callback during a refresh, so reaching it
// through Up would need a daemon, a golden, and five minutes to assert that a
// shell command ran.
func RunSeedForTest(ctx context.Context, o *Orchestrator, seed, candidateURL string) error {
	return o.runSeed(ctx, nil, seed, secrets.New(candidateURL))
}

// SeedRulesHashForTest exposes seedRulesHash.
func SeedRulesHashForTest(seed string) string { return seedRulesHash(seed) }

// RunnerEnvironmentForTest exposes runnerEnvironment to the package's external
// tests.
//
// It is the whole answer to whether 'af model set' does anything. A key that
// resolves everywhere the engine looks and never reaches the one subprocess
// that spends it is a command that stores a key and changes nothing about a
// run, which is the dead-code shape this repository has shipped before.
func (o *Orchestrator) RunnerEnvironmentForTest(ctx context.Context) []string {
	return o.runnerEnvironment(ctx)
}

// ModelEnvForTest exposes modelEnv to the package's external tests.
//
// It decides what the egress sidecar receives for a synth rule, which is the
// second place a model key is actually spent.
func (o *Orchestrator) ModelEnvForTest(ctx context.Context) []string {
	return o.modelEnv(ctx)
}

// InvokeRunnerForTest exposes invokeRunner to the package's external tests.
//
// Exported because testing runnerEnvironment alone proves nothing: a function
// that assembles the right environment and a subprocess that receives it are
// different claims, and deleting the line that connects them left every test of
// the first one green. Reaching the real subprocess is the only way to assert
// the second.
func (o *Orchestrator) InvokeRunnerForTest(
	ctx context.Context, runnerPath, artifacts string,
) ([]byte, error) {
	return o.invokeRunner(ctx, runnerPath, jobDocument{Artifacts: artifacts})
}

// InvokeRunnerCapturingDocument runs the runner and returns the exact bytes
// that went to its standard input.
//
// The document between the engine and the runner is a wire format, and nothing
// asserted its shape until af explore turned out to have been impossible on
// every machine: the exploration path never set the workflows field, a nil
// slice marshals as null, and main.ts read null.length before it looked at the
// goals. Both halves worked and the document between them was untested.
//
// So this hands the caller the real bytes rather than the struct, because the
// defect was in what json.Marshal produced and not in what the struct held.
func (o *Orchestrator) InvokeRunnerCapturingDocument(
	ctx context.Context, runnerPath, artifacts string, goals []schema.Goal,
) ([]byte, []byte, error) {
	job := jobDocument{Artifacts: artifacts, Headless: true}
	for _, g := range goals {
		job.Goals = append(job.Goals, goalDoc{Name: g.Name, Goal: g.Goal, Seed: g.Seed})
	}
	captured, err := o.runnerDocument(job)
	if err != nil {
		return nil, nil, err
	}
	out, err := o.invokeRunner(ctx, runnerPath, job)
	return captured, out, err
}

// DescribeSkipForTest exposes the split of one verify.Scan skipped line.
func DescribeSkipForTest(line string) (string, string, string) { return describeSkip(line) }
