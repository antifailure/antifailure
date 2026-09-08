// Package airgap is the one place this product can open a connection to
// something it did not create.
//
// MIT, like the rest of the engine. This is a socket, not the enterprise
// feature: nothing in the community binary seals it, and with nothing sealed
// every call here is a pass through to the standard library. The enterprise
// binary seals it under a licence, the same way extension.PolicyHook is MIT and
// the organization policy that plugs into it is not.
//
// It exists because air gapped is a claim about ABSENCE, and absence cannot be
// proved by reading call sites. A licensed feature called air_gapped had two
// mentions in this repository, both of them the constant declaring its own
// name, and an installation running it would have reached the internet exactly
// as often as one that was not. What that feature needs is not a flag that
// suppresses the calls somebody remembered; it is a place every call has to go
// through, and a count that comes out of a real run rather than out of a
// reading of the source.
//
// Three decisions are worth the words.
//
// A HOSTNAME IS REFUSED BEFORE IT IS RESOLVED. net.Dialer.Control runs after
// the resolver has already answered, so a guard installed there has already
// sent a DNS query to the network for the name it is about to refuse. That
// query is an outbound packet carrying the name of a host an air gapped
// installation was not supposed to be interested in, which is the leak this
// feature exists to prevent and would have been invisible in the count. So the
// check is on the address string, before anything resolves.
//
// THE ALLOW LIST IS EMPTY BY DEFAULT AND THE OPERATOR FILLS IT. Private address
// ranges are not allowed implicitly. An internal registry on 10.0.0.0/8 is
// reachable because the operator named it, not because the range looked
// harmless: a default that admitted every private address would widen the air
// gap for anybody whose corporate network is flat, and would do it silently.
// Loopback and unix sockets are the exception and are always permitted, because
// the sidecar, the local Postgres and the Docker daemon are addressed there and
// an installation that could not reach them could not run at all.
//
// A REFUSAL IS RECORDED EVEN WHEN NOBODY IS LOOKING. The ledger is the
// measurement. A run that refuses eleven connections and reports success is a
// run whose air gap is doing work nobody can see, and the number in the report
// is the whole reason a buyer believes the mode.
package airgap

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Site names one place in this product that can open a connection.
//
// A string rather than an enum because the sites are spread across packages
// that must not import each other, and the value's only job is to appear in a
// refusal and in the ledger. It is what turns "something tried to phone home"
// into "the release check tried to reach api.github.com", which is the
// difference between a number an operator can act on and one they cannot.
type Site string

// The sites, one per outbound client in the engine and the enterprise edition.
//
// Declared here rather than at each call site so that the list of what this
// product can reach is one list somebody can read, and so that the test that
// walks the source for unguarded clients has something to compare against.
const (
	SiteReleaseCheck    Site = "the release check"
	SiteTelemetry       Site = "the telemetry exporter"
	SiteModelProbe      Site = "the model key probe"
	SiteOracle          Site = "the workflow oracle"
	SitePersonas        Site = "the identity provider seeding"
	SiteControlPlane    Site = "the control plane client"
	SiteControlPlaneID  Site = "the control plane identity discovery"
	SiteDeviceAuth      Site = "the device authorization login"
	SiteLoadTest        Site = "the load generator"
	SiteGoldenS3        Site = "the S3 golden store"
	SiteGoldenAzure     Site = "the Azure Blob golden store"
	SiteNeon            Site = "the Neon control API"
	SiteSupabase        Site = "the Supabase management API"
	SiteDBLab           Site = "the Database Lab API"
	SiteClickHouse      Site = "the ClickHouse HTTP interface"
	SiteServiceProbe    Site = "the service readiness probe"
	SiteWebhookDelivery Site = "the webhook delivery"
	SiteDoctor          Site = "the doctor reachability check"
	SiteCloudAuth       Site = "the cloud credential path"
	SiteConformance     Site = "the runtime conformance suite"
	SiteImagePull       Site = "the container image pull"
	SiteImageBuild      Site = "the container image build"
)

// Attempt is one connection this product tried to make.
type Attempt struct {
	// Site is which part of the product tried.
	Site Site
	// Network is the dial network, or "" for an attempt refused before a dial.
	Network string
	// Address is what it tried to reach. A host and port for a dial, and a
	// description for a refusal made before one.
	Address string
	// At is when.
	At time.Time
	// Refused reports whether the air gap stopped it. An attempt to a permitted
	// address is recorded too, because "we allowed nine connections to the
	// internal registry" is a number an operator wants as much as the refusals.
	Refused bool
}

// String renders one attempt for a report.
func (a Attempt) String() string {
	verb := "reached"
	if a.Refused {
		verb = "was refused reaching"
	}
	return fmt.Sprintf("%s %s %s", a.Site, verb, a.Address)
}

// ErrSealed is what every refused connection returns.
//
// A sentinel so that a caller can tell an air gap refusal from a network that
// happens to be down, which matters: those two want different messages and one
// of them is not a fault.
var ErrSealed = errors.New("air gapped")

var state struct {
	mu       sync.RWMutex
	sealed   bool
	reason   string
	allowed  []rule
	attempts []Attempt
	dropped  int
}

// rule is one entry in the operator's allow list.
type rule struct {
	// raw is what the operator wrote, for the message.
	raw string
	// host matches a hostname exactly, case insensitively.
	host string
	// net matches an address inside a CIDR.
	net *net.IPNet
	// port, when non-empty, requires the port to match too.
	port string
}

// Seal puts this process into air gapped mode.
//
// The reason appears in every refusal, because an operator meeting one of these
// in a log six months from now needs to know which decision produced it.
//
// There is no Unseal for a running process on purpose. A licence that lapses
// mid run must not open the network on an installation that was deployed behind
// an air gap: the expensive direction of that mistake is not "features stopped
// working", it is "the machine in the secure facility started talking to the
// internet because a purchase order was slow". The enterprise binary checks the
// licence once, at startup, and refuses to start rather than starting unsealed.
func Seal(reason string) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.sealed = true
	state.reason = reason
}

// Sealed reports whether this process is air gapped.
func Sealed() bool {
	state.mu.RLock()
	defer state.mu.RUnlock()
	return state.sealed
}

// Reason is why this process was sealed.
func Reason() string {
	state.mu.RLock()
	defer state.mu.RUnlock()
	return state.reason
}

// Allow adds addresses the operator's own network answers for.
//
// Each entry is a hostname, a hostname and port, an IP address, or a CIDR. A
// bare hostname permits every port on it; a CIDR permits every address in it.
// An entry that parses as none of those is refused, because an allow list with
// a typo in it is an allow list that is quietly narrower than the operator
// believes, and they will find out when something they permitted is refused at
// three in the morning.
func Allow(entries ...string) error {
	parsed := make([]rule, 0, len(entries))
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		r, err := parseRule(entry)
		if err != nil {
			return err
		}
		parsed = append(parsed, r)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	state.allowed = append(state.allowed, parsed...)
	return nil
}

// Allowed lists what the operator permitted, as they wrote it.
func Allowed() []string {
	state.mu.RLock()
	defer state.mu.RUnlock()
	out := make([]string, 0, len(state.allowed))
	for _, r := range state.allowed {
		out = append(out, r.raw)
	}
	sort.Strings(out)
	return out
}

func parseRule(entry string) (rule, error) {
	if _, cidr, err := net.ParseCIDR(entry); err == nil {
		return rule{raw: entry, net: cidr}, nil
	}
	// Checked on the WHOLE entry and before the split, which is not fussiness.
	// net.SplitHostPort splits on the last colon and does not validate either
	// half, so "https://registry.internal/v2/" comes back as the host "https"
	// on the port "//registry.internal/v2/" with no error, and a check applied
	// to the host afterwards sees a clean word and admits it. The rule would
	// then match nothing for ever and the operator would never be told.
	if strings.ContainsAny(entry, "/ \t") {
		return rule{}, fmt.Errorf("air gapped: %q is not a host, a host and port, or a CIDR. "+
			"A scheme and a path are not part of an address here", entry)
	}
	host, port := entry, ""
	if h, p, err := net.SplitHostPort(entry); err == nil {
		if _, convErr := strconv.Atoi(p); convErr != nil {
			return rule{}, fmt.Errorf("air gapped: %q ends in %q, which is not a port number", entry, p)
		}
		host, port = h, p
	}
	if host == "" {
		return rule{}, fmt.Errorf("air gapped: %q names no host", entry)
	}
	if ip := net.ParseIP(host); ip != nil {
		bits := 32
		if ip.To4() == nil {
			bits = 128
		}
		return rule{raw: entry, net: &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)}, port: port}, nil
	}
	return rule{raw: entry, host: strings.ToLower(host), port: port}, nil
}

// permits reports whether the allow list covers this host and port.
func (r rule) permits(host, port string) bool {
	if r.port != "" && r.port != port {
		return false
	}
	if r.net != nil {
		ip := net.ParseIP(host)
		return ip != nil && r.net.Contains(ip)
	}
	return r.host != "" && r.host == strings.ToLower(host)
}

// isLoopback reports whether an address is this machine talking to itself.
//
// Always permitted. Every container this engine starts is reached through a
// port published on the loopback interface, the sidecar's admin API is on
// loopback, and a local Postgres is too. Refusing loopback would not make an
// installation more air gapped, it would make it not run.
func isLoopback(host string) bool {
	if host == "" {
		return false
	}
	switch strings.ToLower(host) {
	case "localhost", "localhost.":
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// maxLedger bounds the ledger.
//
// The guard sits on every dial in the process, and a readiness probe polls
// until a service answers, so a slow service on a long af up produces hundreds
// of entries by itself. Unbounded is fine for a command that runs and exits and
// is a leak in anything that embeds the engine and stays up. Ten thousand is
// far more than any lifecycle produces and small enough to be free.
const maxLedger = 10000

// record adds an attempt to the ledger.
//
// Past the cap the entries are counted rather than kept, and the count is
// reported, because a ledger that silently stopped recording would answer
// "no refusals" for the one reason that is indistinguishable from success.
func record(a Attempt) {
	state.mu.Lock()
	defer state.mu.Unlock()
	if len(state.attempts) >= maxLedger {
		state.dropped++
		return
	}
	state.attempts = append(state.attempts, a)
}

// Dropped is how many attempts were counted rather than kept.
//
// Non-zero means Attempts is a sample and not the whole run, which a report has
// to say rather than imply.
func Dropped() int {
	state.mu.RLock()
	defer state.mu.RUnlock()
	return state.dropped
}

// Attempts returns the connections this process tried to make since the last
// Reset, in order.
//
// Recorded only while sealed, and never for a unix socket, so the community
// edition carries no ledger at all and a sealed run's ledger is not buried
// under Docker API calls. Both exclusions are stated here rather than left for
// somebody to infer from a shorter number than they expected.
func Attempts() []Attempt {
	state.mu.RLock()
	defer state.mu.RUnlock()
	out := make([]Attempt, len(state.attempts))
	copy(out, state.attempts)
	return out
}

// Refusals returns only the attempts the air gap stopped.
//
// This is the number the mode is measured by. During a lifecycle that is
// properly air gapped it is empty, and every entry in it is a finding.
func Refusals() []Attempt {
	out := []Attempt{}
	for _, a := range Attempts() {
		if a.Refused {
			out = append(out, a)
		}
	}
	return out
}

// Reset clears the ledger and the seal, FOR TESTS ONLY.
//
// It is exported because the tests that need it are in three packages and two
// modules, and Go has no way to share an unexported hook across a module
// boundary. That makes it the one function in this package that can undo the
// air gap, so it is not left to a comment: the walk in guarded_test.go treats a
// call to it from any file that is not a _test.go as a finding, in the same
// pass and with the same wording as an unguarded client.
//
// This is not a trust boundary and the comment should not pretend it is.
// Anything that can call Reset is compiled into the binary and could equally
// have declined to call Seal. What the check stops is the accident: a helper
// that resets state between operations, written by somebody who had not
// thought about what state this package holds.
func Reset() {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.sealed = false
	state.reason = ""
	state.allowed = nil
	state.attempts = nil
	state.dropped = 0
}

// Check reports whether this site may reach this address, and records the
// attempt either way.
//
// Exported because not every outbound path in this product is an http.Client. A
// container image pull happens in the Docker daemon and an image build happens
// in a builder, neither of them in this process and neither of them reachable
// by a dialer here. Those paths ask this instead, before they hand the work to
// something that would do it outside the guard's reach.
func Check(site Site, network, address string) error {
	state.mu.RLock()
	sealed, reason := state.sealed, state.reason
	rules := make([]rule, len(state.allowed))
	copy(rules, state.allowed)
	state.mu.RUnlock()

	if !sealed {
		return nil
	}
	// unix, unixgram and unixpacket. Permitted and NOT recorded: the Docker
	// daemon is reached this way hundreds of times in one lifecycle, and a
	// ledger full of them would bury the entries that mean something. It is
	// not a network address in the sense this feature is about.
	if strings.HasPrefix(network, "unix") {
		return nil
	}

	host, port := address, ""
	if h, p, err := net.SplitHostPort(address); err == nil {
		host, port = h, p
	}
	host = strings.Trim(host, "[]")

	permitted := isLoopback(host)
	if !permitted {
		for _, r := range rules {
			if r.permits(host, port) {
				permitted = true
				break
			}
		}
	}

	record(Attempt{Site: site, Network: network, Address: address, At: time.Now(), Refused: !permitted})
	if permitted {
		return nil
	}
	return fmt.Errorf("%w: %s tried to reach %s. This installation is air gapped because %s. "+
		"Nothing outside the operator's own network is reachable, and the way to permit a host "+
		"inside it is to name it: %w", ErrSealed, site, address, reason, ErrNotAllowed)
}

// ErrNotAllowed is joined into a refusal so the message can end with the fix
// without the fix being repeated at every call site.
var ErrNotAllowed = errors.New("set AF_AIR_GAPPED_ALLOW to a comma separated list of hosts, host:port pairs or CIDRs")

// Refuse stops an outbound path before it starts, by name.
//
// The difference from Check is that this one is not a dial. It is the paths the
// mode refuses OUTRIGHT rather than pointing somewhere else: a licence
// revocation callout, a telemetry export, a call to a model. Those have no
// address an operator could add to an allow list, because the answer is not
// "reach a different one", it is "an air gapped installation does not do this".
//
// It returns nil when the process is not sealed, so a call site is one if
// statement rather than a mode check plus a branch.
func Refuse(site Site, what string) error {
	state.mu.RLock()
	sealed, reason := state.sealed, state.reason
	state.mu.RUnlock()
	if !sealed {
		return nil
	}
	record(Attempt{Site: site, Address: what, At: time.Now(), Refused: true})
	return fmt.Errorf("%w: %s is refused because this installation is air gapped because %s. "+
		"%s does not happen here, and it does not fall back to a degraded version of itself either, "+
		"because a mode that quietly did less would be indistinguishable from one that worked",
		ErrSealed, what, reason, site)
}

// DialContext is the dialer every outbound client in this product uses.
func DialContext(site Site) func(ctx context.Context, network, address string) (net.Conn, error) {
	var d net.Dialer
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		if err := Check(site, network, address); err != nil {
			return nil, err
		}
		return d.DialContext(ctx, network, address)
	}
}

// Dial is DialContext without a context, for the two callers that have none.
func Dial(site Site, network, address string, timeout time.Duration) (net.Conn, error) {
	if err := Check(site, network, address); err != nil {
		return nil, err
	}
	return net.DialTimeout(network, address, timeout)
}

// Transport returns a NEW transport whose every connection passes the guard.
//
// A clone of http.DefaultTransport rather than a bare &http.Transport{}, so
// that a client built here keeps the standard library's connection pooling,
// proxy handling and idle connection timeout rather than silently losing them.
//
// New on every call, and that is what it is for: the two callers that use this
// rather than Client are the load generator and the conformance suite, and both
// go on to set fields on what they get back. Handing those a shared value would
// be one goroutine writing a transport another is reading.
//
// Everything else wants Client, because a transport is where the connection
// pool lives and a transport per request is a pool of one used once.
func Transport(site Site) *http.Transport {
	// Asserted rather than type asserted outright. http.DefaultTransport is a
	// package variable and test code in this repository and outside it does
	// replace it, and a panic in the one function every outbound client in the
	// product goes through is a poor way to find that out.
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		base = &http.Transport{}
	}
	t := base.Clone()
	t.DialContext = DialContext(site)
	return t
}

// shared holds one transport per site.
//
// The reason is a regression this package caused and then fixed. Several
// clients here are built PER REQUEST: neon, supabase and dblab all call
// c.httpClient().Do(req) on every call to a control API, and those poll an
// operation in a loop. Before the guard they were &http.Client{} values with a
// nil Transport, which means http.DefaultTransport, which is one shared
// singleton with one shared pool, so a new client per request still reused the
// connection. Giving each of them its own cloned transport quietly turned every
// poll into a fresh TCP and TLS handshake and left an idle connection behind
// for the pool to time out.
var shared struct {
	mu sync.Mutex
	m  map[Site]*http.Transport
}

func sharedTransport(site Site) *http.Transport {
	shared.mu.Lock()
	defer shared.mu.Unlock()
	if shared.m == nil {
		shared.m = map[Site]*http.Transport{}
	}
	if t, ok := shared.m[site]; ok {
		return t
	}
	t := Transport(site)
	shared.m[site] = t
	return t
}

// Client returns an http.Client whose every connection passes the guard.
//
// The client is new and the transport under it is shared per site, which is the
// same arrangement the standard library has: an http.Client is a cheap value
// holding a timeout and a policy, and the pool belongs to the transport. Two
// clients for the same site reuse each other's connections, which is what the
// code this replaced got for free from http.DefaultTransport.
func Client(site Site, timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: sharedTransport(site)}
}

// CheckImage reports whether this installation may fetch a container image.
//
// The pull happens in the Docker daemon, in another process, over a connection
// this package's dialer never sees, so the guard cannot refuse it at the
// socket. What it can do is refuse it before it is asked for, against the
// registry the reference names, which is the same answer arrived at one step
// earlier.
//
// Both callers inspect the local daemon for the image first and only reach here
// when it is absent, so an air gapped installation that has loaded its images
// from a tarball or an internal registry runs untouched. What is refused is the
// silent reach for Docker Hub, which is the one that happens on a machine
// somebody believed had no route out.
func CheckImage(site Site, ref string) error {
	return Check(site, "tcp", RegistryHost(ref)+":443")
}

// RegistryHost is the registry a Docker image reference names.
//
// Docker's own rule and it is worth stating because it surprises people: the
// registry is the first path component ONLY when it looks like a host, meaning
// it contains a dot or a colon or is exactly localhost. So
// clickhouse/clickhouse-server is Docker Hub under an organization called
// clickhouse, and registry.internal/clickhouse-server is an internal registry.
// A reference with no registry at all, like postgres:17, is Docker Hub too.
func RegistryHost(ref string) string {
	const dockerHub = "registry-1.docker.io"
	// A digest or a tag can carry a colon, so only the part before the first
	// slash is considered.
	first := ref
	if i := strings.IndexByte(ref, '/'); i >= 0 {
		first = ref[:i]
	} else {
		return dockerHub
	}
	if first == "localhost" || strings.ContainsAny(first, ".:") {
		return first
	}
	return dockerHub
}

// LookupHost resolves a name, refusing when sealed.
//
// A resolver call is an outbound packet in its own right and it is the one
// people forget, because it does not look like a connection: nothing in the
// code says http or dial, and the name being looked up is exactly the
// information an air gapped installation was not supposed to disclose. The
// dialer here refuses a hostname before resolving it for the same reason, so
// this covers the callers that resolve without dialing.
func LookupHost(site Site, host string) ([]string, error) {
	if err := Check(site, "dns", net.JoinHostPort(host, "53")); err != nil {
		return nil, err
	}
	return net.LookupHost(host)
}
