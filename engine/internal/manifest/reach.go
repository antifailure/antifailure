package manifest

import (
	"fmt"
	"net"
	"strings"

	"golang.org/x/net/publicsuffix"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// How far an egress rule reaches, read from where its stars sit.
//
// A rule that lets a request out either names the host it goes to or names a
// shape that many hosts share, and the two are not the same promise. The
// policy engine matches both correctly and the matcher was never the problem:
// *.zapier.com matches every name under zapier.com, which is what it says.
// What nothing said was that it says that. af net explain rendered a Zapier
// catch hook reached through that rule as a clean ALLOW, with the same
// sentence an exact host gets, and a catch hook is somebody's live automation
// posting to real contacts. A rehearsal reaching it is the harm the sidecar
// exists to prevent, and the explanation read as though nothing was wrong.
//
// So the breadth is stated wherever a rule is explained, and the one shape
// that cannot be a decision about one owner is refused. The public suffix list
// is what tells the two apart, because label counting cannot: *.co.uk and
// *.zapier.com both hold two labels still, and only one of them names a
// company. The list is compiled into golang.org/x/net/publicsuffix, so asking
// it opens no connection.

// reach is how far a host pattern reaches.
type reach int

const (
	// reachOne is a host, an address, or a pattern whose stars are all
	// interior and sit under one owner's domain, such as
	// email.*.amazonaws.com. It can only ever reach one owner's one service.
	reachOne reach = iota
	// reachDomain is a leading wildcard over one owner's domain, such as
	// *.zapier.com. One owner, and every name they have or will ever make.
	reachDomain
	// reachPlatform is a star over a suffix a platform hands out to its
	// customers, such as *.github.io. One company runs it and the names under
	// it belong to everybody who signed up.
	reachPlatform
	// reachAnybody is a star where the owner's name goes, such as *.com,
	// *.co.uk or hooks.*.com. The pattern reaches names registered by anybody
	// at all, which is the reach the rule that only * may match everything
	// exists to stop, arrived at one label down.
	reachAnybody
)

// reachOf classifies a pattern. domain is the literal text to the right of the
// rightmost star, which is the part of the name the pattern holds still, and
// is empty for a pattern that ends in a star.
func reachOf(host string) (r reach, domain string) {
	h := strings.ToLower(strings.TrimSpace(host))
	if hp, _, err := net.SplitHostPort(h); err == nil {
		h = hp
	}
	h = strings.TrimSuffix(h, ".")
	labels := strings.Split(h, ".")
	last := -1
	for i, l := range labels {
		if l == "*" {
			last = i
		}
	}
	if last < 0 {
		return reachOne, h
	}
	domain = strings.Join(labels[last+1:], ".")
	if domain == "" {
		return reachAnybody, ""
	}
	// PublicSuffix answers for a name it has no rule for with its last label
	// and icann false, which is how a private top level name such as corp is
	// told apart from a platform's suffix: the platform's has a dot in it.
	suffix, icann := publicsuffix.PublicSuffix(domain)
	isSuffix := suffix == domain
	switch {
	case isSuffix && icann:
		return reachAnybody, domain
	case isSuffix && strings.Contains(domain, "."):
		return reachPlatform, domain
	case !strings.HasPrefix(h, "*."):
		return reachOne, domain
	default:
		return reachDomain, domain
	}
}

// EgressReach says, as a noun phrase, which names a pattern covers beyond one
// host, and is empty for a pattern that names one host.
//
// It is the one place the breadth is worded. af net explain, af net policy,
// af init, the MCP probe and the fidelity report each put it in a sentence of
// their own, and five wordings of one fact are five chances for one of them to
// understate it.
func EgressReach(host string) string {
	r, domain := reachOf(host)
	switch r {
	case reachDomain:
		return "every name under " + domain + ", however many labels deep"
	case reachPlatform:
		return "every customer's names under " + domain + ", not only yours"
	case reachAnybody:
		if domain == "" {
			return "that name under every top level domain, whoever registered it"
		}
		return "names under " + domain + " registered by anybody at all"
	}
	return ""
}

// EgressCaution is the sentence a rule that lets requests out for real gets
// when it does not name the host they go to, and is empty for every other
// rule.
//
// Only allow and sandbox reach a real host, which is what Decision.Allowed
// reports. A wildcard block is conservative, and capture, mock and emulate
// answer inside the environment, where the sidecar already refuses to invent
// a provider's success for a host nobody named.
//
// The anybody sentence is not reachable from a manifest, because the
// validator refuses that shape outside block. It is worded anyway because the
// policy engine compiles it, and a caller holding an engine built some other
// way should read the truth rather than nothing.
func EgressCaution(host string, mode schema.Mode) string {
	if mode != schema.ModeAllow && mode != schema.ModeSandbox {
		return ""
	}
	covers := EgressReach(host)
	if covers == "" {
		return ""
	}
	lands := "reaches the real host"
	if mode == schema.ModeSandbox {
		lands = "reaches the real host carrying the sandbox credential"
	}
	return fmt.Sprintf(
		"The rule for %s names no host. It covers %s, including names nobody has written down, "+
			"and a request to any of them %s.", host, covers, lands)
}
