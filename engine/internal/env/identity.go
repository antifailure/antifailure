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
	"time"

	"github.com/antifailure/antifailure/engine/internal/events"
)

// startedAt reports the instant an environment began existing, for the events
// emitted by the command that is bringing it up.
//
// This is the field the bill is computed from, and it is separate from the
// event's own timestamp because those two are not the same instant and the
// difference is money. The control plane measures usage as created_at to
// torn_down_at, and it takes created_at from the earliest thing it is told.
// If the only event it ever receives for an environment is env.ready, and
// ready carries just its own timestamp, then created_at lands AFTER the build
// and the customer is not charged for the build. A cold build is the
// expensive part of a run, so the number would be quietly too low forever and
// would look exactly like a correct number.
//
// That is not a hypothetical ordering. The sink drops the OLDEST events when
// a failed batch overflows its spool, and env.creating is the oldest event of
// a run, so the case where ready is the first thing the control plane hears
// is the ordinary consequence of one outage.
//
// Only the commands that know the instant send it. af down runs in a separate
// process and has no way to know when the environment came up, so its event
// carries none and the control plane keeps whatever it already had.
func startedField(at time.Time) events.Field {
	return events.F("started_at", at.UTC().Format(time.RFC3339Nano))
}

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
	if secs, ok := o.ttlSeconds(); ok {
		fields = append(fields, events.F("ttl_seconds", secs))
	}
	return fields
}

// ttlSeconds is the lifetime this orchestrator stamps on its resources, as a
// number the control plane can add to a timestamp.
//
// It reads Orchestrator.ttl rather than parsing runtime.ttl a second time, and
// that is the point: the reaper destroys an environment when the expiry
// LABEL says to, and the console shows the expiry this number produces. Two
// parses of the same field would agree until somebody changed one of them,
// and the disagreement would be a console that says an environment lives
// until Friday and a reaper that took it on Thursday.
//
// Seconds rather than the manifest's own spelling, because "168h" and "7d" are
// the engine's vocabulary and ParseDuration is the engine's parser. Shipping
// the string would put a second duration parser in TypeScript that has to
// agree with this one forever, with no way to notice when it stops, which is
// the same shape as the event type lookup table that mapped nine names the
// engine cannot emit. A number needs no parser on the other side.
//
// Not the absolute expiry the resources carry, deliberately. That is stamped
// per resource as clock.Now()+ttl inside Runtime.managed, so one environment
// carries several, which is why the reaper takes the latest within an
// environment. There is no single absolute value to send. The control plane
// adds this duration to the instant the environment came up, so the answer is
// one number per environment and it survives an event arriving out of order.
func (o *Orchestrator) ttlSeconds() (float64, bool) {
	d := o.ttl()
	if d <= 0 {
		// Zero is reported as absent rather than as zero seconds, because an
		// environment that expired the instant it was created is a reaper
		// destroying live work.
		return 0, false
	}
	return d.Seconds(), true
}
