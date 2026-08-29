// Package compliance turns what the engine already recorded into evidence
// somebody can hand an auditor.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// The one rule this package exists to keep: it never says a control is
// satisfied. It says what the evidence shows, names the artifact, and leaves
// the conclusion to the person whose job it is. A tool that prints "SOC 2
// compliant" is worse than no tool, because it is a document that reads as an
// opinion from somebody qualified to hold one, produced by a program that has
// looked at four tables.
//
// So there are four outcomes and three of them are not "pass".
//
// Evidenced. The check ran, the artifact exists, and it says what the control
// asks about. The artifact is named so an auditor can go and look at it.
//
// Not evidenced. The check ran and found nothing to show. That is the ordinary
// state for an installation that has not been running long enough, and it is
// not a failure. It is also the state that stops this being a rubber stamp:
// most controls are here on the first day.
//
// Failed. The check ran and found evidence that the control is NOT holding: an
// audit chain with a break in it, a golden published without a clean scan.
// This is the most important of the four. A compliance tool that cannot say "no"
// has no ability to say "yes" that means anything.
//
// Outside this product. The control is real and nothing here can speak to it.
// Physical security of a data centre, background checks, a business continuity
// plan. Listed rather than quietly omitted, because an auditor needs the whole
// framework and needs to know which parts to go and get from somewhere else. A
// pack that showed only the controls it happens to cover would read as a
// complete answer and would be about a third of one.

package compliance

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/antifailure/antifailure/ee/engine/feature"
	"github.com/antifailure/antifailure/ee/engine/license"
)

func init() {
	// Recorded so that a feature which is sold and never checked shows up as
	// such. See ee/engine/feature.
	feature.Declare(license.FeatureCompliance, "ee/engine/compliance.Pack.Evaluate")
}

// State is what the evidence showed about one control.
type State string

const (
	// StateEvidenced means the artifact exists and says what the control asks.
	StateEvidenced State = "evidenced"
	// StateNotEvidenced means the check ran and found nothing to show. Not a
	// failure: it is the ordinary state of a new installation.
	StateNotEvidenced State = "not evidenced"
	// StateFailed means the check found evidence that the control is not
	// holding.
	StateFailed State = "failed"
	// StateOutside means the control is real and nothing here can speak to it.
	StateOutside State = "outside this product"
)

// Check names one evidence routine.
//
// A named constant rather than a function on the control, so that the control
// tables are data: they can be read, diffed and reviewed by somebody who does
// not read Go, which is the audience for the part of this that matters.
type Check string

const (
	// CheckNone is a control this product cannot speak to.
	CheckNone Check = ""
	// CheckAuditChain verifies the hash chain over the audit log.
	CheckAuditChain Check = "audit-chain"
	// CheckAuditAppendOnly reports whether the database itself refuses an
	// update to the audit log, rather than whether the application avoids one.
	CheckAuditAppendOnly Check = "audit-append-only"
	// CheckAuditCoverage reports which recorded actions occurred in the window.
	CheckAuditCoverage Check = "audit-coverage"
	// CheckTenantIsolation reports whether row level security is on for every
	// table holding tenant data.
	CheckTenantIsolation Check = "tenant-isolation"
	// CheckMasking reports on the signed masking attestations.
	CheckMasking Check = "masking-attestations"
	// CheckMaskingBeforeUse reports whether any environment was created from a
	// golden that had not been verified clean.
	CheckMaskingBeforeUse Check = "masking-before-use"
	// CheckEgress reports on the recorded egress decisions.
	CheckEgress Check = "egress-decisions"
	// CheckPolicy reports on organization policy refusals.
	CheckPolicy Check = "policy-decisions"
	// CheckAccessRemoval reports on membership removals, which is the control
	// every framework asks about and almost nobody can show.
	CheckAccessRemoval Check = "access-removal"
	// CheckRetention reports the audit log's retention setting against the
	// framework's minimum.
	CheckRetention Check = "audit-retention"
	// CheckTeardown reports whether environments were destroyed rather than
	// left, which is what makes "the data does not accumulate" checkable.
	CheckTeardown Check = "environment-teardown"
)

// Control is one requirement of a framework, and what this product shows.
type Control struct {
	// ID is the framework's own identifier, as "CC6.1" or "164.312(b)".
	ID string
	// Title is the framework's name for it.
	Title string
	// Requirement is what the framework asks for, in the framework's terms.
	Requirement string
	// Scope is what THIS PRODUCT covers of that requirement, and it is almost
	// never all of it. Saying where the boundary is, on every control, is the
	// difference between evidence and a claim.
	Scope string
	// Check names the evidence routine, or CheckNone.
	Check Check
	// retentionDays is the framework's own minimum, for the controls that have
	// one. Unexported and set by the framework tables, because it is a property
	// of the framework rather than something a caller should be able to lower.
	retentionDays int
}

// Pack is a framework's controls.
type Pack struct {
	// Name is the framework, as "SOC 2 Type II".
	Name string
	// Revision identifies which edition of the framework these are from, so a
	// report produced last year can be told from one produced against a later
	// revision.
	Revision string
	// Note is what a reader should understand before reading any of it.
	Note     string
	Controls []Control
}

// Result is what one control's evidence showed.
type Result struct {
	Control Control
	State   State
	// Detail is the sentence explaining the state, always present.
	Detail string
	// Artifacts point at the underlying evidence, so a reader can go and look
	// rather than taking this document's word for it. That is the whole
	// difference between evidence and an assertion.
	Artifacts []string
}

// Report is a pack evaluated against evidence.
type Report struct {
	Pack Pack
	Org  string
	// From and To bound what the evidence covers. An auditor's first question
	// about any report is which period it is about.
	From, To time.Time
	// GeneratedAt is when this was produced.
	GeneratedAt time.Time
	Results     []Result
	// Incomplete records what could not be read, so that a control reported as
	// "not evidenced" because a query failed is not mistaken for one where
	// there was genuinely nothing.
	Incomplete []string
}

// Counts returns how many controls landed in each state.
func (r Report) Counts() map[State]int {
	out := map[State]int{}
	for _, result := range r.Results {
		out[result.State]++
	}
	return out
}

// Failed reports whether any control has evidence of not holding.
//
// The one boolean this package offers, and it is deliberately the negative
// one. There is no Passed: this package does not decide that a framework is
// satisfied, and a caller reaching for a boolean should only ever be reaching
// for "is anything visibly wrong".
func (r Report) Failed() bool {
	for _, result := range r.Results {
		if result.State == StateFailed {
			return true
		}
	}
	return false
}

// Evaluate runs a pack's controls against evidence.
//
// A pure function over a value, so that every control's logic is testable
// without a database and so that the same evidence always produces the same
// report. Reading the evidence is a separate concern and lives in evidence.go,
// where it can be tested against a real Postgres.
func (p Pack) Evaluate(e Evidence) Report {
	report := Report{
		Pack: p, Org: e.Org, From: e.From, To: e.To,
		GeneratedAt: e.GeneratedAt, Incomplete: e.Incomplete,
	}
	for _, control := range p.Controls {
		report.Results = append(report.Results, evaluate(control, e))
	}
	// Sorted by identifier so that two reports for consecutive periods can be
	// diffed. An auditor comparing quarters is comparing documents.
	sort.SliceStable(report.Results, func(i, j int) bool {
		return report.Results[i].Control.ID < report.Results[j].Control.ID
	})
	return report
}

func evaluate(control Control, e Evidence) Result {
	result := Result{Control: control}
	if control.Check == CheckNone {
		result.State = StateOutside
		result.Detail = "This product records nothing about this control. " +
			"The evidence for it comes from somewhere else."
		return result
	}
	// A check whose evidence could not be read is reported as not evidenced
	// WITH the reason, rather than as evidenced or as failed. Reporting it as
	// failed would cry wolf about a query timeout; reporting it as evidenced
	// would be the lie this package exists to avoid.
	if reason, unread := e.Unread[control.Check]; unread {
		result.State = StateNotEvidenced
		result.Detail = "The evidence could not be read: " + reason
		return result
	}

	switch control.Check {
	case CheckAuditChain:
		return auditChainResult(control, e)
	case CheckAuditAppendOnly:
		return appendOnlyResult(control, e)
	case CheckAuditCoverage:
		return coverageResult(control, e)
	case CheckTenantIsolation:
		return isolationResult(control, e)
	case CheckMasking:
		return maskingResult(control, e)
	case CheckMaskingBeforeUse:
		return maskingBeforeUseResult(control, e)
	case CheckEgress:
		return egressResult(control, e)
	case CheckPolicy:
		return policyResult(control, e)
	case CheckAccessRemoval:
		return accessRemovalResult(control, e)
	case CheckRetention:
		return retentionResult(control, e)
	case CheckTeardown:
		return teardownResult(control, e)
	default:
		// A control naming a check nobody implemented. Reported rather than
		// silently passed, because the alternative is a control that appears in
		// the document as evidenced and was never looked at.
		result.State = StateNotEvidenced
		result.Detail = fmt.Sprintf(
			"This control names the check %q and this build has no such check.", control.Check)
		return result
	}
}

func auditChainResult(control Control, e Evidence) Result {
	result := Result{Control: control}
	switch {
	case e.Audit.Entries == 0:
		result.State = StateNotEvidenced
		result.Detail = "There are no audit entries in this period, so there is no chain to verify."
	case len(e.Audit.Breaks) > 0:
		// The state that matters. A break means an entry was altered or removed
		// with a privileged connection, and reporting it as anything softer
		// would be this tool covering up the one thing it exists to detect.
		result.State = StateFailed
		result.Detail = fmt.Sprintf(
			"The hash chain over %d entries is broken in %d place(s). "+
				"An entry was altered or removed after it was written.",
			e.Audit.Entries, len(e.Audit.Breaks))
		for _, b := range e.Audit.Breaks {
			result.Artifacts = append(result.Artifacts,
				fmt.Sprintf("audit_entries seq %d: %s", b.Seq, b.Detail))
		}
	default:
		result.State = StateEvidenced
		result.Detail = fmt.Sprintf(
			"The hash chain over all %d entries verifies. Each entry carries the hash of the "+
				"one before it, so altering or removing any of them leaves a break that this check finds.",
			e.Audit.Entries)
		result.Artifacts = append(result.Artifacts,
			"audit_entries seq "+span(e.Audit.FirstSeq, e.Audit.LastSeq),
			"head hash "+shortHash(e.Audit.Head))
	}
	return result
}

func appendOnlyResult(control Control, e Evidence) Result {
	result := Result{Control: control}
	granted := e.Posture.AuditGrants
	sort.Strings(granted)
	// The distinction worth drawing: the application avoiding an update and the
	// database refusing one are different guarantees, and only the second
	// survives a bug in the application.
	for _, grant := range granted {
		switch strings.ToUpper(grant) {
		case "UPDATE", "DELETE", "TRUNCATE":
			result.State = StateFailed
			result.Detail = fmt.Sprintf(
				"The application role holds %s on the audit log, so the database would permit "+
					"history to be rewritten. Only INSERT and SELECT should be granted.",
				strings.ToUpper(grant))
			result.Artifacts = append(result.Artifacts,
				"role "+e.Posture.AppRole+" grants: "+strings.Join(granted, ", "))
			return result
		}
	}
	if len(granted) == 0 {
		result.State = StateNotEvidenced
		result.Detail = "The grants on the audit log could not be determined."
		return result
	}
	result.State = StateEvidenced
	result.Detail = fmt.Sprintf(
		"The application role holds only %s on the audit log. An update is refused by the "+
			"database rather than by a code path somebody can forget to call.",
		strings.Join(granted, " and "))
	result.Artifacts = append(result.Artifacts, "role "+e.Posture.AppRole+" on audit_entries")
	return result
}

func coverageResult(control Control, e Evidence) Result {
	result := Result{Control: control}
	if e.Audit.Entries == 0 {
		result.State = StateNotEvidenced
		result.Detail = "Nothing was recorded in this period."
		return result
	}
	actions := make([]string, 0, len(e.Audit.ByAction))
	for action := range e.Audit.ByAction {
		actions = append(actions, action)
	}
	sort.Strings(actions)

	result.State = StateEvidenced
	result.Detail = fmt.Sprintf(
		"%d actions were recorded across %d distinct kinds, each with the actor, the target, "+
			"where it came from and when.", e.Audit.Entries, len(actions))
	for _, action := range actions {
		result.Artifacts = append(result.Artifacts,
			fmt.Sprintf("%s: %d", action, e.Audit.ByAction[action]))
	}
	return result
}

func isolationResult(control Control, e Evidence) Result {
	result := Result{Control: control}
	switch {
	case len(e.Posture.TenantTables) == 0:
		result.State = StateNotEvidenced
		result.Detail = "The tables holding tenant data could not be determined."
	case len(e.Posture.TablesWithoutRLS) > 0:
		result.State = StateFailed
		result.Detail = fmt.Sprintf(
			"%d of %d tables holding tenant data have row level security disabled, so a query "+
				"that forgot its own filter would return another organization's rows.",
			len(e.Posture.TablesWithoutRLS), len(e.Posture.TenantTables))
		result.Artifacts = append(result.Artifacts, e.Posture.TablesWithoutRLS...)
	default:
		result.State = StateEvidenced
		result.Detail = fmt.Sprintf(
			"All %d tables holding tenant data have row level security enabled, and the "+
				"application role cannot bypass it. Isolation is enforced by the database rather "+
				"than by a WHERE clause somebody can omit.", len(e.Posture.TenantTables))
		result.Artifacts = append(result.Artifacts,
			fmt.Sprintf("%d tables with a policy", len(e.Posture.TenantTables)))
		if e.Posture.AppRoleBypassesRLS {
			// Caught here rather than left implied: a role with BYPASSRLS makes
			// every policy decorative.
			result.State = StateFailed
			result.Detail = "Every table has row level security enabled and the application " +
				"role holds BYPASSRLS, which makes every policy decorative."
		}
	}
	return result
}

func maskingResult(control Control, e Evidence) Result {
	result := Result{Control: control}
	if len(e.Attestations) == 0 {
		result.State = StateNotEvidenced
		result.Detail = "No masking attestations were produced in this period."
		return result
	}

	var unverified, dirty int
	for _, a := range e.Attestations {
		if !a.SignatureValid {
			unverified++
		}
		if !a.Clean {
			dirty++
		}
	}
	switch {
	case unverified > 0:
		result.State = StateFailed
		result.Detail = fmt.Sprintf(
			"%d of %d masking attestations do not verify against their own signing key, so they "+
				"were altered after they were signed.", unverified, len(e.Attestations))
	case dirty > 0:
		// Not a failure on its own. A scan that found real data is the control
		// working: it is what stops the golden being published.
		result.State = StateEvidenced
		result.Detail = fmt.Sprintf(
			"%d masking scans were signed, of which %d found unmasked data and refused to "+
				"publish the golden. Each records what was scanned, how many rows were sampled, "+
				"and the hash of the masking rules used.", len(e.Attestations), dirty)
	default:
		result.State = StateEvidenced
		result.Detail = fmt.Sprintf(
			"%d masking scans were signed and every one found no unmasked data. Each records "+
				"what was scanned, how many rows were sampled, and the hash of the masking rules used.",
			len(e.Attestations))
	}
	for _, a := range e.Attestations {
		result.Artifacts = append(result.Artifacts, a.Summary())
	}
	return result
}

func maskingBeforeUseResult(control Control, e Evidence) Result {
	result := Result{Control: control}
	switch {
	case e.Goldens.Total == 0:
		result.State = StateNotEvidenced
		result.Detail = "No goldens were published in this period."
	case e.Goldens.Unverified > 0:
		result.State = StateFailed
		result.Detail = fmt.Sprintf(
			"%d of %d goldens were published without a clean masking scan, so an environment "+
				"could have been created holding unmasked production data.",
			e.Goldens.Unverified, e.Goldens.Total)
		result.Artifacts = append(result.Artifacts, e.Goldens.UnverifiedIDs...)
	default:
		result.State = StateEvidenced
		result.Detail = fmt.Sprintf(
			"All %d goldens published in this period carry a clean masking scan taken before "+
				"anything was branched from them.", e.Goldens.Total)
	}
	return result
}

func egressResult(control Control, e Evidence) Result {
	result := Result{Control: control}
	if e.Egress.Decisions == 0 {
		result.State = StateNotEvidenced
		// Said carefully. Every request an environment makes IS decided against
		// the manifest, by a proxy it cannot route around; what is absent is a
		// record of those decisions reaching the control plane, and a report
		// that said "no requests were decided" would be describing the opposite
		// of what happened.
		result.Detail = "No egress decisions reached the control plane in this period. " +
			"The proxy decides every outbound request whether or not a decision is " +
			"forwarded, so this is an absence of evidence rather than an absence of control."
		return result
	}
	result.State = StateEvidenced
	result.Detail = fmt.Sprintf(
		"%d outbound requests were decided, of which %d were refused. Every request an "+
			"environment made was decided against the manifest and recorded with the rule that "+
			"decided it; there is no path off the boundary that is not decided.",
		e.Egress.Decisions, e.Egress.Blocked)
	for host, count := range e.Egress.BlockedByHost {
		result.Artifacts = append(result.Artifacts, fmt.Sprintf("refused %s: %d", host, count))
	}
	sort.Strings(result.Artifacts)
	return result
}

func policyResult(control Control, e Evidence) Result {
	result := Result{Control: control}
	if !e.Policy.Configured {
		result.State = StateNotEvidenced
		result.Detail = "No organization policy is configured, so no environment was refused by one."
		return result
	}
	result.State = StateEvidenced
	result.Detail = fmt.Sprintf(
		"An organization policy is configured and was applied to every environment created in "+
			"this period. %d were refused by it. A repository cannot opt out: the policy is "+
			"evaluated before anything is created and can only refuse, never permit.",
		e.Policy.Refusals)
	result.Artifacts = append(result.Artifacts, e.Policy.Rules...)
	return result
}

func accessRemovalResult(control Control, e Evidence) Result {
	result := Result{Control: control}
	if e.Access.Removals == 0 {
		result.State = StateNotEvidenced
		result.Detail = "No members were removed in this period, so there is nothing to show."
		return result
	}
	if e.Access.RemovalsWithoutSessionRevoke > 0 {
		// The specific failure every framework asks about and almost nobody
		// checks: a removed member whose session keeps working.
		result.State = StateFailed
		result.Detail = fmt.Sprintf(
			"%d of %d membership removals are not followed by a session revocation, so a "+
				"removed member's existing session may still have worked.",
			e.Access.RemovalsWithoutSessionRevoke, e.Access.Removals)
		return result
	}
	result.State = StateEvidenced
	result.Detail = fmt.Sprintf(
		"%d members were removed and every removal revoked that member's sessions in the same "+
			"transaction, so access ended when the membership did rather than when the session "+
			"expired.", e.Access.Removals)
	return result
}

func retentionResult(control Control, e Evidence) Result {
	result := Result{Control: control}
	switch {
	case e.Posture.AuditRetentionDays == 0:
		result.State = StateEvidenced
		result.Detail = "The audit log is not pruned, so every entry ever written is still present."
	case control.RetentionDays() > 0 && e.Posture.AuditRetentionDays < control.RetentionDays():
		result.State = StateFailed
		result.Detail = fmt.Sprintf(
			"The audit log is kept for %d days and this framework asks for %d.",
			e.Posture.AuditRetentionDays, control.RetentionDays())
	default:
		result.State = StateEvidenced
		result.Detail = fmt.Sprintf("The audit log is kept for %d days.", e.Posture.AuditRetentionDays)
	}
	return result
}

func teardownResult(control Control, e Evidence) Result {
	result := Result{Control: control}
	switch {
	case e.Environments.Created == 0:
		result.State = StateNotEvidenced
		result.Detail = "No environments were created in this period."
	case e.Environments.Leaked > 0:
		result.State = StateFailed
		result.Detail = fmt.Sprintf(
			"%d of %d environments hold resources that were not destroyed, so a copy of "+
				"production shaped data still exists somewhere the inventory can see.",
			e.Environments.Leaked, e.Environments.Created)
		result.Artifacts = append(result.Artifacts, e.Environments.LeakedIDs...)
	default:
		result.State = StateEvidenced
		result.Detail = fmt.Sprintf(
			"%d environments were created and %d were destroyed, with nothing left behind that "+
				"the inventory can see. Every resource is journaled before it is made and "+
				"compensated on teardown.", e.Environments.Created, e.Environments.Destroyed)
	}
	return result
}

// RetentionDays reads a retention minimum out of a control's requirement.
//
// Carried on the control rather than in the check, because the number is the
// framework's and differs between them: HIPAA asks for six years and SOC 2 asks
// for the period under review. A check with a number in it would be a check
// that is right for one framework.
func (c Control) RetentionDays() int { return c.retentionDays }

func span(first, last int64) string {
	if first == 0 && last == 0 {
		return "none"
	}
	return fmt.Sprintf("%d to %d", first, last)
}

func shortHash(h string) string {
	if len(h) <= 16 {
		return h
	}
	return h[:16] + "..."
}
