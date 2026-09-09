// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package main_test

// What the licence actually turns on, measured rather than described.
//
// This file is here for the same reason main_test.go is: this is the only place
// where the enterprise edition exists as a whole. main.go imports compliance,
// secrets and policyenforce, so their init functions have run and
// feature.Sites is populated. In any one of those packages the registry holds
// one entry, and in ee/engine/feature it holds none at all, so the two
// reconciliations below can only be made from here. A version of them written
// next to the catalogue would pass on an empty map and prove nothing, which is
// the shape of failure this repository keeps finding in its own instruments.
//
// Two things are checked, and they are different questions.
//
// The first is agreement: the catalogue and the registry name the same sites,
// in both directions, so neither a feature declared and left out of the
// catalogue nor a catalogue entry claiming a declaration that was deleted can
// pass.
//
// The second is BEHAVIOUR, and it is the one the lane's number comes from. For
// every gated feature, the real entry point is called twice: once with a licence
// that grants everything except that feature, and once with a licence that
// grants it. The first must do less. "It compiles", "the constant is declared"
// and "Sites is non empty" are not verification; the refusal is.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/auditsink"
	"github.com/antifailure/antifailure/ee/engine/cloudgate"
	"github.com/antifailure/antifailure/ee/engine/compliance"
	"github.com/antifailure/antifailure/ee/engine/feature"
	"github.com/antifailure/antifailure/ee/engine/license"
	"github.com/antifailure/antifailure/ee/engine/policyenforce"
	"github.com/antifailure/antifailure/ee/engine/secrets"
	"github.com/antifailure/antifailure/engine/pkg/extension"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/secret"
)

// withEverythingExcept grants every feature the licence sells but one.
//
// Deliberately not an empty licence. An empty one proves that the code path
// needs A licence and not that it needs THIS entitlement, and the difference is
// the whole of what a per feature gate is for: a customer who bought four
// features and not the fifth has a valid, active, honoured licence.
func withEverythingExcept(missing license.Feature) context.Context {
	granted := []license.Feature{}
	for _, f := range license.AllFeatures() {
		if f != missing {
			granted = append(granted, f)
		}
	}
	return withFeatures(granted...)
}

func withFeatures(features ...license.Feature) context.Context {
	v := license.NewVerifier(nil)
	status := v.Evaluate(license.Claims{
		ID: "l", Org: "acme", Plan: "enterprise", Features: features,
		IssuedAt: time.Now().Add(-time.Hour), ExpiresAt: time.Now().AddDate(1, 0, 0),
	}, license.Evaluation{Org: "acme", Now: time.Now()})
	return feature.With(context.Background(), status)
}

func TestEveryGatedFeatureIsDeclaredWhereTheCatalogueSaysItIs(t *testing.T) {
	// WHAT THIS DOES NOT CHECK, said out loud because I advised somebody
	// wrongly about it and L7.1 corrected me from the source.
	//
	// Contains, not Equals, so a feature may register more sites than the
	// catalogue names. Only the catalogue's own EnforcedAt is ever opened and
	// verified; a second or third registered site is recorded and unchecked,
	// whatever it is spelled like. I had suggested that respelling extra sites
	// in path:symbol form would bring them under the instrument. It does not:
	// the file check iterates feature.Catalogue() and never reads Sites().
	//
	// Nor can it be fixed by simply extending the check to every registered
	// site, which is the interesting part. Where several surfaces share ONE
	// gate function, only the file holding that function contains the Enabled
	// literal, so validating the others the same way would reject correct code
	// and the only way to keep them would be to weaken the literal check to a
	// bare name match. That is the compliance_packs defect this format exists
	// to kill, so the resolution is one canonical site per feature rather than
	// a looser instrument. A fact like "there are three sinks" belongs
	// somewhere that answers "what can this forward to", not in a registry
	// whose question is "where is this gated".
	//
	// The residue is a real and accepted hole: an extra registered site is a
	// string nothing verifies. Contains stays because a feature may one day
	// have two genuinely independent gates in two files, and forbidding that
	// outright would be a guess about the future rather than a check.
	for _, e := range feature.Catalogue() {
		if e.State != feature.StateGated {
			continue
		}
		sites := feature.Sites(e.Feature)
		require.Containsf(t, sites, e.EnforcedAt,
			"the catalogue says %s is enforced at %s and the registry holds %v. Either the "+
				"Declare call is missing from the enterprise binary's imports, or the two "+
				"strings have drifted.",
			e.Feature, e.EnforcedAt, sites)
	}
}

func TestTheAuditSiteIsTheOneAuditsinkDeclares(t *testing.T) {
	// The import that would have made this impossible to get wrong is a cycle:
	// auditsink asks feature for the licence, so feature cannot import
	// auditsink to reuse its constant. This binary links both, so it is the
	// only place the two strings can be compared at all.
	//
	// Not a duplicate of the site checks below. Those prove the catalogue's
	// string names a real file and a real symbol that really asks about this
	// feature; a string can satisfy every one of them and still not be the
	// string the enforcing package publishes as its own site.
	entry, ok := feature.Of(license.FeatureAuditStream)
	require.True(t, ok, "audit_stream has no catalogue entry")
	require.Equal(t, auditsink.AuditStreamSite, entry.EnforcedAt,
		"the catalogue names a different audit site from the one auditsink declares. The "+
			"constant exists so these cannot drift and the import that would enforce it is a "+
			"cycle, so this assertion is what stands in for it.")
}

func TestEveryDeclaredSiteBelongsToAGatedCatalogueEntry(t *testing.T) {
	// The reverse, and the one that catches the next lane rather than the last.
	// A feature gated in code and absent from the catalogue is a feature the
	// licensing page does not know is enforced, so a customer reading the page
	// is told they get something a licence withholds. This fails until the
	// catalogue is updated, which is the intended cost of adding a gate.
	for _, f := range license.AllFeatures() {
		sites := feature.Sites(f)
		if len(sites) == 0 {
			continue
		}
		entry, ok := feature.Of(f)
		require.Truef(t, ok,
			"%s is declared at %v and has no catalogue entry", f, sites)
		require.Containsf(t,
			[]feature.State{feature.StateGated, feature.StateEditionGated}, entry.State,
			"%s is declared at %v and the catalogue calls it %q. A site that refuses IS a "+
				"gate, so either the state is stale or the Declare is. The two accepted "+
				"states are the two kinds of ENGINE side refusal: this module asking "+
				"feature.Enabled, and the community engine asking edition.Permits with the "+
				"licence crossing the boundary as strings.",
			f, sites, entry.State)
		require.Containsf(t, sites, entry.EnforcedAt,
			"%s is declared at %v and the catalogue names %s", f, sites, entry.EnforcedAt)
	}
}

// TestEveryRegisteredSiteNamesAFileThatChecksThatFeature verifies EVERY site in
// the registry, not only the one the catalogue names.
//
// Contributed by L7.1, and it is a better answer than the two I was choosing
// between. The catalogue check opens e.EnforcedAt alone, so a second or third
// registered site was a string nothing verified, and I had wrongly suggested
// that respelling such a site in path:symbol form would bring it under the
// instrument. It would not: nothing read it.
//
// The obvious fix, requiring the registry to hold EXACTLY the catalogue's
// string, is the wrong one, because a feature may one day have two genuinely
// independent gates in two files and forbidding that is a guess about the
// future. This is the third option and it forbids nothing real: two independent
// gates both pass, because each file really does contain its own check. The
// only thing rejected is a site naming a file that does not check this feature,
// which is precisely the unverifiable string, rejected by the same rule as
// everything else rather than by a count.
//
// WHICH check is required is read from the catalogue's state and never from the
// path, and the two are not interchangeable: a gated site must hold a
// feature.Enabled call for this feature and resolves under ee/engine, while an
// edition gated site must hold an edition.Permits call for the constant whose
// VALUE is this feature's wire name and resolves under the repository root.
// Sniffing the path instead would accept an ee/engine file for an edition gated
// row and prove only that some licence check is in it.
//
// It rejects nothing today. All five current Declares name a file holding their
// own check, including compliance, whose Declare sits in pack.go and names
// command.go, and multi_runtime, whose Declare sits in this module and names a
// file in the community engine. A site does not have to name the file it is
// declared in; what is verified is the file it NAMES.
//
// It lives here rather than beside the catalogue for the reason everything else
// in this file does: in ee/engine/feature the registry is empty, so the loop
// would run zero times and report success about a check it never made.
func TestEveryRegisteredSiteNamesAFileThatChecksThatFeature(t *testing.T) {
	root := eeEngineRoot(t)
	constants := featureConstants(t, root)

	checked := 0
	for _, f := range license.AllFeatures() {
		for _, site := range feature.Sites(f) {
			file, symbol, ok := feature.SplitSite(site)
			require.Truef(t, ok,
				"%s is declared at %q, which is not path:symbol", f, site)

			// Which root the site resolves against, and which call has to be in
			// it, are decided by the catalogue's STATE rather than by the shape
			// of the path. A check that sniffed the path would accept an
			// ee/engine file for an edition gated entry and never notice that
			// the mechanism it claims is not the mechanism it has.
			entry, ok := feature.Of(f)
			require.Truef(t, ok, "%s is declared at %s and has no catalogue entry", f, site)
			base := root
			if entry.State == feature.StateEditionGated {
				base = repoRoot(t)
			}

			source, err := os.ReadFile(filepath.Join(base, file))
			require.NoErrorf(t, err,
				"%s is declared at %s and that file cannot be read", f, site)

			require.Regexpf(t,
				regexp.MustCompile(`func (\([^)]*\) )?`+regexp.QuoteMeta(symbol)+`\(`),
				string(source),
				"%s is declared at %s and %s does not DEFINE %s", f, site, file, symbol)

			if entry.State == feature.StateEditionGated {
				ec := editionConstants(t, repoRoot(t))[f]
				require.NotEmptyf(t, ec, "no constant in edition.go has the value %q", f)
				require.Containsf(t, string(source),
					"edition.Permits(ctx, edition."+ec+")",
					"%s is edition gated at %s and %s contains no "+
						"edition.Permits(ctx, edition.%s) call, so that site is a string "+
						"nothing can verify",
					f, site, file, ec)
				checked++
				continue
			}

			constant := constants[f]
			require.NotEmptyf(t, constant, "no constant in license.go has the value %q", f)
			require.Containsf(t, string(source),
				"feature.Enabled(ctx, license."+constant+")",
				"%s is declared at %s and %s contains no feature.Enabled(ctx, license.%s) "+
					"call, so that site is a string nothing can verify",
				f, site, file, constant)
			checked++
		}
	}

	require.NotZerof(t, checked,
		"no enforcement site is registered at all, so this checked nothing. Either every "+
			"Declare has been removed or this binary no longer imports the packages holding them")
	t.Logf("registered enforcement sites verified: %d", checked)
}

// eeEngineRoot is the ee/engine directory, from this test's own source path.
func eeEngineRoot(t *testing.T) string {
	t.Helper()
	_, self, _, ok := runtime.Caller(0)
	require.True(t, ok, "the test cannot locate its own source")
	return filepath.Dir(filepath.Dir(filepath.Dir(self)))
}

// featureConstants maps each wire name to its Go constant, read from license.go.
//
// A second READER of one source of truth rather than a second list. The
// catalogue's own check parses the same file for the same mapping; two readers
// of one file cannot disagree about it, and a hardcoded table here could.
func featureConstants(t *testing.T, root string) map[license.Feature]string {
	t.Helper()
	source, err := os.ReadFile(filepath.Join(root, "license", "license.go"))
	require.NoError(t, err)
	out := map[license.Feature]string{}
	for _, m := range regexp.MustCompile(`(Feature\w+)\s+Feature\s+=\s+"([a-z_]+)"`).
		FindAllStringSubmatch(string(source), -1) {
		out[license.Feature(m[2])] = m[1]
	}
	require.NotEmpty(t, out, "license.go declares no feature constants, so the parse is wrong")
	return out
}

// repoRoot is the tree above ee/, for the sites that are not in this module.
func repoRoot(t *testing.T) string {
	t.Helper()
	return filepath.Dir(filepath.Dir(eeEngineRoot(t)))
}

// editionConstants is featureConstants for the COMMUNITY engine's own list.
//
// A second reader of a second source of truth, and the two lists are separate
// on purpose: engine/pkg/edition cannot import ee/engine/license, which is the
// whole reason the licence crosses the boundary as strings. So the wire name is
// the only thing they share, and this is what checks that the string an
// edition gated site tests is the one this feature actually is.
func editionConstants(t *testing.T, root string) map[license.Feature]string {
	t.Helper()
	source, err := os.ReadFile(filepath.Join(root, "engine", "pkg", "edition", "edition.go"))
	require.NoError(t, err)
	out := map[license.Feature]string{}
	for _, m := range regexp.MustCompile(`(Feature\w+)\s+=\s+"([a-z_]+)"`).
		FindAllStringSubmatch(string(source), -1) {
		out[license.Feature(m[2])] = m[1]
	}
	require.NotEmpty(t, out, "edition.go declares no feature constants, so the parse is wrong")
	return out
}

func TestNothingIsDeclaredForAFeatureNoLicenceCanCarry(t *testing.T) {
	// A call site for a feature no licence grants is dead code that looks like
	// enforcement, which is the mirror image of a feature nobody checks. The
	// registry's own doc comment names both and this is the second one.
	sellable := map[license.Feature]bool{}
	for _, f := range license.AllFeatures() {
		sellable[f] = true
	}
	for _, f := range feature.Declared() {
		require.Truef(t, sellable[f],
			"%s is declared as an enforcement site and no licence can grant it, so that "+
				"check can never pass", f)
	}
}

// ---------------------------------------------------------------------------
// The measurement
// ---------------------------------------------------------------------------

// proof is one feature's entry point and what to look for when it runs.
type proof struct {
	feature license.Feature
	// run exercises the real entry point under the given context and reports
	// whether THE LICENSED BEHAVIOUR HAPPENED, plus one line saying what it saw.
	//
	// "The licensed behaviour happened" rather than "it refused", and the
	// difference is not cosmetic: it is the bug this harness was written with.
	// For a secret source the licensed behaviour is a store that answers, and
	// for the compliance command it is a report. For the policy hook it is the
	// REFUSAL: an unlicensed hook permits, because a policy nobody paid for
	// must not start refusing environments. Written as "did it refuse", the
	// policy row failed against correct code and the other two passed, which is
	// a harness reporting the wrong answer twice over.
	run func(t *testing.T, ctx context.Context) (licensed bool, what string)
}

// entryPoints is the real call for each feature that has one.
//
// One per gated feature and no more. A feature missing from this list is not
// silently skipped: TestTheEntitlementIsWhatDecides requires the list and the
// catalogue's gated set to be the same set, so adding a gate without adding a
// proof of it fails.
func entryPoints() []proof {
	return []proof{
		{
			feature: license.FeaturePolicy,
			run: func(t *testing.T, ctx context.Context) (bool, string) {
				t.Helper()
				// A policy that refuses this request outright, so the only
				// variable between the two calls is the entitlement.
				hook := policyenforce.NewHook(policyenforce.Policy{
					DeniedHosts: []string{"api.stripe.com"},
				}, nil)
				err := hook.Check(ctx, extension.EnvironmentRequest{
					Org: "acme", Repository: "acme/app", Branch: "main", EnvID: "af-1",
					EgressHosts: []string{"api.stripe.com"},
					EgressModes: map[string]string{"api.stripe.com": "sandbox"},
				})
				// The licensed behaviour is the refusal. A hook whose licence
				// has lapsed permits, which is what the gate inside Check is
				// for: an expired customer must not keep a feature they cannot
				// turn off without a restart, and must not have their
				// environments refused by one either.
				if err == nil {
					return false, "Hook.Check permitted the environment"
				}
				return true, "Hook.Check refused: " + firstLine(err.Error())
			},
		},
		{
			feature: license.FeatureSecrets,
			run: func(t *testing.T, ctx context.Context) (bool, string) {
				t.Helper()
				source := secrets.New(reachableBackend{})
				ok, why := source.Available(ctx)
				if ok {
					return true, "Source.Available reported the store usable"
				}
				return false, "Source.Available refused: " + firstLine(why)
			},
		},
		{
			feature: license.FeatureAuditStream,
			run: func(t *testing.T, ctx context.Context) (bool, string) {
				t.Helper()
				// DELIVERY, not an opinion about delivery. auditsink exports
				// Unlicensed(), which would have been one line here and would
				// have proved only that the package agrees with itself. What a
				// customer buys is that the entry ARRIVES, so the proof is a
				// real sink posting to a real listener and the observable is
				// whether anything showed up.
				var got int32
				// TLS, because the sink refuses a plaintext URL outright:
				// the body IS the audit record, and posting it over http is
				// the thing the record exists to prove is not happening. The
				// test server's own client trusts its certificate.
				srv := httptest.NewTLSServer(http.HandlerFunc(
					func(w http.ResponseWriter, r *http.Request) {
						atomic.AddInt32(&got, 1)
						w.WriteHeader(http.StatusOK)
					}))
				defer srv.Close()

				sink, err := auditsink.NewWebhook(auditsink.WebhookConfig{
					URL: srv.URL,
					// Required, and required for a reason worth keeping: an
					// entry that cannot be delivered and leaves nothing behind
					// is an audit stream with an invisible hole in it.
					DeadLetterFile: filepath.Join(t.TempDir(), "dead.jsonl"),
					Client:         srv.Client(),
				})
				require.NoError(t, err, "the webhook sink could not be built")

				err = sink.Write(ctx, extension.AuditEntry{
					Action:     "environment.created",
					OccurredAt: time.Now(),
				})
				// Not an error either way: an unlicensed sink accepts the entry
				// and forwards nothing, which auditsink argues for deliberately
				// so an expired licence does not read as a broken deployment.
				require.NoError(t, err, "the sink returned an error rather than declining")

				if atomic.LoadInt32(&got) > 0 {
					return true, "the webhook sink delivered the entry"
				}
				return false, "the webhook sink accepted the entry and forwarded nothing"
			},
		},
		{
			feature: license.FeatureCloudDatabase,
			run: func(t *testing.T, ctx context.Context) (bool, string) {
				t.Helper()
				// The wrapper is what enforces, so the proof goes through it
				// rather than around it. cloudgate.Wrap REPLACES a registration,
				// and a provider registered after the wrap is not covered, which
				// that package says about itself; registering first and wrapping
				// after is therefore the arrangement the binary uses and the one
				// worth exercising.
				db := openedCloudDatabase(t, ctx)
				_, err := db.Branch(ctx, "gv_1", "env-1")
				if err == nil {
					return true, "the provider branched the database"
				}
				var refusal *cloudgate.Refusal
				require.ErrorAsf(t, err, &refusal,
					"Branch failed with something that is not a licensing refusal: %v. A "+
						"provider that is merely down would look like this and must not be "+
						"counted as a gate.", err)
				require.Equal(t, license.FeatureCloudDatabase, refusal.Feature,
					"the refusal names the wrong feature, so one entitlement is answering "+
						"for another")
				return false, "the gate refused Branch: " + refusal.Error()
			},
		},
		{
			feature: license.FeatureCloudRuntime,
			run: func(t *testing.T, ctx context.Context) (bool, string) {
				t.Helper()
				rt := openedCloudRuntime(t, ctx)
				_, err := rt.Up(ctx, provider.EnvSpec{EnvID: "env-1"})
				if err == nil {
					return true, "the provider brought the environment up"
				}
				var refusal *cloudgate.Refusal
				require.ErrorAsf(t, err, &refusal,
					"Up failed with something that is not a licensing refusal: %v", err)
				require.Equal(t, license.FeatureCloudRuntime, refusal.Feature,
					"the refusal names the wrong feature, so one entitlement is answering "+
						"for another")
				return false, "the gate refused Up: " + refusal.Error()
			},
		},
		{
			feature: license.FeatureCompliance,
			run: func(t *testing.T, ctx context.Context) (bool, string) {
				t.Helper()
				var out, errs strings.Builder
				code := compliance.Command(ctx, []string{"soc2", "--org", "acme"},
					compliance.Options{
						Stdout: &out, Stderr: &errs,
						Gather: func(context.Context, string, time.Time, time.Time) (
							compliance.Evidence, error) {
							return compliance.Evidence{
								Org: "acme", From: time.Now().AddDate(-1, 0, 0), To: time.Now(),
							}, nil
						},
					})
				if code == 0 {
					require.NotEmpty(t, out.String(),
						"af compliance exited 0 and wrote no report")
					return true, "af compliance produced a report"
				}
				require.Empty(t, out.String(),
					"a refusal wrote a partial document to standard output")
				return false, "af compliance exited " + itoa(code) + ": " + firstLine(errs.String())
			},
		},
	}
}

func TestTheEntitlementIsWhatDecides(t *testing.T) {
	// The number, measured. For each gated feature: with everything else
	// granted and this one missing, the entry point must do less; with it
	// granted, it must do the thing.
	//
	// Both halves matter and the second is the one an over eager gate breaks. A
	// check written the wrong way round refuses under a valid licence, and a
	// test that only looked at the refusal would call that a pass.
	proofs := entryPoints()

	haveProof := map[license.Feature]bool{}
	for _, p := range proofs {
		haveProof[p.feature] = true
	}
	for _, f := range feature.GatedFeatures() {
		require.Truef(t, haveProof[f],
			"%s is marked gated in the catalogue and this file exercises no entry point for "+
				"it, so the claim rests on a string rather than on a refusal", f)
	}
	for _, p := range proofs {
		entry, ok := feature.Of(p.feature)
		require.Truef(t, ok, "%s is exercised here and is not in the catalogue", p.feature)
		require.Equalf(t, feature.StateGated, entry.State,
			"%s is exercised as a gate and the catalogue does not call it gated", p.feature)
	}

	decided := 0
	for _, p := range proofs {
		p := p
		passed := t.Run(string(p.feature), func(t *testing.T) {
			without, sawWithout := p.run(t, withEverythingExcept(p.feature))
			require.Falsef(t, without,
				"with a valid licence granting every feature except %s, the licensed behaviour "+
					"still happened: %s. The entitlement is not what decides.",
				p.feature, sawWithout)

			with, sawWith := p.run(t, withFeatures(license.AllFeatures()...))
			require.Truef(t, with,
				"with %s granted the licensed behaviour did not happen: %s. A gate that "+
					"refuses a customer who paid is worse than no gate, and a test that only "+
					"looked at the refusal would call that a pass.",
				p.feature, sawWith)

			t.Logf("%s: without it, %s. With it, %s.", p.feature, sawWithout, sawWith)
		})
		if passed {
			decided++
		}
	}

	t.Logf("licensed features whose entitlement decides the behaviour: %d of %d",
		decided, len(license.AllFeatures()))
}

func TestTheFeaturesThatRefuseNothingAreTheOnesTheCatalogueNames(t *testing.T) {
	// The unflattering half, asserted rather than left to a reader.
	//
	// Nine of twelve features change nothing when they are absent. That is the
	// finding, and pinning it here means the day one of them gains a real gate,
	// this fails until somebody moves it in the catalogue, and the day a tenth
	// quietly loses one, this fails too.
	for _, e := range feature.Catalogue() {
		if e.State == feature.StateGated || e.State == feature.StateEditionGated {
			continue
		}
		require.Emptyf(t, feature.Sites(e.Feature),
			"the catalogue calls %s %q and something declared an enforcement site for it: %v",
			e.Feature, e.State, feature.Sites(e.Feature))
	}
}

// reachableBackend is a secret store that works, so the only thing that can
// refuse a lookup is the licence.
type reachableBackend struct{}

func (reachableBackend) Describe() string { return "a test store at memory://entitlements" }

func (reachableBackend) Reach(context.Context) error { return nil }

func (reachableBackend) Fetch(context.Context, string) (string, bool, error) {
	return "value", true, nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}

// ---------------------------------------------------------------------------
// The cloud provider proofs need a provider to gate, and a real one would need
// a cloud account. These are the smallest thing cloudgate can wrap.
//
// Deliberately NOT shared with ee/engine/cloudgate's own fakes, which are
// unexported and belong to that package's tests. A test that reached into
// another package's fixtures would couple the proof to the shape of the code it
// is meant to be independent evidence about.

type gateFakeDatabase struct{}

func (gateFakeDatabase) Name() string                { return "aurora" }
func (gateFakeDatabase) Capabilities() provider.Caps { return provider.Caps{Branching: true} }

func (gateFakeDatabase) RefreshGolden(
	context.Context, provider.GoldenSpec,
) (provider.GoldenVersion, error) {
	return provider.GoldenVersion{ID: "gv_1", Verified: true}, nil
}

func (gateFakeDatabase) ListGoldens(context.Context) ([]provider.GoldenVersion, error) {
	return []provider.GoldenVersion{{ID: "gv_1", Verified: true}}, nil
}

func (gateFakeDatabase) DestroyGolden(context.Context, string) error { return nil }

func (gateFakeDatabase) Branch(_ context.Context, version, envID string) (provider.Branch, error) {
	return provider.Branch{EnvID: envID, From: version}, nil
}

func (gateFakeDatabase) Reset(context.Context, provider.Branch) error   { return nil }
func (gateFakeDatabase) Destroy(context.Context, provider.Branch) error { return nil }

func (gateFakeDatabase) ConnString(
	context.Context, provider.Branch, provider.ConnMode,
) (secret.Value, error) {
	return secret.New("postgres://example"), nil
}

func (gateFakeDatabase) Inventory(context.Context) ([]provider.Resource, error) {
	return []provider.Resource{{ID: "cluster-1"}}, nil
}

func (gateFakeDatabase) Health(context.Context, provider.Branch) (provider.Health, error) {
	return provider.Health{}, nil
}

func (gateFakeDatabase) Close() error { return nil }

type gateFakeDatabaseProvider struct{}

func (gateFakeDatabaseProvider) Name() string { return "aurora" }

func (gateFakeDatabaseProvider) Open(
	context.Context, extension.DatabaseConfig,
) (provider.Database, error) {
	return gateFakeDatabase{}, nil
}

type gateFakeRuntime struct{}

func (gateFakeRuntime) Name() string { return "ecs" }

func (gateFakeRuntime) Capabilities() provider.RuntimeCaps {
	return provider.RuntimeCaps{Logs: true}
}

func (gateFakeRuntime) Up(context.Context, provider.EnvSpec) (provider.Env, error) {
	return provider.Env{EnvID: "env-1"}, nil
}

func (gateFakeRuntime) Down(context.Context, string) (provider.Teardown, error) {
	return provider.Teardown{}, nil
}

func (gateFakeRuntime) Status(context.Context, string) (provider.Env, error) {
	return provider.Env{EnvID: "env-1"}, nil
}

func (gateFakeRuntime) Inventory(context.Context) ([]provider.Resource, error) {
	return []provider.Resource{{ID: "service-1"}}, nil
}

func (gateFakeRuntime) Close() error { return nil }

type gateFakeRuntimeProvider struct{}

func (gateFakeRuntimeProvider) Name() string { return "ecs" }

func (gateFakeRuntimeProvider) Open(
	context.Context, extension.RuntimeConfig,
) (provider.Runtime, error) {
	return gateFakeRuntime{}, nil
}

// openedCloudDatabase is a gated database, opened through the wrapper.
//
// Wrap is asserted to have wrapped exactly one, because a Wrap that found
// nothing returns zero and every assertion afterwards would then be made about
// an UNGATED provider, which permits everything and would read as "the licence
// decided" in the granted direction and as a broken gate in the other.
func openedCloudDatabase(t *testing.T, ctx context.Context) provider.Database {
	t.Helper()
	reg := extension.NewRegistry()
	reg.AddDatabaseProvider(gateFakeDatabaseProvider{})
	require.Equal(t, 1, cloudgate.Wrap(reg),
		"cloudgate wrapped no provider, so what follows would be testing an ungated one")
	p, ok := reg.DatabaseProviderNamed("aurora")
	require.True(t, ok, "wrapping removed the provider instead of replacing it")
	db, err := p.Open(ctx, extension.DatabaseConfig{})
	require.NoError(t, err)
	require.NotNil(t, db)
	return db
}

// openedCloudRuntime is openedCloudDatabase for the runtime half.
func openedCloudRuntime(t *testing.T, ctx context.Context) provider.Runtime {
	t.Helper()
	reg := extension.NewRegistry()
	reg.AddRuntimeProvider(gateFakeRuntimeProvider{})
	require.Equal(t, 1, cloudgate.Wrap(reg),
		"cloudgate wrapped no provider, so what follows would be testing an ungated one")
	p, ok := reg.RuntimeProviderNamed("ecs")
	require.True(t, ok, "wrapping removed the provider instead of replacing it")
	rt, err := p.Open(ctx, extension.RuntimeConfig{})
	require.NoError(t, err)
	require.NotNil(t, rt)
	return rt
}
