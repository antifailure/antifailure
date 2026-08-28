// Package policy decides what happens to every outbound request.
//
// It is one pure function. Given the effective rules and a request, it returns
// a decision and the chain of rules that produced it. Nothing here does any
// I/O, which is what lets the same code answer three different questions with
// one implementation: the sidecar asks it per request, af net explain asks it
// about a hypothetical, and the control plane asks it to render a policy page.
// Three implementations would diverge, and the one that diverged would be the
// one deciding real traffic.
//
// Two rules govern evaluation and both are chosen for the same reason, which
// is that a policy nobody can predict is a policy nobody trusts.
//
// Specificity decides, not order. An exact host beats a wildcard, a longer
// path beats a shorter one, and an explicit method beats any. Order deciding
// would mean that appending a rule could silently change what an existing one
// does, and reviewing a policy would require reading it top to bottom every
// time.
//
// The default is block. Not because blocking is usually right, but because the
// cost of the two mistakes is not symmetric: a wrongly blocked request shows
// up immediately as a readable decision naming the host, and a wrongly allowed
// one shows up as a real customer receiving an email from a preview
// environment.
package policy

import (
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// Request is what the proxy asks about.
type Request struct {
	// Host is the destination, without a port unless the request named one.
	Host string
	// Port is the destination port. Zero means the scheme's default.
	Port int
	// Method is the HTTP method, uppercase.
	Method string
	// Path is the request path, beginning with a slash.
	Path string
	// TLS reports whether the connection is encrypted, which decides the
	// default port and appears in the decision for the audit trail.
	TLS bool
}

// String renders a request the way a decision log line does.
func (r Request) String() string {
	scheme := "http"
	if r.TLS {
		scheme = "https"
	}
	host := r.Host
	// The port is part of the identity unless it is the default one for the
	// scheme, where including it would make the same request read as two.
	if r.Port != 0 && (!r.TLS || r.Port != 443) && (r.TLS || r.Port != 80) {
		host = net.JoinHostPort(host, strconv.Itoa(r.Port))
	}
	method := r.Method
	if method == "" {
		method = "GET"
	}
	return fmt.Sprintf("%s %s://%s%s", method, scheme, host, r.Path)
}

// Decision is what to do with a request.
//
// It is deliberately cheap to produce: the fields are copied from the winning
// rule and nothing is formatted. The proxy makes one of these per outbound
// request, so anything allocated here is allocated on the hot path.
type Decision struct {
	// Mode is the action to take.
	Mode schema.Mode
	// RuleHost is the host pattern of the rule that decided. It is empty when
	// no rule matched and the default decided.
	//
	// The rule is reported by value rather than by pointer because a pointer
	// would alias the engine's own compiled rule, and a caller that wrote
	// through it would silently change what every later request decides.
	RuleHost string
	// RateLimit is the rule's limit, when it has one.
	RateLimit string
	// Credential names the variable holding the sandbox credential.
	Credential string
	// Fixtures is the mock fixture path.
	Fixtures string
	// WebhookPath is where a sandbox or mock delivers inbound callbacks.
	WebhookPath string

	// why records what matched. It is a value rather than a sentence because
	// building the sentence allocates, and Evaluate runs on every outbound
	// request while the sentence is read only when something is printed.
	why  why
	host string
	note string
}

// Matched reports whether a rule decided, rather than the default.
func (d Decision) Matched() bool { return d.RuleHost != "" }

// Allowed reports whether the request reaches the real destination.
//
// Only two modes do. Capture and mock answer without leaving the environment,
// and synth answers from a model, so a workflow that touched one is reported
// unverified rather than passed.
func (d Decision) Allowed() bool {
	return d.Mode == schema.ModeAllow || d.Mode == schema.ModeSandbox
}

// Reason is one sentence explaining the decision, in the second person where
// it names something the user can change.
//
// It is a method rather than a field because formatting it costs an allocation
// and most decisions are never printed. A blocked request is printed, and a
// blocked request is exactly the one somebody needs a sentence for.
func (d Decision) Reason() string {
	if d.RuleHost == "" {
		if d.Mode == schema.ModeBlock {
			return fmt.Sprintf(
				"No rule matches %s, and the default is block. Add an egress rule for it if the environment should reach it.",
				d.host)
		}
		return fmt.Sprintf("No rule matches %s, so the default of %s applies.", d.host, d.Mode)
	}
	base := fmt.Sprintf("The rule for %s decided %s because %s.", d.RuleHost, d.Mode, d.why.String())
	switch {
	case d.note != "":
		return base + " " + d.note
	case d.Mode == schema.ModeSynth:
		return base + " A workflow that touches a synthesized response reports unverified rather than passed."
	}
	return base
}

// why records which parts of a rule matched a request.
//
// Every field is either a small value or a string that already exists inside
// the compiled rule, so filling one in costs nothing. String is what turns it
// into prose, and only a decision somebody reads ever calls it.
type why struct {
	host   hostMatch
	suffix string
	path   string
	method bool
}

type hostMatch uint8

const (
	hostAny hostMatch = iota
	hostExactly
	hostAddress
	hostSuffixed
)

func (w why) String() string {
	var b strings.Builder
	switch w.host {
	case hostAny:
		b.WriteString("it matches every host")
	case hostExactly:
		b.WriteString("the host matches exactly")
	case hostAddress:
		b.WriteString("the address matches")
	case hostSuffixed:
		b.WriteString("the host ends in ")
		b.WriteString(w.suffix)
	}
	if w.method {
		b.WriteString(" and the method is listed")
	}
	if w.path != "" {
		b.WriteString(" and the path is under ")
		b.WriteString(w.path)
	}
	return b.String()
}

// Match is one rule that matched, with why it ranked where it did.
type Match struct {
	Rule schema.EgressRule
	// Index is the rule's position in the manifest, which breaks ties.
	Index int
	// Specificity is the computed rank; higher wins.
	Specificity int
	// Why explains what matched, for the explain output.
	Why string
}

// Engine evaluates requests against a compiled rule set.
//
// Compilation happens once, at environment start, so that per request work is
// a scan over a small sorted slice rather than a glob compile. Evaluate runs
// on every outbound call the application makes, so the cost here is the cost
// of the application's whole network life.
type Engine struct {
	rules     []compiled
	fallback  schema.Mode
	allowIPv6 bool
}

type compiled struct {
	rule  schema.EgressRule
	index int
	// hostExact is set when the rule names a host with no wildcard.
	hostExact string
	// hostSuffix is set for a wildcard rule, and is the part after the star,
	// including the leading dot.
	hostSuffix string
	// matchAll is set for the rule that matches every host.
	matchAll bool
	// ip is set when the rule names an address rather than a name.
	ip net.IP
	// port restricts the rule to one port. Zero means any.
	port int
	// paths are the path prefixes, longest first.
	paths []string
	// methods are the uppercase methods, or empty for any.
	methods map[string]bool
	// specificity ranks the rule before any request arrives.
	specificity int
}

// New compiles an egress section into an engine.
//
// A rule that cannot be compiled is refused rather than skipped. Skipping
// would produce an engine that silently enforces less than the manifest says,
// which is the worst possible failure for a security control: it looks like it
// is working.
func New(e *schema.Egress) (*Engine, error) {
	if e == nil {
		return &Engine{fallback: schema.ModeBlock}, nil
	}
	fallback := e.Default
	if fallback == "" {
		fallback = schema.ModeBlock
	}
	eng := &Engine{fallback: fallback, allowIPv6: e.AllowIPv6}

	for i, r := range e.Rules {
		c, err := compile(r, i)
		if err != nil {
			return nil, fmt.Errorf("policy: rule %d for %q: %w", i, r.Host, err)
		}
		eng.rules = append(eng.rules, c)
	}
	// Sorted once so that evaluation is a scan rather than a sort. Ties break
	// on the manifest index, so the earlier rule wins and the outcome does not
	// depend on the sort being stable.
	sort.SliceStable(eng.rules, func(a, b int) bool {
		if eng.rules[a].specificity != eng.rules[b].specificity {
			return eng.rules[a].specificity > eng.rules[b].specificity
		}
		return eng.rules[a].index < eng.rules[b].index
	})
	return eng, nil
}

func compile(r schema.EgressRule, index int) (compiled, error) {
	c := compiled{rule: r, index: index}

	host := strings.ToLower(strings.TrimSpace(r.Host))
	if host == "" {
		return c, fmt.Errorf("the host is empty")
	}

	if h, port, err := net.SplitHostPort(host); err == nil {
		n, convErr := strconv.Atoi(port)
		if convErr != nil || n <= 0 || n > 65535 {
			return c, fmt.Errorf("the port %q is not valid", port)
		}
		host, c.port = h, n
	}
	// Before the trailing dot is trimmed, because "*." would otherwise become
	// "*" and a malformed rule would silently mean match everything. A rule
	// that widens itself on a typo is the worst kind of security bug.
	if host == "*." {
		return c, fmt.Errorf("a wildcard needs a domain after it")
	}
	host = strings.TrimSuffix(host, ".")

	switch {
	case host == "*":
		c.matchAll = true
	case strings.HasPrefix(host, "*."):
		suffix := host[1:] // keep the leading dot
		if strings.Contains(suffix[1:], "*") {
			// A star in the middle would need a real glob engine, and a glob
			// engine is a place to hide a pattern that takes exponential time.
			// Prefix wildcards cover every case a manifest needs.
			return c, fmt.Errorf("a wildcard is only allowed at the start, as *.example.com")
		}
		c.hostSuffix = suffix
	default:
		if strings.Contains(host, "*") {
			return c, fmt.Errorf("a wildcard is only allowed at the start, as *.example.com")
		}
		if ip := net.ParseIP(host); ip != nil {
			c.ip = ip
		} else {
			c.hostExact = host
		}
	}

	for _, p := range r.Paths {
		if !strings.HasPrefix(p, "/") {
			p = "/" + p
		}
		c.paths = append(c.paths, p)
	}
	// Longest first, so the most specific path in a rule decides the match and
	// reports the right specificity.
	sort.Slice(c.paths, func(a, b int) bool { return len(c.paths[a]) > len(c.paths[b]) })

	if len(r.Methods) > 0 {
		c.methods = make(map[string]bool, len(r.Methods))
		for _, m := range r.Methods {
			c.methods[strings.ToUpper(strings.TrimSpace(m))] = true
		}
	}

	c.specificity = specificityOf(c)
	return c, nil
}

// specificityOf ranks a rule.
//
// The weights are chosen so that no combination of weaker signals can outrank
// a stronger one: an exact host always beats a wildcard however many paths the
// wildcard names. That is what makes the ranking explainable in one sentence,
// which is the only kind of ranking anyone reasons about correctly.
func specificityOf(c compiled) int {
	const (
		exactHost    = 1 << 20
		ipHost       = 1 << 20 // an address is as specific as an exact name
		wildcardHost = 1 << 12
		anyHost      = 0
		perPathChar  = 1 << 2
		hasMethod    = 1 << 10
		hasPort      = 1 << 11
	)
	score := anyHost
	switch {
	case c.hostExact != "":
		// No length term. Two exact hosts can never both match one request, so
		// ranking them against each other decides nothing and only makes the
		// printed order look arbitrary. They tie, and the manifest order
		// breaks the tie, so a policy prints in the order it was written.
		score += exactHost
	case c.ip != nil:
		score += ipHost
	case c.hostSuffix != "":
		// A longer suffix is more specific: *.api.example.com beats
		// *.example.com.
		score += wildcardHost + len(c.hostSuffix)
	}
	if len(c.paths) > 0 {
		score += perPathChar * len(c.paths[0])
	}
	if c.methods != nil {
		score += hasMethod
	}
	if c.port != 0 {
		score += hasPort
	}
	return score
}

// normalize brings a request to the form the compiled rules are in.
func normalize(req Request) (host string, port int, method, path string) {
	host = strings.ToLower(strings.TrimSpace(req.Host))
	// A trailing dot is the fully qualified form of a name and means the same
	// thing. Not normalizing it is a way to walk straight past a rule.
	host = strings.TrimSuffix(host, ".")
	port = req.Port
	if port == 0 {
		port = 80
		if req.TLS {
			port = 443
		}
	}
	method = strings.ToUpper(req.Method)
	if method == "" {
		method = "GET"
	}
	path = req.Path
	if path == "" {
		path = "/"
	}
	return host, port, method, path
}

// Evaluate returns the decision for a request.
//
// This is the hot path. The rules are sorted by specificity at compile time,
// so the first rule that matches is the one that decides and the scan stops
// there. Nothing is allocated: the reason is a method, and the full chain of
// matching rules is Explain's job.
func (e *Engine) Evaluate(req Request) Decision {
	host, port, method, path := normalize(req)

	for i := range e.rules {
		c := &e.rules[i]
		why, ok := c.matches(host, port, method, path)
		if !ok {
			continue
		}
		return Decision{
			Mode:        c.rule.Mode,
			RuleHost:    c.rule.Host,
			RateLimit:   c.rule.RateLimit,
			Credential:  c.rule.Credential,
			Fixtures:    c.rule.Fixtures,
			WebhookPath: c.rule.WebhookPath,
			why:         why,
			host:        host,
			note:        c.rule.Note,
		}
	}
	return Decision{Mode: e.fallback, host: host}
}

// Explain returns the decision together with every rule that matched, most
// specific first.
//
// It is what af net explain prints and what the policy page renders. A
// surprising decision is diagnosable when you can see the rules that lost, and
// mysterious when you can only see the one that won.
func (e *Engine) Explain(req Request) (Decision, []Match) {
	host, port, method, path := normalize(req)

	var chain []Match
	for i := range e.rules {
		c := &e.rules[i]
		why, ok := c.matches(host, port, method, path)
		if !ok {
			continue
		}
		chain = append(chain, Match{
			Rule: c.rule, Index: c.index, Specificity: c.specificity, Why: why.String(),
		})
	}
	return e.Evaluate(req), chain
}

func (c *compiled) matches(host string, port int, method, path string) (why, bool) {
	var w why
	if c.port != 0 && c.port != port {
		return w, false
	}
	switch {
	case c.matchAll:
		w.host = hostAny
	case c.hostExact != "":
		if c.hostExact != host {
			return w, false
		}
		w.host = hostExactly
	case c.ip != nil:
		ip := net.ParseIP(host)
		// An address rule matches only an address. A name that resolves to the
		// address is a different request, and treating them alike is how a
		// rule ends up covering traffic nobody intended.
		if ip == nil || !ip.Equal(c.ip) {
			return w, false
		}
		w.host = hostAddress
	case c.hostSuffix != "":
		if !strings.HasSuffix(host, c.hostSuffix) {
			return w, false
		}
		// The wildcard must cover at least one label, so *.example.com does
		// not match example.com itself. That distinction matters: an apex and
		// its subdomains are frequently operated differently.
		if len(host) <= len(c.hostSuffix) {
			return w, false
		}
		w.host, w.suffix = hostSuffixed, c.hostSuffix
	default:
		return w, false
	}

	if c.methods != nil {
		if !c.methods[method] {
			return w, false
		}
		w.method = true
	}

	if len(c.paths) > 0 {
		matched := ""
		for _, p := range c.paths {
			if pathMatches(path, p) {
				matched = p
				break
			}
		}
		if matched == "" {
			return w, false
		}
		w.path = matched
	}
	return w, true
}

// pathMatches reports whether a request path is under a prefix.
//
// The boundary check is what stops /admin from matching /administrator, which
// is the classic way a path rule turns out to cover far more than its author
// meant.
func pathMatches(path, prefix string) bool {
	if prefix == "/" {
		return true
	}
	prefix = strings.TrimSuffix(prefix, "/")
	if path == prefix {
		return true
	}
	return strings.HasPrefix(path, prefix+"/")
}

// AllowsIPv6 reports whether the environment may open IPv6 connections.
//
// Off by default, because an IPv6 path that bypasses the proxy is the most
// common way an egress control is silently defeated: the application resolves
// a AAAA record, connects directly, and every rule in the manifest is simply
// not consulted.
func (e *Engine) AllowsIPv6() bool { return e.allowIPv6 }

// Default returns the fallback mode.
func (e *Engine) Default() schema.Mode { return e.fallback }

// Rules returns the compiled rules in evaluation order, which is what a policy
// view renders so that a reader sees them in the order that decides.
func (e *Engine) Rules() []schema.EgressRule {
	out := make([]schema.EgressRule, 0, len(e.rules))
	for _, c := range e.rules {
		out = append(out, c.rule)
	}
	return out
}

// Hosts returns every host the policy names, sorted.
func (e *Engine) Hosts() []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range e.rules {
		if !seen[c.rule.Host] {
			seen[c.rule.Host] = true
			out = append(out, c.rule.Host)
		}
	}
	sort.Strings(out)
	return out
}

// InspectsHost reports whether decisions for a host need to see inside TLS.
//
// A host reached over HTTPS arrives as a tunnel, and a tunnel shows only the
// name. That is enough when the answer is the same for every request to that
// host, and not enough otherwise: a rule that names paths or methods cannot be
// applied without the path, and capture, mock, sandbox, and synth all have to
// read or replace the request itself.
//
// This is asked before the connection is decided rather than after, because
// terminating TLS is a choice that has to be made at the handshake. Getting it
// wrong in the safe direction costs a tunnel that could have been inspected;
// getting it wrong the other way silently applies a host rule where a path
// rule was written.
func (e *Engine) InspectsHost(host string, port int) bool {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if port == 0 {
		port = 443
	}
	if inspectMode(e.fallback) {
		return true
	}
	for i := range e.rules {
		c := &e.rules[i]
		if !c.matchesHost(host, port) {
			continue
		}
		if len(c.paths) > 0 || c.methods != nil || inspectMode(c.rule.Mode) {
			return true
		}
	}
	return false
}

// inspectMode reports whether a mode can be served without reading the
// request. Only block and allow can: one refuses everything to the host and
// the other forwards everything to it.
func inspectMode(m schema.Mode) bool {
	switch m {
	case schema.ModeCapture, schema.ModeMock, schema.ModeSandbox, schema.ModeSynth:
		return true
	}
	return false
}

// matchesHost reports whether a rule could apply to a host, ignoring the path
// and the method.
func (c *compiled) matchesHost(host string, port int) bool {
	if c.port != 0 && c.port != port {
		return false
	}
	switch {
	case c.matchAll:
		return true
	case c.hostExact != "":
		return c.hostExact == host
	case c.ip != nil:
		ip := net.ParseIP(host)
		return ip != nil && ip.Equal(c.ip)
	case c.hostSuffix != "":
		return strings.HasSuffix(host, c.hostSuffix) && len(host) > len(c.hostSuffix)
	}
	return false
}
