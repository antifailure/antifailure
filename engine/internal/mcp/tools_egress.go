package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/antifailure/antifailure/engine/internal/egress"
	"github.com/antifailure/antifailure/engine/internal/policy"
	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/internal/runtime/local"
)

// observeDecisions reads the sidecar's decision log for the bound project.
//
// It returns the log and whether observation was possible at all. The second
// return is the important one: an empty log and an unreachable sidecar look
// identical from the caller's side and mean opposite things, and confusing
// them would report "nothing left the environment" for an environment nobody
// was watching.
type observeDecisions func(ctx context.Context, limit int) (decisions []local.Decision, available bool, err error)

// newInspectEgressTool builds inspect_egress_firewall.
//
// Synchronous and read only. It observes what already happened rather than
// running an experiment, so there is nothing to submit and nothing to poll.
func newInspectEgressTool(p *Project, observe observeDecisions) *Tool {
	return &Tool{
		Name:     "inspect_egress_firewall",
		Title:    "Inspect the egress firewall",
		ReadOnly: true,
		Description: "Report what the environment is allowed to reach, what it actually " +
			"reached, and whether containment held. It answers the question a summary of " +
			"traffic cannot: for each call to a third party under a sandbox rule, whether " +
			"the credential was really swapped for a sandbox one on the way out, or " +
			"whether the application's own credential left the environment. " +
			"Optionally give it requests to ask about and it will say what the policy " +
			"would do with each, without needing an environment to be running. " +
			"The policy itself comes from the project's manifest and cannot be changed " +
			"from here.",
		Input: &Schema{
			Type: "object",
			Properties: map[string]*Schema{
				"project_id": projectIDSchema(),
				"probe": {
					Type: "array", MaxItems: 20,
					Description: "Optional. Requests to ask the policy about, as method and " +
						"URL. Asking is free and needs no running environment.",
					Items: &Schema{
						Type:     "object",
						Required: []string{"method", "url"},
						Properties: map[string]*Schema{
							"method": {
								Type: "string", MaxLength: 16, MinLength: 1,
								Pattern:     `[A-Za-z]+`,
								Description: "The HTTP method, such as GET or POST.",
							},
							"url": {
								Type: "string", MaxLength: 2048, MinLength: 1,
								Description: "The URL the environment would request.",
							},
						},
					},
				},
				"observed_limit": {
					Type: "integer", HasMin: true, Minimum: 1, HasMax: true, Maximum: 2000,
					Description: "Optional. How many recent decisions to read. Defaults to 500.",
				},
			},
		},
		Handler: func(ctx context.Context, _ *Call, args map[string]any) (any, *Fault) {
			if fault := p.checkAssertion(args); fault != nil {
				return nil, fault
			}
			return inspectEgress(ctx, p, observe, args)
		},
	}
}

// egressResult is what inspect_egress_firewall returns.
//
// It is shaped like Result so that a caller reads a verdict, a summary and
// bounded findings in the same order it does everywhere else, with the
// egress specific detail underneath.
type egressResult struct {
	Kind    string  `json:"kind"`
	Verdict Verdict `json:"verdict"`
	Summary string  `json:"summary"`
	// Observed says whether the decision log could be read at all. When it is
	// false every count below is absent rather than zero, because a zero
	// nobody measured is the most dangerous number this tool could print.
	Observed     bool             `json:"observed"`
	Unavailable  string           `json:"unavailable,omitempty"`
	Findings     FindingPage      `json:"findings"`
	Policy       egressPolicyDoc  `json:"declared_policy"`
	Containment  *containmentDoc  `json:"containment,omitempty"`
	Probes       []probeResultDoc `json:"probes,omitempty"`
	Metrics      []Metric         `json:"metrics,omitempty"`
	EvidenceNote string           `json:"evidence_note,omitempty"`
}

type egressPolicyDoc struct {
	Default   string          `json:"default"`
	AllowIPv6 bool            `json:"allow_ipv6"`
	Rules     []egressRuleDoc `json:"rules"`
	Total     int             `json:"total_rules"`
	Truncated bool            `json:"truncated"`
	Note      string          `json:"note,omitempty"`
}

type egressRuleDoc struct {
	Host       string   `json:"host"`
	Mode       string   `json:"mode"`
	Paths      []string `json:"paths,omitempty"`
	Methods    []string `json:"methods,omitempty"`
	Credential string   `json:"credential,omitempty"`
	RateLimit  string   `json:"rate_limit,omitempty"`
}

type containmentDoc struct {
	Decisions   int `json:"decisions_read"`
	Allowed     int `json:"allowed"`
	Refused     int `json:"refused"`
	Captured    int `json:"captured"`
	Mocked      int `json:"mocked"`
	Sandbox     int `json:"sandbox_calls"`
	Substituted int `json:"credential_substituted"`
	// Unsubstituted is the count this tool exists to report.
	Unsubstituted int       `json:"sandbox_credential_not_substituted"`
	HostOnly      int       `json:"decided_on_host_only"`
	RateLimited   int       `json:"rate_limited"`
	HeldMs        int64     `json:"rate_limit_held_ms"`
	Hosts         []hostDoc `json:"hosts"`
	HostsTotal    int       `json:"hosts_total"`
	HostsShown    int       `json:"hosts_shown"`
	Truncated     bool      `json:"hosts_truncated"`
	Note          string    `json:"note,omitempty"`
}

type hostDoc struct {
	Host          string   `json:"host"`
	Modes         []string `json:"modes"`
	Requests      int      `json:"requests"`
	Allowed       int      `json:"allowed"`
	Refused       int      `json:"refused"`
	Substituted   int      `json:"credential_substituted,omitempty"`
	Unsubstituted int      `json:"credential_not_substituted,omitempty"`
	Declared      bool     `json:"declared_in_manifest"`
}

type probeResultDoc struct {
	Request    string `json:"request"`
	Mode       string `json:"mode"`
	Allowed    bool   `json:"allowed"`
	Rule       string `json:"rule,omitempty"`
	Reason     string `json:"reason"`
	Credential string `json:"credential,omitempty"`
	Error      string `json:"error,omitempty"`
}

// maxRulesReported and maxHostsReported bound the two lists that grow with the
// project rather than with the run.
const (
	maxRulesReported = 60
	maxHostsReported = 40
)

func inspectEgress(
	ctx context.Context, p *Project, observe observeDecisions, args map[string]any,
) (any, *Fault) {
	eng, err := policy.New(p.Manifest.Egress)
	if err != nil {
		// A manifest that validates but will not compile is a defect in the
		// validator, and the safety subsystem is therefore not established.
		return nil, &Fault{
			Code: FaultSafetyUnavailable,
			Detail: "The project's egress policy did not compile, so this server cannot " +
				"say what the environment is allowed to reach.",
			wrapped: err,
		}
	}

	out := egressResult{Kind: "egress_inspection", Policy: describePolicy(eng)}

	probes, fault := runProbes(eng, args)
	if fault != nil {
		return nil, fault
	}
	out.Probes = probes

	limit := 500
	if raw, ok := args["observed_limit"]; ok {
		if n, err := toInt(raw); err == nil {
			limit = n
		}
	}

	decisions, available, obsErr := observe(ctx, limit)
	out.Observed = available
	var findings []report.Finding

	if !available {
		// Fail closed. The tool's central question is about what actually
		// happened, and nothing can answer it right now. Reporting PASS here,
		// or reporting zero requests as though that were a measurement, would
		// be a monitoring failure presented as a clean bill of health.
		out.Verdict = VerdictInconclusive
		out.Unavailable = unavailableReason(obsErr)
		out.Summary = "The decision log could not be read, so this says nothing about what " +
			"the environment reached. The declared policy below is what the manifest asks " +
			"for, not evidence that it was enforced. " + out.Unavailable
		out.Findings = boundFindings(nil)
		return out, nil
	}

	c := egress.Observe(decisions)
	doc := describeContainment(c)
	out.Containment = &doc

	if f := egress.CredentialFinding(c); f != nil {
		findings = append(findings, *f)
	}
	// The same surprise detection af ci uses, ranked by the same manifest
	// policy, so the two cannot disagree about what counts as a surprise.
	if f := egress.Finding(egress.Summarise(decisions), p.Gate); f != nil {
		findings = append(findings, *f)
	}
	out.Findings = boundFindings(findings)
	out.Metrics = egressMetrics(c)

	// The verdict is the report package's own counting rule applied to the
	// findings, and every level in them came from the manifest. Nothing here
	// invents a threshold.
	fail, warn := report.Run{Findings: findings}.Counts()
	switch {
	case fail > 0:
		out.Verdict = VerdictFail
	default:
		out.Verdict = VerdictPass
	}
	out.Summary = egressSummary(c, fail, warn, len(decisions))

	if c.Total > 0 {
		out.EvidenceNote = "The full decision log is on the environment. Read it with " +
			"af net log -o json, which now carries the substitution and rate limit fields " +
			"this summary is built from."
	}
	return out, nil
}

// unavailableReason turns an observation failure into one sentence, without
// letting an engine error string reach the caller verbatim.
func unavailableReason(err error) string {
	if err == nil {
		return "No environment is running for this branch, so there is no decision log yet. " +
			"Bring one up with af up."
	}
	return "The environment's decision log could not be read. The server log says why."
}

func describePolicy(eng *policy.Engine) egressPolicyDoc {
	rules := eng.Rules()
	doc := egressPolicyDoc{
		Default: string(eng.Default()), AllowIPv6: eng.AllowsIPv6(), Total: len(rules),
	}
	shown := rules
	if len(shown) > maxRulesReported {
		shown = shown[:maxRulesReported]
		doc.Truncated = true
		doc.Note = fmt.Sprintf(
			"This project declares %d rules and the %d most specific are shown. "+
				"They are listed in the order that decides, so the ones withheld are the "+
				"least specific. Use af net policy for the whole list.",
			len(rules), maxRulesReported)
	}
	for _, r := range shown {
		doc.Rules = append(doc.Rules, egressRuleDoc{
			Host: r.Host, Mode: string(r.Mode), Paths: r.Paths, Methods: r.Methods,
			Credential: r.Credential, RateLimit: r.RateLimit,
		})
	}
	if doc.Rules == nil {
		doc.Rules = []egressRuleDoc{}
	}
	return doc
}

func describeContainment(c egress.Containment) containmentDoc {
	doc := containmentDoc{
		Decisions: c.Total, Allowed: c.Allowed, Refused: c.Refused,
		Captured: c.Captured, Mocked: c.Mocked, Sandbox: c.Sandbox,
		Substituted: c.Substituted, Unsubstituted: c.SandboxUnsubstituted,
		HostOnly: c.HostOnly, RateLimited: c.RateLimited, HeldMs: c.WaitedMs,
		HostsTotal: len(c.Hosts),
	}
	hosts := c.Hosts
	if len(hosts) > maxHostsReported {
		hosts = hosts[:maxHostsReported]
		doc.Truncated = true
		doc.Note = fmt.Sprintf(
			"%d hosts were reached and the %d most notable are shown, worst first: any "+
				"host that carried an unreplaced credential comes first, then any the "+
				"manifest does not declare.", len(c.Hosts), maxHostsReported)
	}
	for _, h := range hosts {
		doc.Hosts = append(doc.Hosts, hostDoc{
			Host: h.Host, Modes: h.Modes, Requests: h.Requests,
			Allowed: h.Allowed, Refused: h.Refused,
			Substituted: h.Substituted, Unsubstituted: h.Unsubstituted,
			Declared: h.Declared,
		})
	}
	doc.HostsShown = len(doc.Hosts)
	if doc.Hosts == nil {
		doc.Hosts = []hostDoc{}
	}
	return doc
}

func egressMetrics(c egress.Containment) []Metric {
	zero := 0.0
	return []Metric{
		{
			Name: "sandbox_credential_not_substituted", Value: float64(c.SandboxUnsubstituted),
			Unit: "requests", Threshold: &zero, Breached: c.SandboxUnsubstituted > 0,
		},
		{Name: "requests_allowed_out", Value: float64(c.Allowed), Unit: "requests"},
		{Name: "requests_refused", Value: float64(c.Refused), Unit: "requests"},
		{Name: "decided_on_host_only", Value: float64(c.HostOnly), Unit: "requests"},
		{Name: "rate_limit_held", Value: float64(c.WaitedMs), Unit: "ms"},
	}
}

func egressSummary(c egress.Containment, fail, warn, read int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Read %d decisions: %d allowed out, %d refused, %d captured, %d mocked. ",
		read, c.Allowed, c.Refused, c.Captured, c.Mocked)
	switch {
	case c.SandboxUnsubstituted > 0:
		fmt.Fprintf(&b,
			"%d of the %d sandbox calls left without the credential being replaced, so the "+
				"application's own credential reached the provider. ",
			c.SandboxUnsubstituted, c.Sandbox)
	case c.Sandbox > 0:
		fmt.Fprintf(&b, "All %d sandbox calls had the credential replaced on the way out. ",
			c.Sandbox)
	}
	if c.HostOnly > 0 {
		fmt.Fprintf(&b,
			"%d decisions were made on the host alone, without seeing the path or method, "+
				"so a rule naming paths could only half apply. ", c.HostOnly)
	}
	if fail == 0 && warn == 0 {
		b.WriteString("Nothing the project's policy treats as a problem.")
	} else {
		fmt.Fprintf(&b, "%d findings stop a merge and %d are reported only.", fail, warn)
	}
	return b.String()
}

// runProbes asks the policy about each request the caller named.
//
// A probe that will not parse is reported as a probe with an error rather than
// failing the whole call, because a caller asking about six requests should
// get five answers and one complaint, not nothing.
func runProbes(eng *policy.Engine, args map[string]any) ([]probeResultDoc, *Fault) {
	raw, present := args["probe"]
	if !present {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fieldFault(FaultInvalidArgument, "probe", "This field must be an array.")
	}
	out := make([]probeResultDoc, 0, len(list))
	for i, item := range list {
		obj, ok := item.(map[string]any)
		if !ok {
			return nil, fieldFault(FaultInvalidArgument, fmt.Sprintf("probe[%d]", i),
				"This element must be an object.")
		}
		method, _ := obj["method"].(string)
		rawURL, _ := obj["url"].(string)

		req, err := policy.ParseRequest(method, rawURL)
		if err != nil {
			out = append(out, probeResultDoc{
				Request: clip(strings.ToUpper(method)+" "+rawURL, 200),
				Error:   "This is not a request the policy can be asked about.",
			})
			continue
		}
		d := eng.Evaluate(req)
		out = append(out, probeResultDoc{
			Request: req.String(), Mode: string(d.Mode), Allowed: d.Allowed(),
			Rule: d.RuleHost, Reason: clip(d.Reason(), 400), Credential: d.Credential,
		})
	}
	return out, nil
}

// toInt reads a JSON number that validation already bounded.
func toInt(raw any) (int, error) {
	type intish interface{ Int64() (int64, error) }
	n, ok := raw.(intish)
	if !ok {
		return 0, fmt.Errorf("not a number")
	}
	v, err := n.Int64()
	return int(v), err
}
