package env

// The privileged actions this engine takes, said out loud to whatever is
// listening.
//
// The socket for this has existed since engine/pkg/extension was written:
// AuditSink, Registry.AddAuditSink and Registry.Audit are all there, tested,
// and documented. Nothing in the entire tree called Registry.Audit. The only
// two call sites were in extension_test.go, so audit_stream was a licensed,
// documented enterprise feature whose sink nothing ever wrote to, which is the
// same shippable gap as a block button that hides nothing: every piece present,
// the behaviour absent.
//
// This file is the missing half. It is in the community engine, MIT, next to
// checkPolicy and observe, for the same reason those are: a socket has to be in
// the thing being extended, and a call site that only exists in a build nobody
// runs is a call site nobody has tested.
//
// What counts as privileged here is deliberately narrow rather than
// comprehensive. An audit log a security team can read is one where every entry
// is an act somebody could be asked about: an environment holding a masked copy
// of production came into being, one was refused by organization policy, one
// was taken away, a golden was published to a shared store, and a published
// golden was pulled onto a machine. Egress decisions and build steps are
// deliberately absent: they are high volume, they are already reported through
// the event bus, and a stream nobody can read is worse than a smaller one they
// can.
//
// The contract the interface states is kept exactly. A sink observes and cannot
// alter, so nothing here reads the return value as a decision, and a sink that
// fails is reported through progress and never returned. That is the same rule
// observe follows and it matters most on teardown: a forwarding outage that
// stopped an environment being destroyed would turn a logging problem into a
// resource leak, which is strictly worse than the problem it came from.

import (
	"context"
	"fmt"
	"os"

	"github.com/antifailure/antifailure/engine/pkg/extension"
)

// Origin is what every entry this engine produces records as its origin.
//
// One value rather than the command's own name, because the control plane's
// audit log uses this column to say which surface an action came through, and
// "the engine" is the answer for all of these however they were invoked. The
// command is in the detail, where a reader who wants it can find it and a
// reader grouping by surface is not fragmented across a dozen values.
const auditOrigin = "engine"

// audit forwards one privileged action to whatever is registered.
//
// Named for what it does rather than for the registry, and shaped exactly like
// observe next door, so that the two rules that matter are visible in three
// lines: nothing is returned, and a registry with nothing in it costs one
// comparison.
//
// The empty check is not only an optimisation. Building an entry means reading
// the environment for the organization and the actor, and doing that on every
// action in the community edition, where there is provably nothing to send it
// to, is work for nobody.
func (o *Orchestrator) audit(ctx context.Context, entry extension.AuditEntry) {
	registry := o.extensions()
	if registry.Empty() {
		return
	}
	entry.Origin = auditOrigin
	entry.Org, entry.Actor = o.auditIdentity()
	// Stamped here rather than in each sink, so that three destinations
	// receiving the same action record the same instant for it. A sink that
	// stamped its own arrival time would make the same action look like three
	// actions at three times to anything correlating across them, and would put
	// a retry's delay into the timestamp of the thing that happened.
	if entry.OccurredAt.IsZero() {
		entry.OccurredAt = o.opts.Clock.Now()
	}
	for _, problem := range registry.Audit(ctx, entry) {
		o.progress(fmt.Sprintf("audit sink: %v", problem))
	}
}

// auditIdentity is the organization and the person this run belongs to.
//
// Both are read from the environment and both may be empty, and that is
// reported as empty rather than filled in with a guess. An audit entry
// attributed to the wrong actor is worse than one attributed to nobody: the
// first is evidence against a person and the second is a gap somebody can go
// and close.
//
// AF_ORG is the same variable the enterprise binary checks the licence against,
// so an installation that has a licence at all has this set and every entry
// carries it. AF_ACTOR is the explicit answer and GITHUB_ACTOR is the one a
// GitHub Actions runner sets for free, which is where most of these run. The
// operating system user is deliberately NOT consulted: on a CI runner it is
// `runner` for everybody, which reads as an attribution and is not one.
func (o *Orchestrator) auditIdentity() (org, actor string) {
	getenv := o.opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	actor = getenv("AF_ACTOR")
	if actor == "" {
		actor = getenv("GITHUB_ACTOR")
	}
	return getenv("AF_ORG"), actor
}

// auditDetail is the fields every environment entry carries.
//
// Repository and branch rather than the environment id alone, because an
// environment id is a project and a branch run through a hash and there is no
// way back from it. A security team reading a forwarded entry needs to know
// which repository it was without holding the engine that made the id.
func (o *Orchestrator) auditDetail() map[string]any {
	detail := map[string]any{}
	if o.opts.Repository != "" {
		detail["repository"] = o.opts.Repository
	}
	if o.opts.Manifest != nil && o.opts.Manifest.Name != "" {
		detail["project"] = o.opts.Manifest.Name
	}
	if o.opts.Branch != "" {
		detail["branch"] = o.opts.Branch
	}
	if o.opts.PullRequest > 0 {
		detail["pull_request"] = o.opts.PullRequest
	}
	return detail
}
