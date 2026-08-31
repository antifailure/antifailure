package env

// Who an environment belongs to, said on every event about it.
//
// The control plane holds environments in a table whose repository and branch
// columns are NOT NULL, so it cannot record an environment it has not been
// told the repository and branch of. Until this file existed the only event
// carrying either was env.creating, which carried the branch and not the
// repository, so nothing the control plane received was ever enough to create
// a row. The projection was an UPDATE that matched nothing, forever, and four
// features downstream of that table read empty: the console's environment
// list, the expiry it shows, the cost attribution, and the spend cap that is
// arithmetic over the same rows.
//
// Every lifecycle event carries the identity, not just the first one, and that
// is the ordering decision rather than a convenience. The control plane must
// be able to create the row from whichever event it hears first, and env.ready
// arriving before env.creating is not hypothetical here: the sink spools a
// failed batch to disk and drops the OLDEST events when it is over capacity
// (controlplane.Sink.Flush), and the oldest event of a run is exactly
// env.creating. An identity carried only on the first event is an identity
// that goes missing in precisely the case it is needed.
//
// None of this is data about the customer's system. A repository name and a
// branch name are already in every pull request URL, and the control plane
// already stores both for repositories the GitHub App is installed on. The
// boundary this must not cross is snapshots, secrets and captured bodies, and
// nothing here approaches it.

import (
	"github.com/antifailure/antifailure/engine/internal/events"
	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// identity is the field set every environment lifecycle event carries.
//
// Empty values are omitted rather than sent as empty strings. The control
// plane distinguishes "not told" from "told nothing", and an empty string in
// the repository field would be a repository named "" rather than a missing
// one.
func (o *Orchestrator) identity() []events.Field {
	fields := make([]events.Field, 0, 4)
	if o.opts.Repository != "" {
		fields = append(fields, events.F("repository", o.opts.Repository))
	}
	if o.opts.Branch != "" {
		fields = append(fields, events.F("branch", o.opts.Branch))
	}
	if o.opts.PullRequest > 0 {
		fields = append(fields, events.F("pull_request", o.opts.PullRequest))
	}
	if secs, ok := ttlSeconds(o.opts.Manifest); ok {
		fields = append(fields, events.F("ttl_seconds", secs))
	}
	return fields
}

// ttlSeconds is runtime.ttl as a number the control plane can add to a
// timestamp.
//
// Seconds rather than the manifest's own spelling, and that is the whole
// point. "168h" and "7d" are the engine's vocabulary, ParseDuration is the
// engine's parser, and shipping the string would mean a second parser in
// TypeScript that has to agree with this one forever. The two have no way to
// notice when they stop agreeing, which is the same shape as the event type
// lookup table that mapped nine names the engine cannot emit. A number needs
// no parser on the other side.
//
// The control plane adds it to the environment's creation time rather than to
// the time of the event carrying it, so this stays correct on an event that
// arrives late or out of order.
func ttlSeconds(m *schema.Manifest) (float64, bool) {
	if m == nil || m.Runtime == nil || m.Runtime.TTL == "" {
		return 0, false
	}
	d, err := manifest.ParseDuration(m.Runtime.TTL)
	if err != nil || d <= 0 {
		// Unreachable through a loaded manifest, which is validated before it
		// gets here. Reported as absent rather than as zero, because zero
		// seconds is an environment that expired the instant it was created
		// and a reaper that believes it would destroy live work.
		return 0, false
	}
	return d.Seconds(), true
}
