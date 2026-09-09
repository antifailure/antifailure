package conformance

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// The emulator suite, and what it refuses to be a suite about.
//
// Antifailure writes no emulators. LocalStack, Azurite and the vendors' own
// carry years of fidelity work nobody here is going to reproduce, and a suite
// that checked whether S3 semantics were right would be checking somebody
// else's product. What this repository claims is narrower and is entirely its
// own: that an application reaches one of them with NO endpoint override, that
// the container holding a third party image cannot reach the internet, and
// that an operation the emulator has not implemented comes back as a refusal
// rather than as a plausible answer.
//
// So the subject is the emulator AS ROUTED. Every behaviour below sends a real
// request to the provider's own hostname through provider.Emulator.RoundTrip,
// and an implementation that pointed that at the emulator's address directly
// would pass all nine and prove none of them. That is stated on the interface
// as well, because it is the one way to write a conforming provider that
// verifies nothing.
//
// THE FIRST TWO BEHAVIOURS ARE THE POINT OF THE OTHERS. An emulator that
// refuses every request satisfies "an unimplemented operation is refused",
// satisfies "a live credential is refused", holds no state to leak and reaches
// nothing, and is completely useless. That is this repository's own rule about
// a check that cannot say no, pointed at an emulator: Covered_IsAnswered and
// Covered_IsNotRefused are separate behaviours because the failure they catch
// is a suite that only ever proved things do not happen.
//
// This file ships with a broken fake and a self test in the same commit, which
// is the rule conformance/db.go states because it was once not followed.
// emulator_selftest_test.go points this suite at an emulator broken one
// behaviour at a time and requires each break to turn its behaviour red.

// EmulatorFactory builds an emulator handle for one behaviour.
//
// Each behaviour gets its own, for the reason DatastoreFactory does: an
// emulator holds state by definition, and one behaviour's leftovers must not
// decide another's result.
type EmulatorFactory func(t *testing.T) provider.Emulator

// EmulatorOptions configure a run.
type EmulatorOptions struct {
	// Timeout bounds each behaviour. Zero uses two minutes, which is generous
	// for a container on the same host and tight enough that a hung route
	// fails the test rather than the job.
	Timeout time.Duration
	// ReachAddress is the address the containment behaviour tries to open FROM
	// the emulator container. Zero uses reachDefault.
	//
	// Configurable because a machine behind a proxy that answers everything
	// would make the default look reachable, and a suite whose containment
	// check cannot be pointed somewhere real is a containment check people
	// switch off.
	ReachAddress string
}

// reachDefault is a public resolver, on its own port.
//
// Chosen because it answers from anywhere with a route to the internet, which
// is what makes the assertion two sided: a container that really has a route
// out CONNECTS, so this behaviour goes red rather than passing on a network
// error that would have happened anyway. An address nothing answers on would
// pass for a contained emulator and for a leaking one alike.
const reachDefault = "1.1.1.1:53"

// The capability names an emulator behaviour can require. Quoted in the skip
// line, so they name something a provider author can find on EmulatorCaps.
const (
	requiresCreatingProbe  = "a covered probe that creates something"
	requiresUncoveredProbe = "an uncovered probe and the provider's error shape"
	requiresLiveCredential = "a live credential vector"
)

var emulatorBehaviors = []Behavior{
	{"Name_IsNotEmpty", "The emulator names itself, because the egress rule and every error about it quote the name.", ""},
	{"Hosts_AreDeclared", "It answers for at least one hostname, and each is a bare host rather than a URL.", ""},
	{"Covered_IsAnswered", "A request inside the emulator's own surface gets a response at the provider's own hostname, with no endpoint override.", ""},
	{"Covered_IsNotRefused", "That response is not a refusal, because an emulator that refuses everything satisfies every other behaviour here and is useless.", ""},
	{"Uncovered_IsRefusedInTheProvidersErrorShape", "An operation the emulator does not implement is refused in the provider's own error shape, rather than answered with something plausible.", requiresUncoveredProbe},
	{"State_IsEnumerable", "What the emulator holds can be listed, because state nobody can list is state nobody can reason about after a run.", requiresCreatingProbe},
	{"State_IsDiscardedByReset", "Discarding the state leaves nothing behind, which is the promise teardown makes without ending the run to prove it.", requiresCreatingProbe},
	{"LiveCredential_IsRefused", "A request carrying a credential that works against the real provider is refused before the emulator sees it.", requiresLiveCredential},
	{"Reach_FindsNoRouteOut", "The emulator container cannot open a connection to the internet, which is a property of the network rather than a promise.", ""},
}

// EmulatorBehaviors returns the name and one sentence description of every
// behaviour this suite checks, sorted.
//
// The provider authoring page is written from this, so what conformance
// requires cannot drift from what is actually run.
func EmulatorBehaviors() []Behavior {
	out := append([]Behavior(nil), emulatorBehaviors...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// RunEmulator runs the whole suite against an emulator.
//
//	func TestLocalStack(t *testing.T) {
//	    conformance.RunEmulator(t, factory, conformance.EmulatorOptions{})
//	}
func RunEmulator(t *testing.T, factory EmulatorFactory, opts EmulatorOptions) {
	t.Helper()
	if opts.Timeout <= 0 {
		opts.Timeout = 2 * time.Minute
	}
	if opts.ReachAddress == "" {
		opts.ReachAddress = reachDefault
	}

	// One instance answers the capability questions, so that a skipped
	// behaviour is decided once and reported the same way everywhere.
	probe := factory(t)
	caps := probe.Capabilities()
	name := probe.Name()
	_ = probe.Close()

	for _, b := range emulatorBehaviors {
		b := b
		t.Run(b.Name, func(t *testing.T) {
			if reason := emulatorSkip(b, caps); reason != "" {
				// Named, never silent. A reviewer has to be able to see which
				// guarantee this emulator does not make.
				t.Skipf("skipped: %s declares no %s", orThisEmulator(name), reason)
			}
			ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
			defer cancel()
			runEmulatorBehavior(ctx, t, b.Name, factory, opts)
		})
	}
}

// emulatorSkip returns why a behaviour cannot run, or an empty string.
func emulatorSkip(b Behavior, caps provider.EmulatorCaps) string {
	switch b.Requires {
	case requiresCreatingProbe:
		if !caps.Covered.Creates {
			return requiresCreatingProbe
		}
	case requiresUncoveredProbe:
		if caps.Uncovered.Path == "" || caps.ErrorCode == "" || caps.ErrorContentType == "" {
			return requiresUncoveredProbe
		}
	case requiresLiveCredential:
		if caps.LiveCredential == "" {
			return requiresLiveCredential
		}
	}
	return ""
}

func orThisEmulator(name string) string {
	if name == "" {
		return "this emulator"
	}
	return name
}

func runEmulatorBehavior(
	ctx context.Context, t *testing.T, name string, factory EmulatorFactory, opts EmulatorOptions,
) {
	t.Helper()
	em := factory(t)
	t.Cleanup(func() { _ = em.Close() })
	caps := em.Capabilities()

	switch name {
	case "Name_IsNotEmpty":
		if strings.TrimSpace(em.Name()) == "" {
			t.Fatal("the emulator's name is empty. An egress rule names an emulator, so an " +
				"emulator with no name is one no rule can route to and every error about it " +
				"has a hole where the subject should be")
		}

	case "Hosts_AreDeclared":
		hosts := em.Hosts()
		if len(hosts) == 0 {
			t.Fatal("the emulator answers for no hosts, so no request could ever reach it")
		}
		for _, h := range hosts {
			if strings.TrimSpace(h) == "" {
				t.Errorf("one of the declared hosts is empty")
				continue
			}
			// A URL here is the mistake that looks harmless. The sidecar
			// matches on a hostname, so https://s3.amazonaws.com/ as a host
			// matches nothing and the rule silently routes no traffic.
			if strings.Contains(h, "/") || strings.Contains(h, ":") {
				t.Errorf("the host %q is not a bare hostname. The sidecar matches a hostname, "+
					"so a scheme, a path or a port here is a rule that matches nothing", h)
			}
		}

	case "Covered_IsAnswered":
		resp, body := emulatorSend(ctx, t, em, caps.Covered, "")
		if resp == nil {
			return // emulatorSend has already failed the test.
		}
		t.Logf("covered probe answered %d, %d bytes", resp.StatusCode, len(body))

	case "Covered_IsNotRefused":
		resp, body := emulatorSend(ctx, t, em, caps.Covered, "")
		if resp == nil {
			return
		}
		// Two ways to refuse, and an emulator that only did the second would
		// pass a status check. AWS returns a 200 carrying an error document
		// for several operations, so the body is read as well.
		if resp.StatusCode >= 400 {
			t.Fatalf("the covered probe %s was refused with %d, and a covered probe is "+
				"the one request this emulator claims to implement. An emulator that "+
				"refuses everything passes every other behaviour in this suite:\n%s",
				emulatorProbeString(em, caps.Covered), resp.StatusCode, emulatorBodyTail(body))
		}
		if caps.ErrorCode != "" && bytes.Contains(body, []byte(caps.ErrorCode)) {
			t.Fatalf("the covered probe %s answered %d but its body carries the error code "+
				"%q, so it was refused in a shape a status check does not see:\n%s",
				emulatorProbeString(em, caps.Covered), resp.StatusCode, caps.ErrorCode,
				emulatorBodyTail(body))
		}

	case "Uncovered_IsRefusedInTheProvidersErrorShape":
		resp, body := emulatorSend(ctx, t, em, caps.Uncovered, "")
		if resp == nil {
			return
		}
		if resp.StatusCode < 400 {
			t.Errorf("the uncovered probe %s was answered %d rather than refused. An "+
				"operation the emulator has not implemented, answered with something "+
				"plausible, is believed:\n%s",
				emulatorProbeString(em, caps.Uncovered), resp.StatusCode, emulatorBodyTail(body))
		}
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, caps.ErrorContentType) {
			t.Errorf("the uncovered probe was refused with Content-Type %q, and this "+
				"provider's own errors are %q. An SDK parses the error body, so a refusal "+
				"in the wrong shape fails inside the client as something else",
				ct, caps.ErrorContentType)
		}
		if !bytes.Contains(body, []byte(caps.ErrorCode)) {
			t.Errorf("the uncovered probe's refusal does not carry the error code %q, which "+
				"is what an SDK reads to tell an unimplemented operation from a real "+
				"failure:\n%s", caps.ErrorCode, emulatorBodyTail(body))
		}

	case "State_IsEnumerable":
		before, err := em.State(ctx)
		if err != nil {
			t.Fatalf("listing the state before the probe: %v", err)
		}
		if resp, _ := emulatorSend(ctx, t, em, caps.Covered, ""); resp == nil {
			return
		}
		after, err := em.State(ctx)
		if err != nil {
			t.Fatalf("listing the state after the probe: %v", err)
		}
		added := emulatorAdded(before, after)
		if len(added) == 0 {
			t.Fatalf("the covered probe declares Creates, and the state went from %d items "+
				"to %d with nothing new in it. Either the probe creates nothing, in which "+
				"case Creates is wrong, or the listing does not see what it created, in "+
				"which case nothing can be reasoned about after a run",
				len(before), len(after))
		}
		for _, it := range added {
			if it.Kind == "" || it.Name == "" {
				t.Errorf("a state item is %+v, and both fields identify it: the kind is the "+
					"emulator's own word for what it is and the name is which one", it)
			}
		}
		t.Logf("the covered probe added %d item(s): %s", len(added), emulatorItems(added))

	case "State_IsDiscardedByReset":
		if resp, _ := emulatorSend(ctx, t, em, caps.Covered, ""); resp == nil {
			return
		}
		held, err := em.State(ctx)
		if err != nil {
			t.Fatalf("listing the state: %v", err)
		}
		if len(held) == 0 {
			t.Fatal("the covered probe declares Creates and the state is empty after it, so " +
				"there is nothing for the reset to discard and this behaviour would pass " +
				"against an emulator that holds nothing")
		}
		if err := em.Reset(ctx); err != nil {
			t.Fatalf("resetting: %v", err)
		}
		left, err := em.State(ctx)
		if err != nil {
			t.Fatalf("listing the state after the reset: %v", err)
		}
		if len(left) != 0 {
			t.Fatalf("%d item(s) survived the reset: %s. Teardown removes the container and "+
				"the state goes with it, and this is the same promise asked without ending "+
				"the run to prove it", len(left), emulatorItems(left))
		}

	case "LiveCredential_IsRefused":
		resp, body := emulatorSend(ctx, t, em, caps.Covered, caps.LiveCredential)
		if resp == nil {
			return
		}
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("a request carrying a live credential was answered %d rather than "+
				"refused with 403. An emulator verifies no signature, so the only thing "+
				"between a misconfigured application and a real cloud account is this "+
				"refusal:\n%s", resp.StatusCode, emulatorBodyTail(body))
		}
		if !bytes.Contains(bytes.ToLower(body), []byte("live credential")) {
			t.Errorf("the refusal does not say it was about a live credential, so whoever "+
				"reads it will look for the wrong cause:\n%s", emulatorBodyTail(body))
		}

	case "Reach_FindsNoRouteOut":
		err := em.Reach(ctx, opts.ReachAddress)
		if err == nil {
			t.Fatalf("the emulator container opened a connection to %s. This is a security "+
				"finding rather than a bug: the container runs a third party image and "+
				"holds a copy of every request the application made, and it can post them "+
				"anywhere. The inner network is created with Docker's internal flag, and "+
				"that flag is what this behaviour is checking", opts.ReachAddress)
		}
		t.Logf("no route to %s: %v", opts.ReachAddress, err)

	default:
		t.Fatalf("behaviour %s is listed and has no body, so it has never asserted "+
			"anything", name)
	}
}

// emulatorAnswer is what a probe answered, once its body has been read and
// closed.
//
// The behaviours read a status and a content type and nothing else, so handing
// them a live *http.Response would hand every call site a body it has to
// remember to close, six times over, in a suite whose whole job is to be the
// thing other people copy. The response is consumed in one place instead.
type emulatorAnswer struct {
	StatusCode int
	Header     http.Header
}

// emulatorSend builds the probe as an application would and sends it.
//
// The URL is https://<host><path>, built from the emulator's OWN declared
// hosts, because the claim is that the application reaches the provider's
// hostname unmodified. Nothing here knows the emulator's address and nothing
// here may learn it.
//
// It returns a nil answer when it has already failed the test, so a caller
// can return rather than repeat the error.
func emulatorSend(
	ctx context.Context, t *testing.T, em provider.Emulator,
	probe provider.EmulatorProbe, authorization string,
) (*emulatorAnswer, []byte) {
	t.Helper()
	host := probe.Host
	if host == "" {
		hosts := em.Hosts()
		if len(hosts) == 0 {
			t.Fatal("the emulator declares no hosts, so a probe with no host of its own " +
				"has nowhere to be sent")
			return nil, nil
		}
		host = hosts[0]
	}
	method := probe.Method
	if method == "" {
		method = http.MethodGet
	}
	url := "https://" + host + probe.Path
	if probe.Query != "" {
		url += "?" + probe.Query
	}
	var body io.Reader
	if len(probe.Body) > 0 {
		body = bytes.NewReader(probe.Body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		t.Fatalf("building the probe %s %s: %v", method, url, err)
		return nil, nil
	}
	for k, vs := range probe.Header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}

	resp, err := em.RoundTrip(ctx, req)
	if err != nil {
		t.Fatalf("%s %s did not reach the emulator: %v. The application sends exactly this, "+
			"to the provider's own hostname with no endpoint override, so a transport error "+
			"here is the whole claim failing", method, url, err)
		return nil, nil
	}
	defer func() { _ = resp.Body.Close() }()
	read, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		t.Fatalf("reading the response to %s %s: %v", method, url, err)
		return nil, nil
	}
	return &emulatorAnswer{StatusCode: resp.StatusCode, Header: resp.Header}, read
}

// emulatorProbeString renders a probe the way the failure messages quote it.
func emulatorProbeString(em provider.Emulator, probe provider.EmulatorProbe) string {
	host := probe.Host
	if host == "" {
		if hosts := em.Hosts(); len(hosts) > 0 {
			host = hosts[0]
		}
	}
	method := probe.Method
	if method == "" {
		method = http.MethodGet
	}
	s := fmt.Sprintf("%s https://%s%s", method, host, probe.Path)
	if probe.Query != "" {
		s += "?" + probe.Query
	}
	return s
}

// emulatorAdded returns the items in after that were not in before.
func emulatorAdded(before, after []provider.EmulatorStateItem) []provider.EmulatorStateItem {
	had := make(map[provider.EmulatorStateItem]bool, len(before))
	for _, it := range before {
		had[it] = true
	}
	var out []provider.EmulatorStateItem
	for _, it := range after {
		if !had[it] {
			out = append(out, it)
		}
	}
	return out
}

// emulatorItems renders state for a failure message, sorted so that two runs
// of one failure read the same.
func emulatorItems(items []provider.EmulatorStateItem) string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.Kind+" "+it.Name)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

// emulatorBodyTail is as much of a response body as belongs in a failure.
func emulatorBodyTail(body []byte) string {
	const max = 2000
	s := strings.TrimSpace(string(body))
	if s == "" {
		return "  (the body was empty)"
	}
	if len(s) > max {
		s = s[:max] + "\n  ... truncated"
	}
	return "  " + strings.ReplaceAll(s, "\n", "\n  ")
}
