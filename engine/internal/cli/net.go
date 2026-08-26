package cli

import (
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/internal/policy"
	"github.com/antifailure/antifailure/engine/internal/runtime/local"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The network commands answer questions about the policy without needing an
// environment to exist. That matters more than it sounds: the moment somebody
// most wants to know why a host is blocked is before they have spent four
// minutes building one, and a question that can only be asked of a running
// system is a question most people never ask.

// PolicyRuleJSON is one rule as af net policy reports it.
type PolicyRuleJSON struct {
	Host        string   `json:"host"`
	Mode        string   `json:"mode"`
	Paths       []string `json:"paths,omitempty"`
	Methods     []string `json:"methods,omitempty"`
	RateLimit   string   `json:"rate_limit,omitempty"`
	Credential  string   `json:"credential,omitempty"`
	Fixtures    string   `json:"fixtures,omitempty"`
	WebhookPath string   `json:"webhook_path,omitempty"`
	Note        string   `json:"note,omitempty"`
}

// PolicyJSON is the machine readable form of af net policy.
type PolicyJSON struct {
	Default   string           `json:"default"`
	AllowIPv6 bool             `json:"allow_ipv6"`
	Rules     []PolicyRuleJSON `json:"rules"`
}

// ExplainMatchJSON is one rule that matched, in the order that decides.
type ExplainMatchJSON struct {
	Host        string `json:"host"`
	Mode        string `json:"mode"`
	Specificity int    `json:"specificity"`
	Why         string `json:"why"`
	Winner      bool   `json:"winner"`
}

// ExplainJSON is the machine readable form of af net explain.
type ExplainJSON struct {
	Request     string             `json:"request"`
	Mode        string             `json:"mode"`
	Allowed     bool               `json:"allowed"`
	Rule        string             `json:"rule,omitempty"`
	Reason      string             `json:"reason"`
	RateLimit   string             `json:"rate_limit,omitempty"`
	Credential  string             `json:"credential,omitempty"`
	Fixtures    string             `json:"fixtures,omitempty"`
	WebhookPath string             `json:"webhook_path,omitempty"`
	Matched     []ExplainMatchJSON `json:"matched"`
}

// loadPolicy reads the manifest and compiles its egress section.
func loadPolicy(env *Env) (*schema.Manifest, *policy.Engine, error) {
	path, err := manifest.Find(env.WorkDir)
	if err != nil {
		return nil, nil, err
	}
	m, err := manifest.Load(path)
	if err != nil {
		return nil, nil, err
	}
	eng, err := policy.New(m.Egress)
	if err != nil {
		// A manifest that validates but will not compile is a bug in the
		// validator, not in the user's file, so it is reported as one rather
		// than as a configuration error they are supposed to fix.
		return nil, nil, aferrors.Wrap(err, aferrors.AFMAN002, "detail", err.Error())
	}
	return m, eng, nil
}

func newNetCommand(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "net",
		Short: "Inspect and explain the environment's network policy",
		Long: strings.TrimSpace(`
An environment reaches nothing on the network except the hosts in the manifest,
each in the mode named there. These commands say what that adds up to, without
needing an environment to be running.`),
	}
	cmd.AddCommand(newNetPolicyCommand(env))
	cmd.AddCommand(newNetExplainCommand(env))
	cmd.AddCommand(newNetLogCommand(env))
	return cmd
}

func newNetPolicyCommand(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "policy",
		Short: "Show the effective policy, in the order that decides",
		Long: strings.TrimSpace(`
Rules are printed most specific first, which is the order they are evaluated
in. An exact host beats a wildcard, a longer path beats a shorter one, and an
explicit method beats any, so where a rule sits in this list is where it sits
in the decision, no matter where it sits in the file.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, eng, err := loadPolicy(env)
			if err != nil {
				return err
			}
			rules := eng.Rules()

			if env.Out.Format == FormatJSON {
				doc := PolicyJSON{
					Default:   string(eng.Default()),
					AllowIPv6: eng.AllowsIPv6(),
					Rules:     make([]PolicyRuleJSON, 0, len(rules)),
				}
				for _, r := range rules {
					doc.Rules = append(doc.Rules, PolicyRuleJSON{
						Host: r.Host, Mode: string(r.Mode), Paths: r.Paths,
						Methods: r.Methods, RateLimit: r.RateLimit,
						Credential: r.Credential, Fixtures: r.Fixtures,
						WebhookPath: r.WebhookPath, Note: r.Note,
					})
				}
				return env.Out.JSON(doc)
			}

			env.Out.Section("Network policy")
			env.Out.Println(env.Out.Wrap(fmt.Sprintf(
				"Everything the environment sends goes through the proxy. Anything not listed below is %s.",
				modeVerb(eng.Default())), 0))
			if !eng.AllowsIPv6() {
				env.Out.Println(env.Out.Wrap(
					"IPv6 is off, so a host that resolves only to an IPv6 address is refused rather than "+
						"connected to directly, which is the usual way an egress control is defeated.", 0))
			}
			env.Out.Println("")

			if len(rules) == 0 {
				env.Out.Println("No rules. Every outbound request takes the default.")
				return nil
			}

			env.Out.Println(env.Out.Wrap(
				"Rules are listed in the order that decides. An exact host beats a wildcard and a "+
					"longer path beats a shorter one, so this is the order they are evaluated in, "+
					"whatever order the manifest lists them.", 0))
			env.Out.Println("")

			// A block per rule rather than a table. The note is the whole
			// value of this output and notes are sentences, so a column would
			// either truncate them or run past the terminal.
			const gutter = 11
			for _, r := range rules {
				env.Out.Printf("  %s%s\n",
					pad(env.Out.S(styleForMode(r.Mode), string(r.Mode)),
						gutter-2+len(env.Out.S(styleForMode(r.Mode), string(r.Mode)))-len(r.Mode)),
					env.Out.S(StyleBold, r.Host))
				if s := scopeOf(r); s != "" {
					env.Out.Printf("%s%s\n", strings.Repeat(" ", gutter), env.Out.S(StyleDim, s))
				}
				if r.Note != "" {
					env.Out.Printf("%s%s\n", strings.Repeat(" ", gutter),
						env.Out.Wrap(r.Note, gutter))
				}
				env.Out.Println("")
			}
			env.Out.Printf("Ask about one request with: af net explain GET https://%s/\n", rules[0].Host)
			return nil
		},
	}
}

// scopeOf describes what narrows a rule beyond its host, as prose.
//
// A bare "GET" under a host reads as a fragment nobody can interpret. The
// point of this line is that a reader knows without asking whether the rule
// covers the request they have in mind.
func scopeOf(r schema.EgressRule) string {
	var methods string
	if len(r.Methods) > 0 {
		ms := append([]string(nil), r.Methods...)
		for i := range ms {
			ms[i] = strings.ToUpper(ms[i])
		}
		sort.Strings(ms)
		methods = strings.Join(ms, " and ") + " requests"
	}
	var pathPart string
	if len(r.Paths) > 0 {
		ps := append([]string(nil), r.Paths...)
		sort.Strings(ps)
		pathPart = "paths under " + strings.Join(ps, " and ")
	}
	switch {
	case methods != "" && pathPart != "":
		return methods + " to " + pathPart
	case methods != "":
		return methods + " only"
	case pathPart != "":
		return pathPart + " only"
	default:
		return ""
	}
}

// modeVerb reads as a sentence, which "block" does not.
func modeVerb(m schema.Mode) string {
	switch m {
	case schema.ModeBlock:
		return "blocked"
	case schema.ModeAllow:
		return "allowed through"
	case schema.ModeCapture:
		return "captured into the inbox"
	case schema.ModeMock:
		return "answered from a mock"
	case schema.ModeSandbox:
		return "sent to the provider's sandbox"
	case schema.ModeSynth:
		return "answered by a model"
	default:
		return string(m)
	}
}

func styleForMode(m schema.Mode) Style {
	switch m {
	case schema.ModeAllow, schema.ModeSandbox:
		return StyleWarn
	case schema.ModeBlock:
		return StyleGood
	case schema.ModeSynth:
		return StyleBad
	default:
		return StyleDim
	}
}

func newNetExplainCommand(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "explain <method> <url>",
		Short: "Say what would happen to one request, and which rule decides it",
		Long: strings.TrimSpace(`
Prints the decision, the rule that made it, and every other rule that also
matched, so a surprising answer is diagnosable rather than mysterious.

  af net explain GET https://api.stripe.com/v1/charges
  af net explain POST https://api.resend.com/emails`),
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			req, err := parseRequest(args[0], args[1])
			if err != nil {
				return err
			}
			_, eng, err := loadPolicy(env)
			if err != nil {
				return err
			}
			d, chain := eng.Explain(req)

			if env.Out.Format == FormatJSON {
				doc := ExplainJSON{
					Request: req.String(), Mode: string(d.Mode), Allowed: d.Allowed(),
					Rule: d.RuleHost, Reason: d.Reason(), RateLimit: d.RateLimit,
					Credential: d.Credential, Fixtures: d.Fixtures, WebhookPath: d.WebhookPath,
					Matched: make([]ExplainMatchJSON, 0, len(chain)),
				}
				for i, m := range chain {
					doc.Matched = append(doc.Matched, ExplainMatchJSON{
						Host: m.Rule.Host, Mode: string(m.Rule.Mode),
						Specificity: m.Specificity, Why: m.Why, Winner: i == 0,
					})
				}
				return env.Out.JSON(doc)
			}

			env.Out.Section(req.String())
			env.Out.Println("")
			env.Out.Printf("  %s\n\n", env.Out.S(styleForMode(d.Mode), strings.ToUpper(string(d.Mode))))
			env.Out.Printf("  %s\n\n", env.Out.Wrap(d.Reason(), 2))

			var detail [][2]string
			if d.Credential != "" {
				detail = append(detail, [2]string{"Credential",
					d.Credential + " is substituted before the request leaves the environment."})
			}
			if d.RateLimit != "" {
				detail = append(detail, [2]string{"Rate limit", d.RateLimit})
			}
			if d.Fixtures != "" {
				detail = append(detail, [2]string{"Fixtures", d.Fixtures})
			}
			if d.WebhookPath != "" {
				detail = append(detail, [2]string{"Webhooks", "delivered to " + d.WebhookPath})
			}
			for _, kv := range detail {
				env.Out.Printf("  %s%s\n", pad(kv[0], 13), env.Out.Wrap(kv[1], 15))
			}
			if len(detail) > 0 {
				env.Out.Println("")
			}

			switch len(chain) {
			case 0:
				// The reason already says no rule matched. Repeating it here
				// would be the third sentence in a row saying one thing.
			case 1:
				env.Out.Println("  No other rule matches this request.")
			default:
				env.Out.Printf("  %s\n\n", env.Out.Wrap(fmt.Sprintf(
					"%d rules match this request. They are listed most specific first, and the "+
						"first one decides.", len(chain)), 2))
				for i, m := range chain {
					marker := "  "
					if i == 0 {
						marker = env.Out.S(StyleGood, "->")
					}
					env.Out.Printf("  %s %s%s\n", marker, pad(string(m.Rule.Mode), 9),
						env.Out.S(StyleBold, m.Rule.Host))
					env.Out.Printf("     %s%s\n", pad("", 9), env.Out.S(StyleDim, m.Why))
				}
			}
			return nil
		},
	}
}

// parseRequest turns a method and a URL from the command line into a request.
//
// It is strict about the URL because a typo that parses as a relative path
// would otherwise be explained against an empty host, and the answer would be
// confidently wrong rather than obviously wrong.
func parseRequest(method, raw string) (policy.Request, error) {
	var req policy.Request
	method = strings.ToUpper(strings.TrimSpace(method))
	if method == "" || strings.ContainsAny(method, " \t/:") {
		return req, aferrors.Coded(aferrors.AFNET002,
			"request", method+" "+raw, "detail", fmt.Sprintf("%q is not an HTTP method", method))
	}

	if !strings.Contains(raw, "://") {
		// A bare host is what people type, so accept it rather than refusing
		// on a technicality, and assume the scheme the environment mostly
		// speaks.
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return req, aferrors.Wrap(err, aferrors.AFNET002,
			"request", method+" "+raw, "detail", err.Error())
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return req, aferrors.Coded(aferrors.AFNET002,
			"request", method+" "+raw, "detail", fmt.Sprintf("the scheme %q is not http or https", u.Scheme))
	}
	if u.Hostname() == "" {
		return req, aferrors.Coded(aferrors.AFNET002,
			"request", method+" "+raw, "detail", fmt.Sprintf("%q names no host", raw))
	}

	req.Host = u.Hostname()
	req.Method = method
	req.TLS = u.Scheme == "https"
	req.Path = u.EscapedPath()
	if req.Path == "" {
		req.Path = "/"
	}
	if p := u.Port(); p != "" {
		n, convErr := strconv.Atoi(p)
		if convErr != nil || n <= 0 || n > 65535 {
			return req, aferrors.Coded(aferrors.AFNET002,
				"request", method+" "+raw, "detail", fmt.Sprintf("the port %q is not valid", p))
		}
		req.Port = n
	}
	return req, nil
}

// DecisionJSON is one line of af net log.
type DecisionJSON struct {
	At      string `json:"at"`
	Request string `json:"request"`
	Mode    string `json:"mode"`
	Rule    string `json:"rule,omitempty"`
	Allowed bool   `json:"allowed"`
	Status  int    `json:"status,omitempty"`
	Bytes   int64  `json:"bytes,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Error   string `json:"error,omitempty"`
}

func newNetLogCommand(env *Env) *cobra.Command {
	var limit int
	var blockedOnly bool
	var branch string
	cmd := &cobra.Command{
		Use:   "log",
		Short: "Show what the environment tried to reach, and what happened",
		Long: strings.TrimSpace(`
Every outbound request the environment made, allowed or refused, with the rule
that decided it.

The allowed ones are the point. A log of refusals answers "why was this
blocked"; a log of everything answers "did anything reach Stripe", which is the
question somebody asks after an incident.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o, err := orchestrator(env, branch, false)
			if err != nil {
				return err
			}
			decisions, err := o.Decisions(cmd.Context(), limit)
			if err != nil {
				return err
			}
			if blockedOnly {
				kept := decisions[:0]
				for _, d := range decisions {
					if !d.Allowed {
						kept = append(kept, d)
					}
				}
				decisions = kept
			}

			if env.Out.Format == FormatJSON {
				docs := make([]DecisionJSON, 0, len(decisions))
				for _, d := range decisions {
					docs = append(docs, DecisionJSON{
						At: d.AtRaw, Request: decisionRequest(d), Mode: d.Mode,
						Rule: d.Rule, Allowed: d.Allowed, Status: d.Status,
						Bytes: d.Bytes, Reason: d.Reason, Error: d.Error,
					})
				}
				return env.Out.JSON(docs)
			}

			if len(decisions) == 0 {
				env.Out.Println("Nothing has been decided yet. Bring the environment up with 'af up' and use it.")
				return nil
			}
			rows := make([][]string, 0, len(decisions))
			for _, d := range decisions {
				mark := env.Out.S(StyleGood, "block")
				if d.Allowed {
					mark = env.Out.S(StyleWarn, d.Mode)
				} else if d.Mode != "block" {
					mark = env.Out.S(StyleAccent, d.Mode)
				}
				rule := d.Rule
				if rule == "" {
					rule = env.Out.S(StyleDim, "(default)")
				}
				rows = append(rows, []string{
					shortTime(d.AtRaw), mark, decisionRequest(d), rule, outcomeOf(d),
				})
			}
			env.Out.Table([]string{"TIME", "MODE", "REQUEST", "RULE", "OUTCOME"}, rows)
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 200, "How many decisions to show, most recent last")
	cmd.Flags().BoolVar(&blockedOnly, "blocked", false, "Show only requests that were refused")
	cmd.Flags().StringVar(&branch, "branch", "", "Branch to read, defaulting to the checked out one")
	return cmd
}

func decisionRequest(d local.Decision) string {
	scheme := "http"
	if d.TLS {
		scheme = "https"
	}
	host := d.Host
	if d.Port != 0 && !((d.TLS && d.Port == 443) || (!d.TLS && d.Port == 80)) {
		host = net.JoinHostPort(host, strconv.Itoa(d.Port))
	}
	return fmt.Sprintf("%s %s://%s%s", d.Method, scheme, host, d.Path)
}

func outcomeOf(d local.Decision) string {
	switch {
	case d.Error != "":
		return d.Error
	case d.Status == 0:
		return ""
	case d.Bytes > 0:
		return fmt.Sprintf("%d, %s", d.Status, humanBytes(uint64(d.Bytes)))
	default:
		return strconv.Itoa(d.Status)
	}
}

// shortTime keeps the clock time and drops the date, because every line in one
// run shares the date and repeating it in every row buries the part that
// differs.
func shortTime(raw string) string {
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return raw
	}
	return t.Local().Format("15:04:05")
}
