package mcp

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/antifailure/antifailure/engine/internal/auth"
	"github.com/antifailure/antifailure/engine/internal/redact"
)

// The orientation tools are the ones an agent calls first, and they are the
// cheapest thing this server does.
//
// They exist because every expensive tool here fails for the same handful of
// environmental reasons, and being told "the rehearsal could not run" twenty
// minutes in is a worse answer than being told "this machine has no container
// daemon" before anything starts.

// Diagnosis is what af doctor found, in a shape this package can hold.
//
// It is declared here rather than imported because the checks live in the
// package that builds this server's command, and that package imports this
// one. So the result crosses the boundary as a plain struct filled in by the
// caller, exactly as the rehearsal and the decision log already do.
type Diagnosis struct {
	// OK is false when at least one check failed, meaning this machine cannot
	// run Antifailure until something is fixed.
	OK       bool
	Platform string
	Checks   []DiagnosticCheck
}

// DiagnosticCheck is one question about the machine and its answer.
type DiagnosticCheck struct {
	Name string
	// Status is pass, fail, warn or skip. A skip is a check that did not
	// apply or could not be run, and it is never a pass.
	Status string
	Detail string
	// Remediation is what to do about it. Every check carries one, including
	// the passing ones, because a diagnostic that says something is wrong and
	// stops costs the same attention and yields nothing.
	Remediation string
}

// RunnerReadiness is what af runner check found.
type RunnerReadiness struct {
	// Verdict is ready, blocked or undetermined. Three and not two: a runner
	// whose manifest could not be parsed is not a working runner and is not a
	// broken one, and folding that into either is how this check came to
	// report ready about a tree it had never read.
	Verdict string
	// Path is the runner a run would actually use, which is the nearest one
	// that can run rather than the nearest one that exists.
	Path string
	// Node is the version range the runner declares.
	Node string
	// Unanswered names the deciding questions that could not be answered,
	// empty unless the verdict is undetermined.
	Unanswered []string
	Checks     []DiagnosticCheck
}

// diagnoseMachine runs the machine checks. Nil when this build did not wire
// them, which is reported as not checked rather than as a pass.
type diagnoseMachine func(ctx context.Context) (Diagnosis, error)

// checkRunnerReady inspects the browser agent runner without executing it.
type checkRunnerReady func(ctx context.Context) (RunnerReadiness, error)

// readAccount reports who this machine is signed in as, and optionally what
// the model provider keys on that control plane may spend.
type readAccount func(ctx context.Context, includeProviders bool) (Account, error)

// Account is the control plane's answer about this machine.
//
// There is no token field and no key field. What identifies a credential here
// is its prefix and a fingerprint, which is what the control plane itself
// publishes for the purpose.
type Account struct {
	// SignedIn is false when this machine has no credential, which is the
	// ordinary state of a laptop and is not a failure.
	SignedIn     bool
	ControlPlane string
	Login        string
	Name         string
	Organization string
	Role         string
	Scopes       []string
	TokenPrefix  string
	ExpiresAt    string
	StoredIn     string
	// Providers is the model provider keys stored on the control plane and
	// their monthly caps. Absent when the caller did not ask, or when the
	// credential does not carry the scope that reads them.
	Providers *ProviderSpend
}

// ProviderSpend is what the stored model keys may spend this month.
type ProviderSpend struct {
	// Sealing reports whether this control plane can store a key at all. An
	// installation with no sealing secret explains itself rather than looking
	// merely empty.
	Sealing bool
	// Unavailable says why the listing could not be read, and is the reason
	// an absent list is never reported as an empty one.
	Unavailable string
	Keys        []ProviderKeyState
	Budgets     []ProviderBudgetState
}

// ProviderKeyState is one stored key, identified and never revealed.
type ProviderKeyState struct {
	Provider    string
	Last4       string
	Fingerprint string
	CreatedAt   string
	RotatedAt   string
}

// ProviderBudgetState is one provider's cap for the current month.
type ProviderBudgetState struct {
	Provider     string
	Period       string
	CapUSD       float64
	SpentUSD     float64
	RemainingUSD float64
}

// ---------------------------------------------------------------------------
// scrubbing free form text on its way into a result
// ---------------------------------------------------------------------------

// resultRedactor removes recognisable credentials from free form text.
//
// This is defence in depth rather than the main control. The main control is
// that no result type here has a field for a secret and no adapter puts one in
// one: af model show has no key field, af provider list publishes a last four
// and a fingerprint, and af whoami publishes a token prefix. What this catches
// is the day a value arrives inside a sentence somebody else wrote: a
// provider's error body quoting the key it rejected, an application's log line,
// a connection string in a runtime's complaint. The engine already redacts the
// model key out of a provider's message, and this is what still holds if it
// ever stops.
//
// It carries the engine's own pattern rules and no registered values, so it
// works with nothing configured and recognises the shapes those rules name. A
// secret with no recognisable shape is registered by the adapter that holds it,
// which is why the model and account adapters build their own redactor rather
// than relying on this one alone.
//
// Redactor.String reads its registered set through an atomic load and this one
// never has anything registered, so it is safe to share across calls.
var resultRedactor = redact.New()

// safeText is neutralize with a credential scrub in front of it.
//
// Every free form string this package puts in a result goes through it: text
// written by the application under test, by a provider, by a runtime, or by
// this engine. Bounded identifiers keep using safeIdentifier, which is
// stricter still.
func safeText(s string, max int) string {
	if s == "" {
		return ""
	}
	return neutralize(resultRedactor.String(s), max)
}

// scrubberFor builds a redactor that also knows the exact values it is handed.
//
// A model key for a self hosted endpoint and an engine token have no
// recognisable prefix, so a pattern rule cannot find them. An adapter that is
// holding one registers it here, and every form of it, encoded or not, is then
// replaced wherever it appears in the text that adapter is about to return.
func scrubberFor(secrets ...string) *redact.Redactor {
	r := redact.New()
	for _, secret := range secrets {
		if secret != "" {
			r.Register(secret)
		}
	}
	return r
}

// ---------------------------------------------------------------------------
// check_prerequisites
// ---------------------------------------------------------------------------

type prerequisitesResult struct {
	Kind    string `json:"kind"`
	Summary string `json:"summary"`
	// Verdict is ready, blocked or undetermined, over everything that was
	// asked for. Undetermined means a question that decides could not be
	// answered at all, which is neither a yes nor a no and is never reported
	// as either.
	Verdict  string `json:"verdict"`
	Engine   string `json:"engine_version"`
	Platform string `json:"platform,omitempty"`
	Project  string `json:"project"`
	// Machine is af doctor: whether this machine can run anything at all.
	Machine *sectionDoc `json:"machine,omitempty"`
	// Runner is af runner check: whether the browser agents can run.
	Runner *runnerSectionDoc `json:"browser_agents,omitempty"`
	// NotChecked names what was asked for and could not be looked at, so a
	// caller is never told ok about something nobody examined.
	NotChecked []string `json:"not_checked,omitempty"`
	Note       string   `json:"note,omitempty"`
}

type sectionDoc struct {
	Verdict string     `json:"verdict"`
	Checks  []checkDoc `json:"checks"`
	Blocked []checkDoc `json:"blocking,omitempty"`
	Total   int        `json:"total"`
}

type runnerSectionDoc struct {
	sectionDoc
	Path       string   `json:"runner_path,omitempty"`
	Node       string   `json:"node_required,omitempty"`
	Unanswered []string `json:"unanswered,omitempty"`
}

type checkDoc struct {
	Name string `json:"name"`
	// Result is pass, fail, warn or skip. A skip is a question that was not
	// answered and is never a pass.
	Result      string `json:"result"`
	Detail      string `json:"detail,omitempty"`
	Remediation string `json:"remediation,omitempty"`
}

const (
	verdictReady        = "ready"
	verdictBlocked      = "blocked"
	verdictUndetermined = "undetermined"
)

func newCheckPrerequisitesTool(
	p *Project, diagnose diagnoseMachine, runner checkRunnerReady,
) *Tool {
	return &Tool{
		Name:     "check_prerequisites",
		Title:    "Can this machine run anything",
		ReadOnly: true,
		Description: "Answer whether this machine can actually run Antifailure, and " +
			"whether the browser driving agents can run, before anything expensive is " +
			"attempted. Call this first in a session, and call it again the moment " +
			"something fails for a reason that might be the machine rather than the code: " +
			"no container daemon, no disk, no route out, no browser, a node too old. Every " +
			"failing check carries what to do about it. The verdict has three values and " +
			"not two: ready means every deciding question was asked and answered yes, " +
			"blocked means one was answered no, and undetermined means one could not be " +
			"answered at all, which is neither and is never reported as ready. This runs " +
			"local probes only and changes nothing.",
		Input: &Schema{
			Type:     "object",
			Required: []string{"project_id"},
			Properties: map[string]*Schema{
				"project_id": projectIDSchema(),
				"scope": {
					Type: "string",
					Enum: []string{"machine", "browser_agents", "both"},
					Description: "What to check. Defaults to both. machine is what every " +
						"command needs: a container daemon, disk, ports, name resolution, a " +
						"route out, git and a Postgres client. browser_agents is what driving " +
						"a real browser needs: the runner, its dependencies, a new enough " +
						"node, and the browser itself.",
				},
			},
		},
		Handler: func(ctx context.Context, _ *Call, args map[string]any) (any, *Fault) {
			if fault := p.checkAssertion(args); fault != nil {
				return nil, fault
			}
			return checkPrerequisites(ctx, p, diagnose, runner, args)
		},
	}
}

func checkPrerequisites(
	ctx context.Context, p *Project, diagnose diagnoseMachine, runner checkRunnerReady,
	args map[string]any,
) (any, *Fault) {
	scope, _ := args["scope"].(string)
	if scope == "" {
		scope = "both"
	}
	out := prerequisitesResult{
		Kind: "prerequisites", Engine: buildVersion, Project: p.ID, Verdict: verdictReady,
	}

	if scope == "machine" || scope == "both" {
		switch {
		case diagnose == nil:
			// Stated rather than silently omitted. A section that vanishes
			// reads as a section that passed, and this build genuinely did
			// not look.
			out.NotChecked = append(out.NotChecked,
				"the machine: this build did not wire the machine checks into this server, "+
					"so nothing here says whether a container daemon, disk or a route out is "+
					"available. Run af doctor at a terminal.")
			out.Verdict = worseVerdict(out.Verdict, verdictUndetermined)
		default:
			d, err := diagnose(ctx)
			if err != nil {
				out.NotChecked = append(out.NotChecked,
					"the machine: the checks could not be run, so nothing here says whether "+
						"this machine can run anything. The server log says why.")
				out.Verdict = worseVerdict(out.Verdict, verdictUndetermined)
				break
			}
			out.Platform = safeText(d.Platform, 120)
			section := describeChecks(d.Checks)
			section.Verdict = machineVerdict(d)
			out.Machine = &section
			out.Verdict = worseVerdict(out.Verdict, section.Verdict)
		}
	}

	if scope == "browser_agents" || scope == "both" {
		switch {
		case runner == nil:
			out.NotChecked = append(out.NotChecked,
				"the browser agents: this build did not wire the runner check into this "+
					"server, so nothing here says whether a browser can be driven. Run "+
					"af runner check at a terminal.")
			out.Verdict = worseVerdict(out.Verdict, verdictUndetermined)
		default:
			r, err := runner(ctx)
			if err != nil {
				out.NotChecked = append(out.NotChecked,
					"the browser agents: the runner could not be inspected, so nothing here "+
						"says whether a browser can be driven. The server log says why.")
				out.Verdict = worseVerdict(out.Verdict, verdictUndetermined)
				break
			}
			section := runnerSectionDoc{sectionDoc: describeChecks(r.Checks)}
			section.Verdict = normaliseVerdict(r.Verdict)
			section.Path = safeText(r.Path, 300)
			section.Node = safeText(r.Node, 60)
			for i, q := range r.Unanswered {
				if i >= 10 {
					break
				}
				section.Unanswered = append(section.Unanswered, safeText(q, 200))
			}
			out.Runner = &section
			out.Verdict = worseVerdict(out.Verdict, section.Verdict)
		}
	}

	out.Summary = prerequisitesSummary(out)
	if out.Verdict != verdictReady {
		out.Note = "A support bundle collects this and the rest of the machine's state, " +
			"redacted, for somebody to look at: run af support bundle at a terminal. It is " +
			"not offered as a tool here, because a bundle carries the application's own " +
			"logs and every outbound request it made, and that is not content to put " +
			"through a model's context."
	}
	return out, nil
}

func describeChecks(checks []DiagnosticCheck) sectionDoc {
	doc := sectionDoc{Checks: []checkDoc{}, Total: len(checks)}
	for _, c := range checks {
		entry := checkDoc{
			Name:        safeText(c.Name, 80),
			Result:      normaliseResult(c.Status),
			Detail:      safeText(c.Detail, 300),
			Remediation: safeText(c.Remediation, 400),
		}
		doc.Checks = append(doc.Checks, entry)
		if entry.Result == "fail" || entry.Result == "skip" {
			doc.Blocked = append(doc.Blocked, entry)
		}
	}
	return doc
}

// normaliseResult keeps the four words closed.
//
// A status this package does not recognise is reported as unknown rather than
// being passed through, so a new word on the other side of the boundary cannot
// arrive here looking like a pass to something branching on the string.
func normaliseResult(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "pass", "ok":
		return "pass"
	case "fail":
		return "fail"
	case "warn":
		return "warn"
	case "skip":
		return "skip"
	default:
		return "unknown"
	}
}

func normaliseVerdict(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case verdictReady:
		return verdictReady
	case verdictBlocked:
		return verdictBlocked
	default:
		return verdictUndetermined
	}
}

// machineVerdict maps a diagnosis onto the same three words the runner check
// uses, so one result does not carry two vocabularies.
//
// A skipped check makes it undetermined rather than ready. That is the whole
// point of the third word: a check that did not run is not a check that
// passed, and reporting it as one is the defect this repository keeps finding
// in its own instruments.
func machineVerdict(d Diagnosis) string {
	verdict := verdictReady
	for _, c := range d.Checks {
		switch normaliseResult(c.Status) {
		case "fail":
			return verdictBlocked
		case "skip", "unknown":
			verdict = verdictUndetermined
		}
	}
	if !d.OK {
		return verdictBlocked
	}
	return verdict
}

// worseVerdict combines two, with blocked beating undetermined.
//
// Both mean do not proceed, and between them the actionable one is the proof.
func worseVerdict(a, b string) string {
	rank := func(v string) int {
		switch v {
		case verdictBlocked:
			return 0
		case verdictUndetermined:
			return 1
		default:
			return 2
		}
	}
	if rank(b) < rank(a) {
		return b
	}
	return a
}

func prerequisitesSummary(out prerequisitesResult) string {
	var b strings.Builder
	switch out.Verdict {
	case verdictReady:
		b.WriteString("This machine is ready. ")
	case verdictBlocked:
		b.WriteString("BLOCKED: something here has to be fixed before a run can work. ")
	default:
		b.WriteString("UNDETERMINED: a question that decides whether this can run was not " +
			"answered, so nothing here claims it can. That is not the same as a pass. ")
	}
	if out.Machine != nil {
		fmt.Fprintf(&b, "The machine checks are %s over %d %s",
			out.Machine.Verdict, out.Machine.Total,
			plural(out.Machine.Total, "check", "checks"))
		if n := len(out.Machine.Blocked); n > 0 {
			fmt.Fprintf(&b, ", %d of which %s in the way: %s",
				n, plural(n, "is", "are"), namesOf(out.Machine.Blocked))
		}
		b.WriteString(". ")
	}
	if out.Runner != nil {
		fmt.Fprintf(&b, "The browser agents are %s", out.Runner.Verdict)
		if n := len(out.Runner.Blocked); n > 0 {
			fmt.Fprintf(&b, ", held by %s", namesOf(out.Runner.Blocked))
		}
		b.WriteString(". ")
	}
	if len(out.NotChecked) > 0 {
		fmt.Fprintf(&b, "%d %s not checked at all, and what was not looked at is listed "+
			"rather than assumed. ", len(out.NotChecked),
			plural(len(out.NotChecked), "thing was", "things were"))
	}
	if out.Verdict != verdictReady {
		b.WriteString("Each check above carries what to do about it.")
	}
	return strings.TrimSpace(b.String())
}

func namesOf(checks []checkDoc) string {
	names := make([]string, 0, len(checks))
	for i, c := range checks {
		if i >= 6 {
			names = append(names, "and others")
			break
		}
		names = append(names, c.Name)
	}
	return clip(strings.Join(names, ", "), 200)
}

// ---------------------------------------------------------------------------
// describe_control_plane_account
// ---------------------------------------------------------------------------

type accountResult struct {
	Kind     string `json:"kind"`
	Summary  string `json:"summary"`
	SignedIn bool   `json:"signed_in"`
	// ControlPlane is the origin this machine talks to. It is decided by this
	// server's own environment, never by a tool argument.
	ControlPlane string   `json:"control_plane"`
	Login        string   `json:"login,omitempty"`
	Name         string   `json:"name,omitempty"`
	Organization string   `json:"organization,omitempty"`
	Role         string   `json:"role,omitempty"`
	Scopes       []string `json:"scopes,omitempty"`
	// TokenPrefix identifies which credential this is, without being one.
	TokenPrefix string `json:"token_prefix,omitempty"`
	ExpiresAt   string `json:"expires_at,omitempty"`
	StoredIn    string `json:"credential_stored_in,omitempty"`
	// Providers is the model provider keys and their monthly caps.
	Providers *providerSpendDoc `json:"model_providers,omitempty"`
	// CredentialNote states the invariant rather than leaving it to be
	// trusted.
	CredentialNote string `json:"credential_note"`
}

type providerSpendDoc struct {
	Sealing bool `json:"can_store_keys"`
	// Unavailable says why this could not be read. An absent listing is never
	// reported as an empty one.
	Unavailable string              `json:"unavailable,omitempty"`
	Keys        []providerKeyDoc    `json:"keys"`
	Budgets     []providerBudgetDoc `json:"budgets"`
}

type providerKeyDoc struct {
	Provider string `json:"provider"`
	// Last4 and Fingerprint identify the key. The key itself is not stored in
	// a form anything can read back, including the control plane's own screens.
	Last4       string `json:"last4,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"`
	RotatedAt   string `json:"rotated_at,omitempty"`
}

type providerBudgetDoc struct {
	Provider     string  `json:"provider"`
	Period       string  `json:"period,omitempty"`
	CapUSD       float64 `json:"cap_usd"`
	SpentUSD     float64 `json:"spent_usd"`
	RemainingUSD float64 `json:"remaining_usd"`
	// Exhausted reports that this provider can spend nothing more this month,
	// which is the state a run is refused in.
	Exhausted bool `json:"exhausted"`
}

const credentialNote = "No tool on this server reads, returns, creates or removes a " +
	"credential. This result carries a token prefix and, for a model provider key, the " +
	"last four characters and a fingerprint, which is what the control plane publishes " +
	"to identify them. Signing in, signing out, and storing or removing any key are done " +
	"by a person at a terminal, and there is no argument here that would do them."

func newDescribeAccountTool(p *Project, read readAccount) *Tool {
	return &Tool{
		Name:     "describe_control_plane_account",
		Title:    "Who this machine is signed in as",
		ReadOnly: true,
		Description: "Report who this machine is signed in to a control plane as, which " +
			"organization, what the credential is allowed to do, and when it expires. Call " +
			"it when something is refused and the reason might be a missing capability or a " +
			"lapsed sign in, rather than guessing which. It ASKS the control plane rather " +
			"than reading the copy on disk, because a credential whose membership was " +
			"revoked still looks perfectly good locally and reporting it would say somebody " +
			"has access they do not have. Not signed in is a normal answer: most of this " +
			"product works with no control plane at all. It can also report the model " +
			"provider keys stored there and what each may still spend this month, which is " +
			"the cap a run is refused against. It never reveals a credential or a key, and " +
			"nothing here can sign in, sign out, store a key or change a spending cap.",
		Input: &Schema{
			Type:     "object",
			Required: []string{"project_id"},
			Properties: map[string]*Schema{
				"project_id": projectIDSchema(),
				"include_model_providers": {
					Type: "boolean",
					Description: "Optional, false by default. Also report the model provider " +
						"keys stored on the control plane and their monthly caps. It is a " +
						"second request and needs a credential carrying the capability to " +
						"read them, so ask for it when a spending cap is the question.",
				},
			},
		},
		Handler: func(ctx context.Context, _ *Call, args map[string]any) (any, *Fault) {
			if fault := p.checkAssertion(args); fault != nil {
				return nil, fault
			}
			includeProviders, _ := args["include_model_providers"].(bool)

			account, err := read(ctx, includeProviders)
			if err != nil {
				return nil, &Fault{
					Code: FaultSafetyUnavailable,
					Detail: "The control plane could not be asked, so this says nothing " +
						"about who this machine is. It is reachable over the network or it " +
						"is not; the server log says which failed.",
					Retryable: true, wrapped: err,
				}
			}
			return describeAccount(account), nil
		},
	}
}

func describeAccount(a Account) accountResult {
	out := accountResult{
		Kind: "control_plane_account", SignedIn: a.SignedIn,
		CredentialNote: credentialNote,
	}
	out.ControlPlane = safeHostURL(a.ControlPlane)
	if out.ControlPlane == "" {
		out.ControlPlane = safeText(a.ControlPlane, 200)
	}
	if !a.SignedIn {
		out.Summary = fmt.Sprintf(
			"This machine is not signed in to %s. That is normal and most of this product "+
				"works without it: what needs it is reading an environment's record, and "+
				"the model provider keys and their spending caps. Somebody has to run "+
				"af login at a terminal; nothing here can.", orNotRecorded(out.ControlPlane))
		return out
	}

	out.Login = safeText(a.Login, 120)
	out.Name = safeText(a.Name, 200)
	out.Organization = safeText(a.Organization, 200)
	out.Role = safeText(a.Role, 60)
	out.TokenPrefix = safeText(a.TokenPrefix, 32)
	out.ExpiresAt = safeText(a.ExpiresAt, 40)
	out.StoredIn = safeText(a.StoredIn, 200)
	for i, s := range a.Scopes {
		if i >= 40 {
			break
		}
		safe, _ := safeIdentifier(s)
		out.Scopes = append(out.Scopes, safe)
	}
	sort.Strings(out.Scopes)

	if a.Providers != nil {
		doc := describeProviderSpend(*a.Providers)
		out.Providers = &doc
	}
	out.Summary = accountSummary(out)
	return out
}

func describeProviderSpend(s ProviderSpend) providerSpendDoc {
	doc := providerSpendDoc{
		Sealing: s.Sealing, Unavailable: safeText(s.Unavailable, 300),
		Keys: []providerKeyDoc{}, Budgets: []providerBudgetDoc{},
	}
	for i, k := range s.Keys {
		if i >= 20 {
			break
		}
		entry := providerKeyDoc{
			Last4: safeText(k.Last4, 8), Fingerprint: safeText(k.Fingerprint, 64),
			CreatedAt: safeText(k.CreatedAt, 40), RotatedAt: safeText(k.RotatedAt, 40),
		}
		entry.Provider, _ = safeIdentifier(k.Provider)
		doc.Keys = append(doc.Keys, entry)
	}
	for i, b := range s.Budgets {
		if i >= 20 {
			break
		}
		entry := providerBudgetDoc{
			Period: safeText(b.Period, 40), CapUSD: b.CapUSD,
			SpentUSD: b.SpentUSD, RemainingUSD: b.RemainingUSD,
			Exhausted: b.RemainingUSD <= 0,
		}
		entry.Provider, _ = safeIdentifier(b.Provider)
		doc.Budgets = append(doc.Budgets, entry)
	}
	return doc
}

func accountSummary(out accountResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Signed in to %s as %s in %s",
		orNotRecorded(out.ControlPlane), orNotRecorded(out.Login),
		orNotRecorded(out.Organization))
	if out.Role != "" {
		fmt.Fprintf(&b, ", role %s", out.Role)
	}
	b.WriteString(". ")
	if len(out.Scopes) > 0 {
		fmt.Fprintf(&b, "The credential carries %s. ", clip(strings.Join(out.Scopes, ", "), 300))
	}
	if out.ExpiresAt != "" {
		fmt.Fprintf(&b, "It expires at %s. ", out.ExpiresAt)
	}
	if out.Providers == nil {
		return strings.TrimSpace(b.String())
	}
	switch {
	case out.Providers.Unavailable != "":
		fmt.Fprintf(&b, "The model provider keys could not be read, so nothing here says "+
			"what may be spent: %s", out.Providers.Unavailable)
	case !out.Providers.Sealing:
		b.WriteString("This control plane has no sealing secret, so it cannot store a " +
			"model provider key at all.")
	case len(out.Providers.Keys) == 0:
		b.WriteString("No model provider key is stored there, so a run that needs one is " +
			"refused rather than falling back to somebody else's key.")
	default:
		exhausted := 0
		for _, budget := range out.Providers.Budgets {
			if budget.Exhausted {
				exhausted++
			}
		}
		fmt.Fprintf(&b, "%d model provider %s stored. ", len(out.Providers.Keys),
			plural(len(out.Providers.Keys), "key is", "keys are"))
		if exhausted > 0 {
			fmt.Fprintf(&b, "%d of them can spend nothing more this month, and a run "+
				"needing one is refused before the key is even decrypted. ", exhausted)
		}
		b.WriteString("A cap is set by a person at a terminal or in the console; nothing " +
			"on this server can raise one.")
	}
	return strings.TrimSpace(b.String())
}

// ---------------------------------------------------------------------------
// the adapter
// ---------------------------------------------------------------------------

// account asks the control plane who this machine is.
//
// It asks rather than reading the stored copy, for the reason af whoami gives:
// the stored copy is what this machine believed at login time, and a
// credential whose membership has been removed still looks perfectly good on
// disk. Reporting it would tell somebody they have access they do not have.
func (f *orchestratorFactory) account(ctx context.Context, includeProviders bool) (Account, error) {
	origin := f.controlPlaneOrigin()
	out := Account{ControlPlane: origin}

	store := auth.NewStore()
	cred, err := store.Load(origin)
	switch {
	case errors.Is(err, auth.ErrNotSignedIn):
		return out, nil
	case err != nil:
		return out, err
	case cred.Expired(f.cfg.Clock.Now()):
		// An expired credential is reported as not signed in rather than as a
		// failure, because it is the same state from a caller's point of view
		// and the next step is the same command.
		return out, nil
	}

	client := auth.NewClient(origin)
	identity, err := client.Whoami(ctx, cred.Token)
	if errors.Is(err, auth.ErrNotSignedIn) {
		// The control plane no longer accepts it. Not signed in, which is the
		// truth, rather than the stored copy's optimistic answer.
		return out, nil
	}
	if err != nil {
		return out, err
	}

	// The credential this call is holding, registered so that it is replaced
	// wherever it appears in what follows. An engine token has no recognisable
	// prefix, so the pattern rules alone cannot find one.
	scrub := scrubberFor(cred.Token).String

	out.SignedIn = true
	out.Login, out.Name = scrub(identity.Login), scrub(identity.Name)
	out.Organization, out.Role = scrub(identity.Organization), scrub(identity.Role)
	out.TokenPrefix = scrub(identity.TokenPrefix)
	out.Scopes = identity.Scopes
	out.ExpiresAt = identity.ExpiresAt
	out.StoredIn = scrub(store.Location(origin))
	if out.ExpiresAt == "" && !cred.ExpiresAt.IsZero() {
		out.ExpiresAt = cred.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z07:00")
	}

	if !includeProviders {
		return out, nil
	}
	spend := ProviderSpend{}
	got, err := client.ListProviders(ctx, cred.Token)
	if err != nil {
		// Reported as unavailable on an otherwise good answer, rather than
		// failing the whole call. A credential without the capability to read
		// keys is a perfectly ordinary credential, and refusing to say who
		// somebody is because of it would be the wrong trade.
		switch {
		case errors.Is(err, auth.ErrScopeMissing):
			spend.Unavailable = "this credential does not carry the capability to read " +
				"model provider keys. Somebody has to sign in again asking for it, at a " +
				"terminal."
		default:
			spend.Unavailable = "the control plane did not answer with the stored keys. " +
				"The server log says why."
		}
		out.Providers = &spend
		return out, nil
	}

	spend.Sealing = got.Sealing
	for _, k := range got.Keys {
		spend.Keys = append(spend.Keys, ProviderKeyState{
			Provider: k.Provider, Last4: k.Last4, Fingerprint: k.Fingerprint,
			CreatedAt: k.CreatedAt, RotatedAt: k.RotatedAt,
		})
	}
	for _, b := range got.Budgets {
		spend.Budgets = append(spend.Budgets, ProviderBudgetState{
			Provider: b.Provider, Period: b.Period, CapUSD: b.CapUSD,
			SpentUSD: b.SpentUSD, RemainingUSD: b.RemainingUSD,
		})
	}
	out.Providers = &spend
	return out, nil
}
