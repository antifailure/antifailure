// Package sideeffect counts the dangerous external effects a change causes and
// blocks on an unexplained increase over the base branch.
//
// The egress and capture logs record every outbound request, but they count
// nothing semantic: a run that creates one PaymentIntent and a run that creates
// three look the same in a request tally. This family reads those same logs,
// classifies each outbound effect by what it MEANS (a charge, an email, a
// deleted bucket) rather than by its host, and compares the per class count of
// the change against the same workflows run on the base branch. The headline it
// exists for reads in one line: the checkout made one PaymentIntent before this
// change and three after, so the deploy is blocked.
//
// It rides the existing two environment oracle for the baseline and the
// existing capture log for the effects, and adds only the semantic counter over
// them. Two verdicts, and the difference between them is the whole design. An
// INCREASE over the base is a policy denial: the base did the same thing fewer
// times, so the change is refused until someone explains the delta or tunes the
// key. A DESTRUCTIVE operation is a verification failure and fires on its own,
// with no baseline defense, because "the base deleted a bucket too" is not a
// reason to allow deleting a bucket.
//
// The counter never treats a missing baseline as a baseline of zero. A base
// twin that could not be built leaves the increase comparison unmade rather
// than reporting every effect as new, because a zero that means "did not
// measure" is the exact defect this repository keeps finding in its own
// instruments. The destructive check, which needs no baseline, still runs.
package sideeffect

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/antifailure/antifailure/engine/internal/change"
	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/internal/runtime/local"
	"github.com/antifailure/antifailure/engine/internal/security"
)

// The two policy keys this family owns, which are also the Rule its findings
// carry. external_call is a policy denial (exit 6): the change did more than
// the base and is refused until the delta is explained. destructive_on_read is
// a verification failure (exit 7): a destructive effect was exercised against
// the twin and no baseline excuses it.
const (
	// RuleExternalCall fires when the change made more dangerous external calls
	// of some class than the base branch did.
	RuleExternalCall = report.PolicyKey("security.side_effect.external_call")
	// RuleDestructive fires when a destructive external operation ran at all,
	// regardless of what the base branch did.
	RuleDestructive = report.PolicyKey("security.side_effect.destructive_on_read")
)

// Class is one kind of dangerous external effect, counted per run. The set is
// deliberately the effects a rehearsal can recognise from the logs it already
// keeps, not an aspiration: a class nothing classifies would be a column of
// zeros pretending to be a check.
type Class string

const (
	// ClassPayment is a charge or a PaymentIntent created at a payment provider.
	ClassPayment Class = "payment"
	// ClassRefund is a refund issued at a payment provider.
	ClassRefund Class = "refund"
	// ClassEmail is a message sent through an email provider.
	ClassEmail Class = "email"
	// ClassSMS is a message sent through an SMS provider.
	ClassSMS Class = "sms"
	// ClassWebhook is an outbound webhook delivered to a destination.
	ClassWebhook Class = "webhook"
	// ClassCloudCreate is a cloud resource created through a provider API.
	ClassCloudCreate Class = "cloud_create"
	// ClassCloudDelete is a cloud resource deleted through a provider API. It is
	// the destructive class: it fires on its own with no baseline defense.
	ClassCloudDelete Class = "cloud_delete"
	// ClassQueuePublish is a message published to a broker or queue.
	ClassQueuePublish Class = "queue_publish"
)

// Counts is a per class tally of dangerous effects in one run.
type Counts map[Class]int

// destructiveClasses are the classes that fire absolutely, with no baseline
// comparison, because a destructive operation is refused whether or not the
// base branch made it too.
var destructiveClasses = []Class{ClassCloudDelete}

// allClasses is every effect class this family recognises.
var allClasses = []Class{
	ClassPayment, ClassRefund, ClassEmail, ClassSMS, ClassWebhook,
	ClassCloudCreate, ClassCloudDelete, ClassQueuePublish,
}

// increaseClasses are the classes compared against the baseline: every class
// that is not destructive. A destructive class is answered by its own absolute
// rule and must not also be reported as a mere increase, so it is filtered out
// here rather than left out of a hand written list that could drift.
var increaseClasses = nonDestructiveClasses()

// nonDestructiveClasses returns every class that is not destructive, so the two
// lists cannot disagree about whether a class is destructive.
func nonDestructiveClasses() []Class {
	var out []Class
	for _, c := range allClasses {
		if !isDestructive(c) {
			out = append(out, c)
		}
	}
	return out
}

// isDestructive reports whether a class is answered by the absolute destructive
// rule rather than the baseline comparison.
func isDestructive(c Class) bool {
	for _, d := range destructiveClasses {
		if d == c {
			return true
		}
	}
	return false
}

// Classify reduces a run's egress decisions and captured messages to a per
// class count of dangerous effects.
//
// The two sources are disjoint by class on purpose, so nothing is counted
// twice: email, SMS and webhook come from the capture log, which is where the
// proxy records a delivered provider message; payment, refund, cloud and queue
// come from the decision log, which is where an outbound API call lands. An
// outbound request is counted as an ATTEMPT regardless of the mode the firewall
// answered it in, because the count is a fact about what the CHANGE tries to do
// and the mode is a fact about how the twin contained it: a change that now
// tries to create three PaymentIntents where it tried one is the signal whether
// the provider was live, sandboxed, or mocked. A request the manifest does not
// let us classify is left out rather than counted as a generic effect, so an
// unknown host never inflates a count.
func Classify(decisions []local.Decision, messages []local.Message) Counts {
	c := Counts{}
	for _, m := range messages {
		switch strings.ToLower(strings.TrimSpace(m.Kind)) {
		case "email":
			c[ClassEmail]++
		case "sms":
			c[ClassSMS]++
		case "webhook":
			c[ClassWebhook]++
		}
	}
	for _, d := range decisions {
		if class, ok := classifyDecision(d); ok {
			c[class]++
		}
	}
	return c
}

// classifyDecision maps one outbound decision to the effect it represents, or
// reports that the decision is not a classified dangerous effect. It is keyed
// on host, method and path prefix, the same three facts the capture log already
// uses to route a host to a provider handler.
func classifyDecision(d local.Decision) (Class, bool) {
	host := strings.ToLower(strings.TrimSpace(d.Host))
	method := strings.ToUpper(strings.TrimSpace(d.Method))
	path := strings.ToLower(d.Path)
	switch {
	case isStripeHost(host) && method == "POST":
		switch {
		case strings.Contains(path, "/refunds"):
			return ClassRefund, true
		case strings.Contains(path, "/payment_intents"), strings.Contains(path, "/charges"):
			return ClassPayment, true
		}
		return "", false
	case isQueueHost(host):
		if method == "POST" || method == "PUT" {
			return ClassQueuePublish, true
		}
		return "", false
	case isCloudHost(host):
		switch method {
		case "DELETE":
			return ClassCloudDelete, true
		case "POST", "PUT":
			return ClassCloudCreate, true
		}
		return "", false
	}
	return "", false
}

// isStripeHost recognises the payment provider whose paths this family reads.
func isStripeHost(host string) bool {
	return host == "api.stripe.com" || strings.HasSuffix(host, ".stripe.com")
}

// isQueueHost recognises a broker or queue endpoint. It is checked before the
// generic cloud host so a queue publish is not miscounted as a cloud create,
// since a queue often lives on a cloud provider's domain.
func isQueueHost(host string) bool {
	return strings.HasPrefix(host, "sqs.") ||
		strings.HasPrefix(host, "pubsub.googleapis.com") ||
		strings.Contains(host, ".servicebus.windows.net")
}

// isCloudHost recognises a cloud provider control plane API, where a create is
// dangerous and a delete is destructive.
func isCloudHost(host string) bool {
	return strings.HasSuffix(host, ".amazonaws.com") ||
		strings.HasSuffix(host, ".googleapis.com") ||
		strings.HasSuffix(host, ".azure.com") ||
		host == "management.azure.com"
}

// Detect turns a head count, a base count, and whether the base was measurable
// into findings. It never treats an unmeasured base as a base of zero: when
// baseOK is false the increase comparison is skipped entirely and only the
// absolute destructive rule runs.
//
// The level of each finding is read from the policy, never decided here, so the
// manifest is the one place a project sets what an increase or a destructive
// effect does to the check. A key resolved to ignore emits nothing, the same
// discipline every other finding builder in the engine holds.
func Detect(head, base Counts, baseOK bool, pol report.Policy) []report.Finding {
	var out []report.Finding

	// Destructive first, and absolute. A destructive operation is refused
	// whether or not the base branch made it too, so it needs no baseline.
	destructiveLevel := pol.Level(RuleDestructive)
	if destructiveLevel != report.LevelIgnore {
		for _, c := range sortedClasses(destructiveClasses) {
			n := head[c]
			if n == 0 {
				continue
			}
			out = append(out, report.Finding{
				Rule:  string(RuleDestructive),
				Level: destructiveLevel,
				Title: "a destructive external operation ran against the sanitized twin",
				Detail: fmt.Sprintf(
					"the workflows drove %s against the twin; a destructive operation is refused whether or not the base branch made it too",
					plural(n, string(c)+" operation", string(c)+" operations")),
				Fix:   "Confirm the change is meant to perform this destructive operation. If it is, name the workflow that should be allowed to; a read oriented rehearsal should not delete infrastructure.",
				Count: n,
				Where: string(c),
			})
		}
	}

	// Increase next, and only when the base was measurable. A missing base is
	// not a base of zero: without it the increase is simply unmeasured, and
	// reporting every effect as new would be the "zero means did not measure"
	// defect this family exists to avoid.
	if baseOK {
		increaseLevel := pol.Level(RuleExternalCall)
		if increaseLevel != report.LevelIgnore {
			for _, c := range sortedClasses(increaseClasses) {
				h, b := head[c], base[c]
				if h <= b {
					continue
				}
				out = append(out, report.Finding{
					Rule:  string(RuleExternalCall),
					Level: increaseLevel,
					Title: "the change made more dangerous external calls than the base branch",
					Detail: fmt.Sprintf(
						"%s: the base branch made %d and this change made %d across the same workflows",
						c, b, h),
					Fix:   "Explain the extra calls or tune security.side_effect.external_call in the manifest policy block. An unintended increase is often a retry loop or a duplicated call added by the change.",
					Count: h - b,
					Where: string(c),
				})
			}
		}
	}
	return out
}

// sortedClasses returns a class slice in a stable order, so findings come out
// in the same order every run and a test can assert them.
func sortedClasses(in []Class) []Class {
	out := append([]Class(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// plural renders a count with the right noun.
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// keys declares the two policy keys this family owns, with the level each
// carries when the manifest is silent and the exit code its rule produces. The
// exits are the literals the namespace agrees on, and a test proves each equals
// security.ExitFor(key) so the declared exit and the gate's exit cannot drift.
func keys() []security.KeySpec {
	return []security.KeySpec{
		{
			Key:     RuleExternalCall,
			Default: report.LevelWarn,
			Title:   "the change made more dangerous external calls than the base branch",
			Docs:    "concepts/security",
			Exit:    report.ExitPolicyDenial,
		},
		{
			Key:     RuleDestructive,
			Default: report.LevelFail,
			Title:   "a destructive external operation ran against the sanitized twin",
			Docs:    "concepts/security",
			Exit:    report.ExitVerification,
		},
	}
}

// The surfaces and check this family binds to, matching the reconciled routing:
// a change to code, a service, or the schema is what can add a dangerous effect.
var (
	familySurfaces = []change.Surface{change.SurfaceCode, change.SurfaceService, change.SurfaceSchema}
	familyChecks   = []change.Check{change.CheckSideEffect}
)

// ensure the family satisfies the interface at compile time.
var _ security.Family = (*family)(nil)

// family is the side effect counter as a registered security family.
type family struct{}

// New builds the side effect family. The router registers it with a bare New
// and populates the per run effect and baseline data through security.Input.
func New() security.Family {
	return &family{}
}

func (f *family) Name() string               { return "side_effect" }
func (f *family) Surfaces() []change.Surface { return familySurfaces }
func (f *family) Checks() []change.Check     { return familyChecks }
func (f *family) Keys() []security.KeySpec   { return keys() }
func (f *family) Licensed() string           { return "" }

// ReadsBaseline marks side_effect as a base-twin consumer, so af ci builds the
// base environment its increase rule diffs against. Without it the collector
// would leave Input.Baseline ok=false on every run and the increase comparison
// would never fire, which was the dormant state this family shipped in: fully
// built, correctly consuming a baseline it was never handed. It satisfies
// security.BaselineReader.
func (f *family) ReadsBaseline() {}

// Probe classifies the run's effects, compares them against the base twin, and
// returns the findings. A run whose candidate logs were not captured at all
// (both the decision and message logs nil) is a blocked probe, because a run
// whose effects we could not read has told us nothing and must never read as a
// pass. A base twin that was not built leaves the increase comparison unmade
// rather than diffing against a base of zero.
func (f *family) Probe(_ context.Context, in security.Input) ([]report.Finding, error) {
	decisions, messages := in.Decisions(), in.Messages()
	if decisions == nil && messages == nil {
		return nil, fmt.Errorf(
			"the side effect counter could not read this run's captured effects, so it counted nothing")
	}
	head := Classify(decisions, messages)

	var base Counts
	baseline, baseOK := in.Baseline()
	if baseOK {
		base = Classify(baseline.Decisions, baseline.Messages)
	}
	return Detect(head, base, baseOK, in.Policy), nil
}
