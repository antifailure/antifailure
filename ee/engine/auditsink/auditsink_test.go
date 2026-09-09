// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package auditsink

// The shared half: what a record looks like on the wire, what the licence gate
// does, and what an installation's variables actually build.
//
// Written as an internal test rather than an auditsink_test one because three
// injection points are unexported on purpose. The syslog dial, the webhook
// sleep and the object store key suffix are all unexported so that no
// installation can turn off TLS, turn off the backoff, or make an audit object
// key predictable, and a test that could not reach them would have to be a test
// against a real SIEM.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/feature"
	"github.com/antifailure/antifailure/ee/engine/license"
	"github.com/antifailure/antifailure/engine/pkg/extension"
)

// occurred is the instant every entry in these tests claims to have happened.
//
// Fixed, and deliberately not the instant a sink writes it, because the whole
// point of carrying two timestamps is that they differ.
var occurred = time.Date(2026, 9, 7, 11, 22, 33, 456789000, time.UTC)

// forwarded is when a sink is pretending to write. A minute later than the
// action, which is what a retrying network looks like from the receiving end.
var forwarded = occurred.Add(time.Minute)

func at(t time.Time) func() time.Time { return func() time.Time { return t } }

// entry is one representative audit entry: the golden publish, because it is
// the one that carries the most and the one a security team asks about.
func entry() extension.AuditEntry {
	return extension.AuditEntry{
		Action:     "golden.published",
		TargetType: "golden",
		TargetID:   "gv_9f2c",
		Origin:     "engine",
		Org:        "acme",
		Actor:      "dana@acme.example",
		OccurredAt: occurred,
		Detail: map[string]any{
			"repository": "acme/shop",
			"store":      "the bucket s3://acme-goldens/audit",
		},
	}
}

// licensed is a context whose licence grants audit_stream.
func licensed(t *testing.T) context.Context {
	t.Helper()
	v := license.NewVerifier(nil)
	status := v.Evaluate(license.Claims{
		ID: "l", Org: "acme", Features: []license.Feature{license.FeatureAuditStream},
		ExpiresAt: time.Now().AddDate(1, 0, 0),
	}, license.Evaluation{Org: "acme", Now: time.Now()})
	return feature.With(context.Background(), status)
}

// otherFeatures is a context licensed for everything EXCEPT audit_stream.
//
// A stronger negative than a bare context: it proves the gate asks about this
// feature rather than merely about whether a licence is present at all, which
// is the mistake that would make audit_stream free to anybody holding any
// licence.
func otherFeatures(t *testing.T) context.Context {
	t.Helper()
	var others []license.Feature
	for _, f := range license.AllFeatures() {
		if f != license.FeatureAuditStream {
			others = append(others, f)
		}
	}
	v := license.NewVerifier(nil)
	status := v.Evaluate(license.Claims{
		ID: "l", Org: "acme", Features: others,
		ExpiresAt: time.Now().AddDate(1, 0, 0),
	}, license.Evaluation{Org: "acme", Now: time.Now()})
	return feature.With(context.Background(), status)
}

// ---------------------------------------------------------------------------
// The record on the wire
// ---------------------------------------------------------------------------

func TestARecordCarriesBothTimestampsAndTheyAreDifferent(t *testing.T) {
	t.Parallel()
	// The reason OccurredAt exists at all. A sink stamps when forwarding
	// succeeded, which a retry can put minutes after the action, so a stream
	// carrying only that silently reorders itself whenever one destination is
	// slow.
	body, err := encode(entry(), forwarded)
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(body, &got))
	require.Equal(t, "2026-09-07T11:22:33.456789Z", got["occurred_at"])
	require.Equal(t, "2026-09-07T11:23:33.456789Z", got["forwarded_at"])
	require.NotEqual(t, got["occurred_at"], got["forwarded_at"])
}

func TestAnEntryWithNoTimestampSaysSoRatherThanBorrowingTheSinksClock(t *testing.T) {
	t.Parallel()
	// A guessed timestamp in an audit log is evidence that is wrong rather
	// than evidence that is missing, so the field is absent rather than filled
	// in from this process's clock.
	e := entry()
	e.OccurredAt = time.Time{}
	body, err := encode(e, forwarded)
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(body, &got))
	_, present := got["occurred_at"]
	require.False(t, present,
		"an entry whose producer said nothing was given this sink's clock as the time it happened")
	require.Equal(t, "2026-09-07T11:23:33.456789Z", got["forwarded_at"])
}

func TestTheFieldOrderIsFixedSoAHandWrittenParserCannotDrift(t *testing.T) {
	t.Parallel()
	// JSON from a map has no defined key order, and an audit stream whose
	// fields move between lines is one that neither diffs nor compresses and
	// that a hand written SIEM parser gets wrong exactly once.
	body, err := encode(entry(), forwarded)
	require.NoError(t, err)

	line := string(body)
	require.NotContains(t, line, "\n", "an entry has to be one line for every destination here")

	var last int
	for _, field := range []string{
		`"occurred_at"`, `"forwarded_at"`, `"org"`, `"actor"`, `"action"`,
		`"target_type"`, `"target_id"`, `"origin"`, `"detail"`,
	} {
		at := strings.Index(line, field)
		require.GreaterOrEqualf(t, at, 0, "%s is not in the record", field)
		require.Greaterf(t, at, last, "%s is out of order in %s", field, line)
		last = at
	}
}

func TestAnEntryThatCannotBeEncodedIsNamedRatherThanSwallowed(t *testing.T) {
	t.Parallel()
	// Reachable: Detail is map[string]any and a caller can put a channel in
	// it. An entry that is not going to reach anybody has to say so.
	e := entry()
	e.Detail = map[string]any{"leaked": make(chan int)}
	_, err := encode(e, forwarded)
	require.Error(t, err)
	require.ErrorContains(t, err, "golden.published",
		"the failure has to name which entry was lost")
}

// ---------------------------------------------------------------------------
// The licence gate, per call rather than per registration
// ---------------------------------------------------------------------------

// eachSink builds one of every sink over a destination that records what it
// received, so that a property claimed for "every sink" is checked against
// every sink rather than against the first one.
func eachSink(t *testing.T) map[string]struct {
	sink Sink
	sent func() int
} {
	t.Helper()
	out := map[string]struct {
		sink Sink
		sent func() int
	}{}

	server := recordingHTTPS(t, 200)
	hook, err := NewWebhook(WebhookConfig{
		URL: server.URL, DeadLetterFile: filepath.Join(t.TempDir(), "dead.jsonl"),
		Client: server.Client(), Now: at(forwarded), sleep: func(time.Duration) {},
	})
	require.NoError(t, err)
	out["webhook"] = struct {
		sink Sink
		sent func() int
	}{hook, server.count}

	store := recordingHTTPS(t, 201)
	object, err := NewObjectStore(ObjectStoreConfig{
		URL:    store.URL + "/audit-bucket/entries",
		Getenv: envMap(map[string]string{"AWS_ACCESS_KEY_ID": "AKIA", "AWS_SECRET_ACCESS_KEY": "s"}),
		Client: store.Client(), Now: at(forwarded), suffix: func() string { return "cafebabe" },
	})
	require.NoError(t, err)
	out["object_store"] = struct {
		sink Sink
		sent func() int
	}{object, store.count}

	collector := recordingSyslog(t)
	syslog, err := NewSyslog(SyslogConfig{
		Address: "siem.acme.example:6514", Now: at(forwarded), dial: collector.dial,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = syslog.Close() })
	out["syslog"] = struct {
		sink Sink
		sent func() int
	}{syslog, collector.count}

	return out
}

func TestEverySinkForwardsWhenAuditStreamIsLicensed(t *testing.T) {
	t.Parallel()
	// The positive control for the gate test below. Without this one, a sink
	// that forwarded nothing ever would pass the negative and the pair would
	// prove only that the code does nothing.
	for name, s := range eachSink(t) {
		t.Run(name, func(t *testing.T) {
			require.NoError(t, s.sink.Write(licensed(t), entry()))
			// Eventually rather than immediately, because the syslog collector
			// de-frames on its own goroutine and a write onto a pipe returns
			// when the bytes are taken rather than when they are parsed.
			require.Eventually(t, func() bool { return s.sent() == 1 },
				5*time.Second, 5*time.Millisecond, "a licensed installation forwarded nothing")
		})
	}
}

func TestNoSinkForwardsWithoutTheAuditStreamFeature(t *testing.T) {
	t.Parallel()
	// The gate is per call rather than per registration, because a licence can
	// expire while the process is running and a sink gated at registration
	// would keep forwarding an organization's audit stream to a destination
	// they have stopped paying for, with no way to stop it short of a restart.
	//
	// The context here holds every OTHER feature, so this fails if the gate
	// asks whether a licence exists rather than whether it grants this.
	for name, s := range eachSink(t) {
		t.Run(name, func(t *testing.T) {
			require.NoError(t, s.sink.Write(otherFeatures(t), entry()),
				"an unlicensed installation is unlicensed, not broken")
			require.Zero(t, s.sent(),
				"an audit entry was forwarded by an installation with no audit_stream licence")
		})
	}
}

func TestAnUnlicensedSinkIsSaidOutLoudOnceRatherThanBeingSilent(t *testing.T) {
	t.Parallel()
	// Configured and licensed are two different facts and only one of them is
	// visible from the receiving end. An operator watching an empty SIEM
	// dashboard has to be able to tell "nothing happened" from "nothing was
	// forwarded".
	sinks := []Sink{eachSink(t)["webhook"].sink}
	require.True(t, Unlicensed(otherFeatures(t), sinks))
	require.False(t, Unlicensed(licensed(t), sinks))
	require.False(t, Unlicensed(otherFeatures(t), nil),
		"an installation that configured no sink is not owed a warning about one")
}

func TestTheFeatureRecordsItsEnforcementSites(t *testing.T) {
	t.Parallel()
	// A feature a licence grants and nothing checks is a feature that is
	// silently free. Before this package, feature.Sites(FeatureAuditStream)
	// had nothing in it, which is exactly what that registry exists to make
	// visible.
	require.Contains(t, feature.Sites(license.FeatureAuditStream), AuditStreamSite)
}

func TestTheEnforcementSiteNamesAFileThatChecksThisExactFeature(t *testing.T) {
	t.Parallel()
	// The assertion above is strictly weaker than it looks: a registry
	// containing three strings passes it even when all three name nothing.
	// That is not hypothetical. compliance_packs was declared at
	// ee/engine/compliance.Pack.Evaluate for its whole life, and Pack.Evaluate
	// takes an Evidence and no context, so it cannot ask about a licence at
	// all. The string agreed with itself and with nothing else.
	//
	// So the site is read as a path, the file is opened, and it has to really
	// call feature.Enabled for THIS feature. A site naming a file that checks
	// a different feature fails, which is the other half of the same defect.
	path, symbol, found := strings.Cut(AuditStreamSite, ":")
	require.True(t, found, "a site is path:symbol, and %q has no colon", AuditStreamSite)
	require.NotEmpty(t, symbol)

	// Relative to ee/engine, which is what the licence catalogue's paths are
	// relative to, and the test runs in the package directory.
	source, err := os.ReadFile(filepath.Join("..", path))
	require.NoErrorf(t, err, "%s names a file that does not exist", AuditStreamSite)

	require.Contains(t, string(source), "feature.Enabled(ctx, license.FeatureAuditStream)",
		"%s names a file that never asks about the feature it claims to gate", AuditStreamSite)

	// And the symbol half names something in it, so a site cannot drift onto a
	// file that happens to check the feature somewhere else entirely.
	parts := strings.Split(symbol, ".")
	require.Contains(t, string(source), "func "+parts[len(parts)-1],
		"%s names a symbol that is not defined in that file", AuditStreamSite)
}

// ---------------------------------------------------------------------------
// What an installation's variables build
// ---------------------------------------------------------------------------

func envMap(m map[string]string) func(string) string {
	return func(name string) string { return m[name] }
}

func TestNoVariableRegistersNothing(t *testing.T) {
	t.Parallel()
	registry := extension.NewRegistry()
	sinks, err := RegisterFromEnvironment(registry, envMap(nil))
	require.NoError(t, err, "an installation that forwards nowhere is not an error")
	require.Empty(t, sinks)
	require.True(t, registry.Empty(), "something was registered for an unset variable")
}

func TestAConfiguredSinkIsInTheRegistryTheEngineConsults(t *testing.T) {
	t.Parallel()
	// The one property that distinguishes this from the state it replaced.
	// policyenforce.Hook was written, tested and constructed by no binary, and
	// audit_stream had no implementation to construct at all, so a test that a
	// sink can be built proves nothing about whether an entry reaches it.
	dead := filepath.Join(t.TempDir(), "spool", "dead.jsonl")
	registry := extension.NewRegistry()
	sinks, err := RegisterFromEnvironment(registry, envMap(map[string]string{
		SinksEnv:             "webhook",
		WebhookURLEnv:        "https://siem.acme.example/audit",
		WebhookDeadLetterEnv: dead,
	}))
	require.NoError(t, err)
	require.Len(t, sinks, 1)

	require.False(t, registry.Empty())
	require.Equal(t, []string{"audit:the audit webhook at https://siem.acme.example/audit"},
		registry.Registered())

	// And the registry hands it an entry, which is the call the engine makes.
	// The receiver does not exist, so this returns the forwarding failure
	// rather than nothing, and that is the proof it was asked at all.
	problems := registry.Audit(licensed(t), entry())
	require.Len(t, problems, 1, "the registry did not hand the entry to the registered sink")
	require.ErrorContains(t, problems[0], "siem.acme.example")

	// The entry landed in the dead letter file rather than evaporating.
	spooled, err := os.ReadFile(dead)
	require.NoError(t, err)
	require.Contains(t, string(spooled), "golden.published")
}

func TestTheOrderGivenIsTheOrderTheyAreWrittenIn(t *testing.T) {
	t.Parallel()
	// An installation that wants its SIEM to see an entry before its archive
	// does can say so, so the order is not incidental.
	registry := extension.NewRegistry()
	store := recordingHTTPS(t, 201)
	sinks, err := RegisterFromEnvironment(registry, envMap(map[string]string{
		SinksEnv:              "object_store, webhook",
		ObjectStoreURLEnv:     store.URL + "/bucket",
		AWSAccessKeyIDEnv:     "AKIA",
		AWSSecretAccessKeyEnv: "secret",
		WebhookURLEnv:         "https://siem.acme.example/audit",
		WebhookDeadLetterEnv:  filepath.Join(t.TempDir(), "dead.jsonl"),
	}))
	require.NoError(t, err)
	require.Equal(t, []string{
		"the bucket " + store.URL + "/bucket",
		"the audit webhook at https://siem.acme.example/audit",
	}, Describe(sinks))
}

func TestASinkThatCannotBeBuiltStopsTheProcessRatherThanForwardingNothing(t *testing.T) {
	t.Parallel()
	// The rule this whole lane exists for. Somebody who writes
	// AF_AUDIT_SINKS=syslog has said that every privileged action must reach
	// their SIEM. Starting anyway with the sink unbuilt means every action
	// goes unforwarded and nothing says so, which is a compliance control that
	// reports itself as held while holding nothing.
	registry := extension.NewRegistry()
	_, err := RegisterFromEnvironment(registry, envMap(map[string]string{
		SinksEnv: "syslog",
	}))
	require.Error(t, err, "a named sink with no address was accepted and forwards nothing")
	require.ErrorContains(t, err, SinksEnv)
	require.ErrorContains(t, err, "syslog")
	require.True(t, registry.Empty(),
		"a half built configuration left a sink registered")
}

func TestASinkNameThisBuildDoesNotKnowNamesTheOnesItDoes(t *testing.T) {
	t.Parallel()
	_, err := FromEnvironment(envMap(map[string]string{SinksEnv: "splunk"}))
	require.Error(t, err)
	require.ErrorContains(t, err, "splunk")
	for _, known := range Known() {
		require.ErrorContains(t, err, known,
			"the refusal did not say what this build can actually write to")
	}
}

func TestTheObviousSpellingsOfTheObjectStoreAllReachIt(t *testing.T) {
	t.Parallel()
	// The URL is what decides which protocol is spoken, so refusing s3 and
	// naming the canonical spelling in the error would be a round trip for
	// nothing.
	for _, name := range []string{"object_store", "objectstore", "s3", "azure_blob", "S3"} {
		t.Run(name, func(t *testing.T) {
			sinks, err := FromEnvironment(envMap(map[string]string{
				SinksEnv:              name,
				ObjectStoreURLEnv:     "s3://acme-audit/entries",
				AWSAccessKeyIDEnv:     "AKIA",
				AWSSecretAccessKeyEnv: "secret",
			}))
			require.NoError(t, err)
			require.Len(t, sinks, 1)
		})
	}
}
