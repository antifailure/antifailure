// Package egress reads the sidecar's decision log and says what it means.
//
// It exists so that the command line and the MCP server answer questions about
// egress with one implementation rather than two. The command line asks "does
// this stop the merge"; the MCP server asks "was the environment actually
// contained". Those are different questions over the same log, and the shared
// part is the reading of the log itself.
package egress

import (
	"fmt"
	"sort"
	"strings"

	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/internal/runtime/local"
)

// RuleSurprise is the manifest policy key for a host nothing declared.
const RuleSurprise = "egress_surprise"

// Summarise counts the decision log and names the hosts nothing declared.
func Summarise(decisions []local.Decision) *report.Egress {
	out := &report.Egress{}
	surprises := map[string]bool{}
	for _, d := range decisions {
		switch d.Mode {
		case "allow", "sandbox":
			out.Allowed++
		case "capture":
			out.Captured++
		case "mock":
			out.Mocked++
		default:
			out.Refused++
			if d.Rule == "" && d.Host != "" {
				// No rule matched at all, which means the manifest does not
				// mention this host. Usually a dependency somebody added
				// without noticing.
				surprises[d.Host] = true
			}
		}
	}
	for host := range surprises {
		out.Surprises = append(out.Surprises, host)
	}
	sort.Strings(out.Surprises)
	return out
}

// Finding ranks a surprise against the project's policy.
func Finding(e *report.Egress, p report.Policy) *report.Finding {
	if e == nil || len(e.Surprises) == 0 || p.EgressSurprise == report.LevelIgnore {
		return nil
	}
	return &report.Finding{
		Rule: RuleSurprise, Level: p.EgressSurprise,
		Count: len(e.Surprises), Where: strings.Join(e.Surprises, ", "),
		Title: fmt.Sprintf("The environment tried to reach %s nothing in the manifest mentions.",
			plural(len(e.Surprises), "host", "hosts")),
		Detail: "The request was refused, so nothing left the environment. It is usually a " +
			"dependency somebody added without noticing.",
		Fix: "Add an egress rule for it with the mode you intend, or leave it blocked and " +
			"set policy.egress_surprise to warn.",
	}
}

// Containment is what the log says about whether the environment was really
// contained, as opposed to merely configured to be.
//
// The counts here are the ones nothing else surfaces. A summary that says four
// requests reached the payment provider is not an answer to "was this safe";
// the answer is whether the credential on those four requests was the sandbox
// one or the application's own, and until this type existed there was no way
// to ask.
type Containment struct {
	// Total is every decision read.
	Total int
	// Allowed, Refused, Captured and Mocked mirror the summary.
	Allowed, Refused, Captured, Mocked int
	// Sandbox is how many requests a sandbox rule decided.
	Sandbox int
	// Substituted is how many had their credential replaced on the way out.
	Substituted int
	// SandboxUnsubstituted is the number a sandbox rule let out WITHOUT
	// replacing the credential, which means the application's own credential
	// went to the provider.
	//
	// This is the number the whole type exists for. The sidecar substitutes
	// only when a value was configured for the rule's credential name, so a
	// sandbox rule whose credential never arrived forwards whatever the
	// application sent and looks, in every other column, exactly like a
	// working sandbox call. It is a containment failure that presents as a
	// success.
	SandboxUnsubstituted int
	// HostOnly is how many decisions were made without seeing the path or
	// method, which is every HTTPS request until the environment certificate
	// lands. A rule naming paths could only half apply to one of these.
	HostOnly int
	// RateLimited and WaitedMs are how many requests the policy held and for
	// how long in total.
	RateLimited int
	WaitedMs    int64
	// Hosts is what was reached, worst first.
	Hosts []Host
}

// Host is one destination and what happened to the requests aimed at it.
type Host struct {
	Host string
	// Modes are the decision modes seen for this host, sorted.
	Modes []string
	// Requests is how many decisions named it.
	Requests int
	Allowed  int
	Refused  int
	// Substituted and Unsubstituted split the sandbox calls.
	Substituted   int
	Unsubstituted int
	// Declared says whether any rule in the manifest matched.
	Declared bool
}

// Observe reads the log into a containment report.
func Observe(decisions []local.Decision) Containment {
	c := Containment{Total: len(decisions)}
	byHost := map[string]*Host{}
	modes := map[string]map[string]bool{}

	for _, d := range decisions {
		switch d.Mode {
		case "allow", "sandbox":
			c.Allowed++
		case "capture":
			c.Captured++
		case "mock":
			c.Mocked++
		default:
			c.Refused++
		}
		if d.HostOnly {
			c.HostOnly++
		}
		if d.Substituted {
			c.Substituted++
		}
		if d.WaitedMs > 0 {
			c.RateLimited++
			c.WaitedMs += d.WaitedMs
		}
		if d.Mode == "sandbox" {
			c.Sandbox++
			if !d.Substituted {
				c.SandboxUnsubstituted++
			}
		}
		if d.Host == "" {
			continue
		}

		h, seen := byHost[d.Host]
		if !seen {
			h = &Host{Host: d.Host}
			byHost[d.Host] = h
			modes[d.Host] = map[string]bool{}
		}
		h.Requests++
		if d.Rule != "" {
			h.Declared = true
		}
		if d.Allowed {
			h.Allowed++
		} else {
			h.Refused++
		}
		if d.Mode == "sandbox" {
			if d.Substituted {
				h.Substituted++
			} else {
				h.Unsubstituted++
			}
		}
		if d.Mode != "" {
			modes[d.Host][d.Mode] = true
		}
	}

	for host, h := range byHost {
		for m := range modes[host] {
			h.Modes = append(h.Modes, m)
		}
		sort.Strings(h.Modes)
		c.Hosts = append(c.Hosts, *h)
	}
	// Worst first: a host that leaked an unsubstituted credential, then one
	// nothing declared, then by traffic. Somebody who reads one row should
	// read the row that matters.
	sort.SliceStable(c.Hosts, func(i, j int) bool {
		a, b := c.Hosts[i], c.Hosts[j]
		if (a.Unsubstituted > 0) != (b.Unsubstituted > 0) {
			return a.Unsubstituted > 0
		}
		if a.Declared != b.Declared {
			return !a.Declared
		}
		if a.Requests != b.Requests {
			return a.Requests > b.Requests
		}
		return a.Host < b.Host
	})
	return c
}

// RuleUnsubstituted is the finding for a sandbox call that carried the
// application's own credential.
//
// It has no manifest policy key of its own and is always a failure. That is
// deliberate: every other finding in this system is ranked by the project's
// policy because reasonable projects disagree about how much a slow query or a
// rewritten migration matters. Nobody wants their application's live
// credential sent to a provider from an environment running unreviewed code,
// so there is no level to configure and no way to turn it down.
const RuleUnsubstituted = "egress_unsubstituted_credential"

// CredentialFinding reports sandbox calls that were never substituted.
//
// Nil when there are none, so a caller can append it unconditionally.
func CredentialFinding(c Containment) *report.Finding {
	if c.SandboxUnsubstituted == 0 {
		return nil
	}
	var hosts []string
	for _, h := range c.Hosts {
		if h.Unsubstituted > 0 {
			hosts = append(hosts, h.Host)
		}
	}
	sort.Strings(hosts)
	return &report.Finding{
		Rule: RuleUnsubstituted, Level: report.LevelFail,
		Count: c.SandboxUnsubstituted, Where: strings.Join(hosts, ", "),
		Title: fmt.Sprintf(
			"%s left the environment under a sandbox rule without the credential being replaced.",
			plural(c.SandboxUnsubstituted, "request", "requests")),
		Detail: "A sandbox rule substitutes a sandbox credential on the way out. The " +
			"substitution only happens when a value was configured for the rule's " +
			"credential name, so these requests carried whatever the application sent, " +
			"which may be a live credential.",
		Fix: "Set the sandbox credential the rule names, then run this again. Until then " +
			"treat the calls to these hosts as having used the application's own credential.",
	}
}

// plural picks the noun. Small enough that sharing it would cost more than it
// saves, and it is the only formatting this package does.
func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}
