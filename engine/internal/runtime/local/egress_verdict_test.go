package local_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/antifailure/antifailure/engine/internal/runtime/local"
)

// Exit 9 meant two completely different things and the test said neither.
//
// THE FAILURE. On 2026-09-05, `engine`, which is a required context, went red
// on a pull request that touched no file under engine/ at all:
//
//	--- FAIL: TestEgress_HTTPSIsDecidedByHost (2.94s)
//	    expected: 0
//	    actual  : 9
//	    Messages: an allowed host was not reachable over HTTPS
//
// The probe is `curl ... && exit 0 || exit 9`, so every way of not getting out
// arrives as the same number: the sidecar refused a host the policy allows,
// which is the containment regression the test exists to catch, and the host
// simply did not answer, which is the weather. The message names the first and
// the second is far more likely, so a transient external failure reads as the
// most alarming result this suite can produce.
//
// THE SIDECAR ALREADY KNOWS WHICH. It writes one decision record per request,
// allowed or not, and Runtime.Decisions reads them; the capture test in this
// same package has been asserting on that log for as long as it existed. The
// reachability tests never asked. So these two functions turn the log into the
// answer, and they are pure so that the discrimination itself is proved on a
// machine with no Docker daemon and no network.
//
// The rule they encode, in one sentence: a probe that did not get out is OURS
// unless the sidecar's own record says it allowed the connection AND this
// machine independently cannot reach the host either. Only that row skips, and
// it needs positive evidence from two places that do not share a cause.

// sidecarVerdict is what the sidecar's own record says about a probe that did
// not get out.
type sidecarVerdict string

const (
	// verdictRefused: the sidecar decided against a connection. When the
	// policy allowed the host, this is the regression the suite is for.
	verdictRefused sidecarVerdict = "refused"
	// verdictAllowedAndFailed: the sidecar let the connection through and it
	// failed anyway, so the failure is past the sidecar.
	verdictAllowedAndFailed sidecarVerdict = "allowed-and-failed"
	// verdictNoDecision: nothing about this host reached the sidecar at all.
	//
	// Not a network excuse. Every external name inside an environment resolves
	// to the sidecar, so a request that produced no decision did not get as
	// far as the thing that decides, and the DNS that failed is the sidecar's
	// own.
	verdictNoDecision sidecarVerdict = "no-decision"
)

// readVerdict reports what the sidecar's log says about one host.
//
// The LAST decision naming the host, because a probe may retry and the answer
// that matters is the one it ended on. Decisions arrive oldest first, which is
// the order Runtime.Decisions returns them in.
func readVerdict(decisions []local.Decision, host string) (sidecarVerdict, local.Decision) {
	var last local.Decision
	var found bool
	for _, d := range decisions {
		if d.Host == host {
			last, found = d, true
		}
	}
	switch {
	case !found:
		return verdictNoDecision, local.Decision{}
	case last.Allowed:
		return verdictAllowedAndFailed, last
	default:
		return verdictRefused, last
	}
}

// unreachableIsOurs decides whether a probe that did not get out is this
// product's failure or the network's, and writes the sentence for either.
//
// reachableFromHere is the second, independent piece of evidence: the test
// process asking the same host over the same scheme at the moment of failure.
// It is what stops this becoming "the test tolerates an unreachable host". A
// sidecar that logs an allow and then drops the bytes still fails here,
// because the machine outside it can still reach the host.
func unreachableIsOurs(
	v sidecarVerdict, d local.Decision, host, origin string, reachableFromHere bool,
) (ours bool, message string) {
	switch v {
	case verdictRefused:
		return true, fmt.Sprintf(
			"the sidecar REFUSED %s, which the policy allows. mode=%q rule=%q reason=%q status=%d error=%q. "+
				"This is the containment regression this test exists to catch, not the network.",
			host, d.Mode, d.Rule, d.Reason, d.Status, d.Error)
	case verdictNoDecision:
		return true, fmt.Sprintf(
			"the probe did not get out and the sidecar recorded no decision about %s at all. "+
				"Every external name in an environment resolves to the sidecar, so the request "+
				"never reached the thing that decides: the sidecar's DNS, the network, or the "+
				"probe container itself. None of those is the host being down.",
			host)
	case verdictAllowedAndFailed:
		if reachableFromHere {
			return true, fmt.Sprintf(
				"the sidecar ALLOWED %s and the request failed anyway, while this machine reaches "+
					"%s right now. The failure is between the sidecar and the host. "+
					"mode=%q rule=%q status=%d error=%q",
				host, origin, d.Mode, d.Rule, d.Status, d.Error)
		}
		return false, fmt.Sprintf(
			"the sidecar allowed %s and neither the environment nor this machine could reach %s. "+
				"Two independent probes agree the host is unreachable, so this run says nothing "+
				"about containment. sidecar error=%q",
			host, origin, d.Error)
	default:
		// A verdict nothing produces. Failing is right: a classification this
		// does not understand must not become a skip.
		return true, fmt.Sprintf("unrecognised sidecar verdict %q about %s", v, host)
	}
}

func TestReadVerdict_TellsTheThreeCasesApart(t *testing.T) {
	const host = "example.com"
	cases := []struct {
		name      string
		decisions []local.Decision
		want      sidecarVerdict
	}{
		{
			name:      "nothing about this host reached the sidecar",
			decisions: []local.Decision{{Host: "other.test", Allowed: true}},
			want:      verdictNoDecision,
		},
		{
			name:      "an empty log is not an allow",
			decisions: nil,
			want:      verdictNoDecision,
		},
		{
			name:      "the sidecar decided against it",
			decisions: []local.Decision{{Host: host, Allowed: false, Mode: "block"}},
			want:      verdictRefused,
		},
		{
			name:      "the sidecar let it through",
			decisions: []local.Decision{{Host: host, Allowed: true, Mode: "allow"}},
			want:      verdictAllowedAndFailed,
		},
		{
			// A retry that ends on a refusal is a refusal. Reading the first
			// record would report the opposite of what happened.
			name: "the last decision wins",
			decisions: []local.Decision{
				{Host: host, Allowed: true, Mode: "allow"},
				{Host: host, Allowed: false, Mode: "block"},
			},
			want: verdictRefused,
		},
		{
			name: "and it wins in the other direction too",
			decisions: []local.Decision{
				{Host: host, Allowed: false, Mode: "block"},
				{Host: host, Allowed: true, Mode: "allow"},
			},
			want: verdictAllowedAndFailed,
		},
		{
			// Another host's refusal must not be read as this host's.
			name: "a decision about a different host decides nothing here",
			decisions: []local.Decision{
				{Host: "www.iana.org", Allowed: false, Mode: "block"},
			},
			want: verdictNoDecision,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _ := readVerdict(c.decisions, host)
			if got != c.want {
				t.Errorf("readVerdict = %q, want %q", got, c.want)
			}
		})
	}
}

func TestUnreachableIsOurs_OnlyOneRowIsTheWeather(t *testing.T) {
	const host = "example.com"
	const origin = "https://example.com"
	cases := []struct {
		name      string
		verdict   sidecarVerdict
		decision  local.Decision
		reachable bool
		wantOurs  bool
		says      string
	}{
		{
			name:     "a refusal of an allowed host is ours whatever the network is doing",
			verdict:  verdictRefused,
			decision: local.Decision{Mode: "block", Rule: "", Reason: "no rule"},
			// The network is fine, and it would not matter if it were not.
			reachable: true,
			wantOurs:  true,
			says:      "REFUSED",
		},
		{
			name:      "and still ours when the host is down as well",
			verdict:   verdictRefused,
			decision:  local.Decision{Mode: "block"},
			reachable: false,
			wantOurs:  true,
			says:      "REFUSED",
		},
		{
			name:      "a request that never reached the sidecar is ours",
			verdict:   verdictNoDecision,
			reachable: false,
			wantOurs:  true,
			says:      "recorded no decision",
		},
		{
			// THE ROW THE WHOLE DESIGN TURNS ON. A sidecar that logs an allow
			// and then drops the bytes must not be able to skip, and this is
			// what stops it: the machine outside can still reach the host.
			name:      "an allow that failed while the host answers here is ours",
			verdict:   verdictAllowedAndFailed,
			decision:  local.Decision{Mode: "allow", Error: "upstream reset"},
			reachable: true,
			wantOurs:  true,
			says:      "ALLOWED",
		},
		{
			// The only skip, and it needs both pieces of evidence.
			name:      "an allow that failed while nothing here can reach it either is the weather",
			verdict:   verdictAllowedAndFailed,
			decision:  local.Decision{Mode: "allow", Error: "dial tcp: i/o timeout"},
			reachable: false,
			wantOurs:  false,
			says:      "neither the environment nor this machine",
		},
		{
			name:      "a classification nothing produces fails rather than skips",
			verdict:   sidecarVerdict("something new"),
			reachable: false,
			wantOurs:  true,
			says:      "unrecognised",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ours, msg := unreachableIsOurs(c.verdict, c.decision, host, origin, c.reachable)
			if ours != c.wantOurs {
				t.Errorf("ours = %v, want %v (message: %s)", ours, c.wantOurs, msg)
			}
			if !strings.Contains(msg, c.says) {
				t.Errorf("message %q does not say %q", msg, c.says)
			}
			if !strings.Contains(msg, host) {
				t.Errorf("message %q does not name the host it is about", msg)
			}
		})
	}
}

// TestUnreachableIsOurs_SkipsExactlyOnce is the counting check. A rule with
// two ways to skip is a rule that will grow a third.
func TestUnreachableIsOurs_SkipsExactlyOnce(t *testing.T) {
	skips := 0
	for _, v := range []sidecarVerdict{verdictRefused, verdictNoDecision, verdictAllowedAndFailed} {
		for _, reachable := range []bool{true, false} {
			if ours, _ := unreachableIsOurs(v, local.Decision{}, "example.com", "https://example.com", reachable); !ours {
				skips++
			}
		}
	}
	if skips != 1 {
		t.Errorf("%d of the six combinations skip, want exactly 1: the sidecar allowed it and "+
			"this machine cannot reach the host either", skips)
	}
}
