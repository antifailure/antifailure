package conformance

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// RuntimeFactory builds a runtime for one behavior. Each gets its own, so a
// runtime holding state cannot let one behavior's leftovers change another's
// result.
type RuntimeFactory func(t *testing.T) provider.Runtime

// RuntimeOptions configure a run.
type RuntimeOptions struct {
	// Timeout bounds each behavior. Zero uses five minutes, which is generous
	// for containers on this machine and tight enough that a pod stuck
	// Pending fails the test rather than the job.
	//
	// It is the only bound. Every wait in the suite is a select on the
	// behavior's context, deliberately: a wait that invents its own deadline
	// cannot be shortened by the caller, and a runtime that never reports an
	// exit code makes a dozen behaviors each sit out the full timeout. That
	// turned the self test into a four minute hang before the waits here were
	// made to honour it.
	Timeout time.Duration
	// SkipSlow omits the behaviors that bring up two environments.
	SkipSlow bool
	// ShellImage is an image holding a POSIX shell, wget, and nslookup. The
	// default is Alpine, which has all three through busybox.
	//
	// Every behavior that has to observe something from inside an environment
	// runs a command in this image and reads the exit code back out, because
	// that is the only observation both a container runtime and a cluster
	// report the same way.
	ShellImage string
	// PrepareImage makes an image available to the runtime before it is used.
	//
	// It exists because "the image is present" means different things in
	// different places: on the local daemon it is a pull, and on a cluster it
	// is a pull by every node that might run the pod, or a load into the
	// node's own store. The suite will not guess, so a runtime whose images
	// have to be put somewhere says how here. Nil means they are already
	// wherever they need to be.
	PrepareImage func(ctx context.Context, ref string) error
	// AllowedHost is a real host the egress behaviors allow by rule, and
	// RefusedHost is a real host they never name.
	//
	// Both are real on purpose. If the allowed host were fictional, every
	// egress behavior would pass with the sidecar removed entirely, because
	// nothing would be reachable either way. The pair is what distinguishes
	// "the policy refused it" from "the machine has no internet".
	AllowedHost string
	RefusedHost string
}

// RuntimeBehaviors returns the name and one sentence description of every
// behavior the runtime suite checks, sorted.
func RuntimeBehaviors() []Behavior {
	out := append([]Behavior(nil), runtimeBehaviors...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// runtimeBehaviors is the contract a runtime has to meet.
//
// Read the Requires column carefully. Three behaviors are skippable, and they
// are the three that describe a convenience: reaching a service from the
// machine that started it, reading logs back, and putting a local database
// container on the environment's network. Nothing about containment is
// skippable. A runtime cannot declare its way out of Egress_ anything, because
// a runtime that lets an environment reach the internet is not a runtime this
// product has, and a capability flag that turned those off would be a
// supported way to ship one.
var runtimeBehaviors = []Behavior{
	{"Capabilities_MatchWhatIsImplemented", "The declared capabilities agree with the interfaces the runtime implements.", ""},
	{"Name_IsNotEmpty", "The runtime names itself, because errors and inventories quote it.", ""},

	{"Up_RefusesAnEnvironmentWithNoID", "Up with no environment id fails rather than creating something unattributable.", ""},
	{"Up_StartsAServiceAndReportsIt", "A service that was asked for is running and named in the result.", ""},
	{"Up_ReportsAReachableURL", "A web service answers at the URL the runtime reports.", "ingress"},
	{"Up_IsIdempotentForOneEnvironment", "Bringing one environment up twice leaves one environment, not two.", ""},
	{"Up_StartsDependenciesFirst", "A service does not start before something it depends on.", ""},
	{"Up_ReportsACycleRatherThanHanging", "A dependency cycle fails with AF-RUN-041 instead of deadlocking.", ""},
	{"Up_ReportsAMissingDependency", "Depending on a service that was never declared fails with AF-RUN-042.", ""},
	{"Up_DoesNotStartAServiceWhoseMigrationFailed", "A failed migration stops the service it belongs to from starting at all.", ""},
	{"Up_LeavesAFailedServiceFindable", "A service that exits immediately is still reported, so teardown can remove it and logs can explain it.", ""},
	{"Up_CreatesNothingTheJournalRefused", "When the journal refuses, Up fails and the environment holds no resources.", ""},
	{"Up_JournalsResourcesTeardownCanFind", "Every name the runtime journals identifies a resource the inventory reports.", ""},

	{"Status_ReportsRunningServices", "Status names what is running and reports it ready.", ""},
	{"Status_ReportsAnExitCode", "A service that has finished carries the code it exited with.", ""},
	{"Status_OfAnUnknownEnvironmentIsEmpty", "Asking about an environment that was never created is empty, not an error.", ""},

	{"Down_RemovesEverythingItCreated", "Teardown removes what Up made and says how much.", ""},
	{"Down_OfSomethingNeverUpSucceeds", "Tearing down an environment that never existed is not an error, because teardown retries.", ""},
	{"Down_IsIdempotent", "Tearing down twice is not an error and leaves nothing pending.", ""},
	{"Down_TouchesOnlyItsOwnEnvironment", "Tearing one environment down leaves another running.", ""},

	{"Inventory_ListsLiveResources", "Inventory reports what exists, which is what the leak detector compares against.", ""},
	{"Inventory_AttributesResourcesToEnvironments", "Every resource inventory reports names the environment it belongs to.", ""},

	{"Egress_NoPolicyMeansNothingGetsOut", "An environment with no egress section reaches nothing.", ""},
	{"Egress_AllowedHostIsReached", "A host the policy allows is reachable through the sidecar.", ""},
	{"Egress_HostWithNoRuleIsRefused", "A host the policy does not name is not reachable.", ""},
	{"Egress_AppliesToAClientThatIgnoresProxyVariables", "The decision does not depend on the client reading its proxy variables.", ""},
	{"Egress_CannotBeBypassedByAddress", "Connecting to a public address rather than a name does not get out.", ""},
	{"Egress_CannotReachTheMetadataEndpoint", "169.254.169.254 is not reachable, whatever the policy says.", ""},
	{"Egress_CannotBeBypassedByUDP", "A UDP query straight to a public resolver does not get out.", ""},
	{"Egress_NamesDoNotCrossEnvironments", "A service name resolves to this environment's service and never to another's.", ""},

	{"Logs_ReturnWhatAServiceWrote", "Logs return a line the service printed.", "logs"},
}

// RunRuntime runs the whole suite against a runtime.
func RunRuntime(t *testing.T, factory RuntimeFactory, opts RuntimeOptions) {
	t.Helper()
	if opts.Timeout <= 0 {
		opts.Timeout = 5 * time.Minute
	}
	if opts.ShellImage == "" {
		opts.ShellImage = DefaultShellImage
	}
	if opts.AllowedHost == "" {
		opts.AllowedHost = DefaultAllowedHost
	}
	if opts.RefusedHost == "" {
		opts.RefusedHost = DefaultRefusedHost
	}

	probe := factory(t)
	caps := probe.Capabilities()
	name := probe.Name()
	_ = probe.Close()

	if opts.PrepareImage != nil {
		ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
		if err := opts.PrepareImage(ctx, opts.ShellImage); err != nil {
			cancel()
			t.Fatalf("the suite could not make %s available to the runtime: %v", opts.ShellImage, err)
		}
		cancel()
	}

	// What the runtime already held before the suite ran. The assertion at the
	// end is that the suite left nothing new of its own, not that the machine
	// is empty: a shared daemon or a shared cluster carries other people's
	// environments, and failing on those makes the check something people
	// learn to ignore.
	before := runtimeSnapshot(t, factory)
	envs := newEnvSet()
	// One suffix for the whole run, so every environment this run creates is
	// distinguishable from one another run left behind, and so a failure can
	// be traced to the run that caused it.
	runID := shortID()

	for _, b := range runtimeBehaviors {
		b := b
		t.Run(b.Name, func(t *testing.T) {
			if reason := runtimeSkipReason(b, caps, opts); reason != "" {
				// Named, never silent. A reviewer reading the output has to be
				// able to see exactly which guarantee this runtime does not
				// make.
				t.Skipf("skipped: %s does not declare %s", name, reason)
			}
			ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
			defer cancel()
			runRuntimeBehavior(ctx, t, b.Name, factory, opts, envs, runID)
		})
	}

	if t.Failed() {
		// A failing behavior legitimately leaves things behind for
		// inspection, and reporting that as a second failure buries the first.
		return
	}
	for res, owner := range runtimeSnapshot(t, factory) {
		if _, wasThere := before[res]; wasThere || !envs.has(owner) {
			continue
		}
		t.Errorf("the suite left %s behind; every resource a behavior creates must be removed "+
			"when it finishes, whether it passed or not", res)
	}
}

// DefaultShellImage is small, has a shell, and is on every machine that has
// ever run this test suite.
const DefaultShellImage = "alpine:3.20"

// DefaultAllowedHost and DefaultRefusedHost are two real hosts. They are real
// so that a refusal can be told apart from a machine with no internet, and
// they are boring so that neither is ever the interesting part of a failure.
const (
	DefaultAllowedHost = "example.com"
	DefaultRefusedHost = "example.org"
)

// runtimeSnapshot records what the runtime owns, as comparable strings mapped
// to the environment each belongs to.
func runtimeSnapshot(t *testing.T, factory RuntimeFactory) map[string]string {
	t.Helper()
	r := factory(t)
	defer func() { _ = r.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	items, err := r.Inventory(ctx)
	if err != nil {
		t.Fatalf("Inventory: %v", err)
	}
	out := make(map[string]string, len(items))
	for _, res := range items {
		out[res.Kind+" "+res.ID] = res.EnvID
	}
	return out
}

// envSet records the environments one run of the suite created, so the leak
// check can tell the suite's own leftovers from anything that was already
// there or that another test package created while it ran.
type envSet struct {
	mu  sync.Mutex
	ids map[string]bool
}

func newEnvSet() *envSet { return &envSet{ids: map[string]bool{}} }

func (s *envSet) add(id string) {
	if id == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ids[id] = true
}

func (s *envSet) has(id string) bool {
	if id == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ids[id]
}

func runtimeSkipReason(b Behavior, caps provider.RuntimeCaps, opts RuntimeOptions) string {
	switch b.Requires {
	case "ingress":
		if !caps.Ingress {
			return "an ingress a caller can reach"
		}
	case "logs":
		if !caps.Logs {
			return "log reading"
		}
	}
	if opts.SkipSlow {
		switch b.Name {
		case "Down_TouchesOnlyItsOwnEnvironment", "Egress_NamesDoNotCrossEnvironments":
			return "a run configured to skip slow behaviors"
		}
	}
	return ""
}

// shortID is a suffix unique to one run of the suite.
func shortID() string {
	n := time.Now().UnixNano()
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	var b [6]byte
	for i := range b {
		b[i] = alphabet[n%int64(len(alphabet))]
		n /= int64(len(alphabet))
	}
	return string(b[:])
}

// rtHarness gives one behavior a runtime and the helpers it needs.
type rtHarness struct {
	t     *testing.T
	r     provider.Runtime
	opts  RuntimeOptions
	envs  *envSet
	runID string
}

func runRuntimeBehavior(
	ctx context.Context,
	t *testing.T,
	name string,
	factory RuntimeFactory,
	opts RuntimeOptions,
	envs *envSet,
	runID string,
) {
	h := &rtHarness{t: t, r: factory(t), opts: opts, envs: envs, runID: runID}
	t.Cleanup(func() { _ = h.r.Close() })

	switch name {
	case "Capabilities_MatchWhatIsImplemented":
		h.capabilitiesMatchWhatIsImplemented()
	case "Name_IsNotEmpty":
		h.nameIsNotEmpty()

	case "Up_RefusesAnEnvironmentWithNoID":
		h.upRefusesAnEnvironmentWithNoID(ctx)
	case "Up_StartsAServiceAndReportsIt":
		h.upStartsAServiceAndReportsIt(ctx)
	case "Up_ReportsAReachableURL":
		h.upReportsAReachableURL(ctx)
	case "Up_IsIdempotentForOneEnvironment":
		h.upIsIdempotent(ctx)
	case "Up_StartsDependenciesFirst":
		h.upStartsDependenciesFirst(ctx)
	case "Up_ReportsACycleRatherThanHanging":
		h.upReportsACycle(ctx)
	case "Up_ReportsAMissingDependency":
		h.upReportsAMissingDependency(ctx)
	case "Up_DoesNotStartAServiceWhoseMigrationFailed":
		h.upDoesNotStartAServiceWhoseMigrationFailed(ctx)
	case "Up_LeavesAFailedServiceFindable":
		h.upLeavesAFailedServiceFindable(ctx)
	case "Up_CreatesNothingTheJournalRefused":
		h.upCreatesNothingTheJournalRefused(ctx)
	case "Up_JournalsResourcesTeardownCanFind":
		h.upJournalsResourcesTeardownCanFind(ctx)

	case "Status_ReportsRunningServices":
		h.statusReportsRunningServices(ctx)
	case "Status_ReportsAnExitCode":
		h.statusReportsAnExitCode(ctx)
	case "Status_OfAnUnknownEnvironmentIsEmpty":
		h.statusOfAnUnknownEnvironmentIsEmpty(ctx)

	case "Down_RemovesEverythingItCreated":
		h.downRemovesEverythingItCreated(ctx)
	case "Down_OfSomethingNeverUpSucceeds":
		h.downOfSomethingNeverUpSucceeds(ctx)
	case "Down_IsIdempotent":
		h.downIsIdempotent(ctx)
	case "Down_TouchesOnlyItsOwnEnvironment":
		h.downTouchesOnlyItsOwn(ctx)

	case "Inventory_ListsLiveResources":
		h.inventoryListsLiveResources(ctx)
	case "Inventory_AttributesResourcesToEnvironments":
		h.inventoryAttributesResources(ctx)

	case "Egress_NoPolicyMeansNothingGetsOut":
		h.egressNoPolicyMeansNothingGetsOut(ctx)
	case "Egress_AllowedHostIsReached":
		h.egressAllowedHostIsReached(ctx)
	case "Egress_HostWithNoRuleIsRefused":
		h.egressHostWithNoRuleIsRefused(ctx)
	case "Egress_AppliesToAClientThatIgnoresProxyVariables":
		h.egressAppliesWithoutProxyVariables(ctx)
	case "Egress_CannotBeBypassedByAddress":
		h.egressCannotBeBypassedByAddress(ctx)
	case "Egress_CannotReachTheMetadataEndpoint":
		h.egressCannotReachMetadata(ctx)
	case "Egress_CannotBeBypassedByUDP":
		h.egressCannotBeBypassedByUDP(ctx)
	case "Egress_NamesDoNotCrossEnvironments":
		h.egressNamesDoNotCrossEnvironments(ctx)

	case "Logs_ReturnWhatAServiceWrote":
		h.logsReturnWhatAServiceWrote(ctx)

	default:
		t.Fatalf("conformance: no implementation for runtime behavior %q", name)
	}
}

// envID gives one behavior its own environment and guarantees teardown, even
// when the assertion under test is what failed.
func (h *rtHarness) envID(name string) string {
	h.t.Helper()
	id := "afc" + name + h.runID
	h.envs.add(id)
	h.t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		_, _ = h.r.Down(ctx, id)
	})
	return id
}

// serve is a command that answers one line of HTTP forever, using only
// busybox, so that a behavior needing a web service does not need an image
// built for it. An image built here would have to be built on the machine
// running the test and then put wherever the runtime can reach it, which is
// exactly the step that differs between a daemon and a cluster.
func serve(body string) string {
	return fmt.Sprintf(
		`while true; do printf 'HTTP/1.1 200 OK\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s' `+
			`| nc -l -p 8080 -s 0.0.0.0; done`, len(body), body)
}

// webService is a service that serves body on port 8080.
func (h *rtHarness) webService(name, body string) provider.ServiceSpec {
	return provider.ServiceSpec{
		Name: name, Image: h.opts.ShellImage, Kind: "web", Port: 8080,
		Command: serve(body),
	}
}

// worker is a service that runs command and stops.
func (h *rtHarness) worker(name, command string) provider.ServiceSpec {
	return provider.ServiceSpec{
		Name: name, Image: h.opts.ShellImage, Kind: "worker", Command: command,
	}
}

// up brings an environment up and reports the error rather than failing, so a
// behavior can assert on it.
func (h *rtHarness) up(ctx context.Context, spec provider.EnvSpec) (provider.Env, error) {
	h.t.Helper()
	if spec.Journal == nil {
		spec.Journal = func(string, string) error { return nil }
	}
	return h.r.Up(ctx, spec)
}

// mustUp brings an environment up and fails the behavior if it did not.
func (h *rtHarness) mustUp(ctx context.Context, spec provider.EnvSpec) provider.Env {
	h.t.Helper()
	env, err := h.up(ctx, spec)
	if err != nil {
		h.t.Fatalf("Up: %v", err)
	}
	return env
}

// reached is the exit code a probe uses to say it got through, and refused is
// the one it uses to say it did not. Two explicit codes rather than the
// command's own, because busybox wget reports several different non-zero codes
// for what is one answer here, and because a probe that never ran at all exits
// with something that is neither.
const (
	reached = 0
	refused = 9
)

// probeCmd wraps a command so that it exits reached or refused and nothing
// else.
func probeCmd(command string) string {
	return command + " >/dev/null 2>&1 && exit 0 || exit 9"
}

// retrying turns a command into one that keeps trying for half a minute
// before it gives up, and still exits reached or refused and nothing else.
//
// Only for probes aimed at a service inside the environment. A probe aimed
// outward must never retry: the answer there is the policy's decision, which
// does not change, and retrying a refusal only makes a failing test slow.
func retrying(command string) string {
	return "i=0; while [ $i -lt 30 ]; do " + command +
		" >/dev/null 2>&1 && exit 0; i=$((i+1)); sleep 1; done; exit 9"
}

// noProxyVars strips the proxy variables, so the command that follows makes
// the request a client with no proxy support would make.
const noProxyVars = "env -u http_proxy -u https_proxy -u HTTP_PROXY -u HTTPS_PROXY "

// probe runs one command inside an environment and returns its exit code.
//
// The Up error is deliberately not asserted on. A runtime is allowed to report
// a service that exited immediately as a failure to bring the environment up,
// and it is allowed to report it as an environment that came up with a service
// in a terminal state. Both are honest, both are things the two runtimes here
// actually do, and the thing this suite is asking about is the exit code
// either way. What is not allowed is losing the container, and that is
// covered by Up_LeavesAFailedServiceFindable rather than by silence here.
func (h *rtHarness) probe(ctx context.Context, envID string, egress *schema.Egress, command string) int {
	h.t.Helper()
	_, _ = h.up(ctx, provider.EnvSpec{
		EnvID:    envID,
		Egress:   egress,
		Services: []provider.ServiceSpec{h.worker("prober", probeCmd(command))},
	})
	return h.waitForExit(ctx, envID, "prober")
}

// waitForExit blocks until a service reports the code it exited with.
func (h *rtHarness) waitForExit(ctx context.Context, envID, service string) int {
	h.t.Helper()
	for {
		env, err := h.r.Status(ctx, envID)
		if err != nil {
			h.t.Fatalf("Status while waiting for %s to finish: %v", service, err)
		}
		for _, s := range env.Services {
			if s.Name == service && s.ExitCode != nil {
				return *s.ExitCode
			}
		}
		select {
		case <-ctx.Done():
			h.t.Fatalf("%s never finished; Status must report an exit code for a service "+
				"that has stopped, or nothing can tell a refused request from a slow one", service)
			return -1
		case <-time.After(time.Second):
		}
	}
}

// waitForReady blocks until a service reports ready.
func (h *rtHarness) waitForReady(ctx context.Context, envID, service string) provider.RunningService {
	h.t.Helper()
	for {
		env, err := h.r.Status(ctx, envID)
		if err != nil {
			h.t.Fatalf("Status while waiting for %s: %v", service, err)
		}
		for _, s := range env.Services {
			if s.Name == service && s.Ready {
				return s
			}
		}
		select {
		case <-ctx.Done():
			h.t.Fatalf("%s never became ready", service)
			return provider.RunningService{}
		case <-time.After(time.Second):
		}
	}
}

// codedIs reports whether err carries the given code, and says what it carried
// instead when it does not.
func (h *rtHarness) codedIs(err error, want aferrors.Code, what string) {
	h.t.Helper()
	if err == nil {
		h.t.Fatalf("%s: expected %s, got no error at all", what, want)
	}
	var coded *aferrors.Error
	if !aferrors.As(err, &coded) {
		h.t.Fatalf("%s: expected %s, got an uncoded error: %v", what, want, err)
	}
	if coded.Code() != want {
		h.t.Fatalf("%s: expected %s, got %s: %s", what, want, coded.Code(), coded.Message())
	}
}

// resourcesFor returns the inventory entries belonging to one environment.
func (h *rtHarness) resourcesFor(ctx context.Context, envID string) []provider.Resource {
	h.t.Helper()
	all, err := h.r.Inventory(ctx)
	if err != nil {
		h.t.Fatalf("Inventory: %v", err)
	}
	var out []provider.Resource
	for _, res := range all {
		if res.EnvID == envID {
			out = append(out, res)
		}
	}
	return out
}

// --- contract ------------------------------------------------------------

func (h *rtHarness) nameIsNotEmpty() {
	if strings.TrimSpace(h.r.Name()) == "" {
		h.t.Error("Name returned nothing; it is quoted in errors, in af status, and in " +
			"the inventory, and an unnamed runtime makes all three unreadable")
	}
}

func (h *rtHarness) capabilitiesMatchWhatIsImplemented() {
	caps := h.r.Capabilities()
	_, readsLogs := h.r.(provider.LogReader)
	if caps.Logs && !readsLogs {
		h.t.Error("Capabilities declares Logs, but the runtime does not implement " +
			"provider.LogReader, so af logs would find nothing to call")
	}
	if !caps.Logs && readsLogs {
		h.t.Error("the runtime implements provider.LogReader but declares Logs false, " +
			"so a working capability is switched off and the behavior that would " +
			"prove it is skipped")
	}
}

// --- up ------------------------------------------------------------------

func (h *rtHarness) upRefusesAnEnvironmentWithNoID(ctx context.Context) {
	_, err := h.up(ctx, provider.EnvSpec{
		Services: []provider.ServiceSpec{h.worker("orphan", "true")},
	})
	// Every resource is labelled with the environment id, and teardown finds
	// resources by that label. Something created without one can never be
	// found again by anything, which is the definition of a leak.
	h.codedIs(err, aferrors.AFRUN040, "Up with no environment id")
}

func (h *rtHarness) upStartsAServiceAndReportsIt(ctx context.Context) {
	id := h.envID("up1")
	env := h.mustUp(ctx, provider.EnvSpec{
		EnvID:    id,
		Services: []provider.ServiceSpec{h.webService("web", "hello")},
	})
	if env.EnvID != id {
		h.t.Errorf("Up reported environment %q, asked for %q", env.EnvID, id)
	}
	if !env.ProxyReady {
		h.t.Error("Up reported the environment up with no egress sidecar running; " +
			"an environment with no sidecar has no policy, and reporting it as up " +
			"is how an unprotected environment gets used")
	}
	found := false
	for _, s := range env.Services {
		if s.Name == "web" {
			found = true
		}
	}
	if !found {
		h.t.Errorf("Up did not report the service it was asked for; got %v", env.Services)
	}
	h.waitForReady(ctx, id, "web")
}

func (h *rtHarness) upReportsAReachableURL(ctx context.Context) {
	id := h.envID("url1")
	const body = "conformance-reachable"
	h.mustUp(ctx, provider.EnvSpec{
		EnvID:    id,
		Services: []provider.ServiceSpec{h.webService("web", body)},
	})
	svc := h.waitForReady(ctx, id, "web")
	if svc.URL == "" {
		h.t.Fatal("the runtime declares ingress but reported no URL for a web service")
	}
	got := httpGet(h.t, ctx, svc.URL)
	if !strings.Contains(got, body) {
		h.t.Errorf("the URL the runtime reported served %q, not the service; a URL that "+
			"does not reach the service is worse than none, because af up prints it "+
			"and a pull request comment links to it", got)
	}
}

func (h *rtHarness) upIsIdempotent(ctx context.Context) {
	id := h.envID("idem1")
	spec := provider.EnvSpec{
		EnvID:    id,
		Services: []provider.ServiceSpec{h.webService("web", "idem")},
	}
	h.mustUp(ctx, spec)
	first := len(h.resourcesFor(ctx, id))
	h.mustUp(ctx, spec)
	second := len(h.resourcesFor(ctx, id))
	if second != first {
		h.t.Errorf("bringing one environment up twice left %d resources where the first "+
			"left %d; af up is run again after a failure all the time, and a second "+
			"run that duplicates everything leaves half of it unreferenced", second, first)
	}
}

func (h *rtHarness) upStartsDependenciesFirst(ctx context.Context) {
	id := h.envID("dep1")
	var order []string
	_, err := h.up(ctx, provider.EnvSpec{
		EnvID: id,
		Services: []provider.ServiceSpec{
			// Declared in the wrong order on purpose, so passing means the
			// runtime sorted them rather than that the manifest happened to
			// list them usefully.
			{Name: "second", Image: h.opts.ShellImage, Kind: "worker",
				Command: "sleep 30", DependsOn: []string{"first"}},
			{Name: "first", Image: h.opts.ShellImage, Kind: "worker", Command: "sleep 30"},
		},
		Journal: func(kind, resource string) error {
			order = append(order, resource)
			return nil
		},
	})
	if err != nil {
		h.t.Fatalf("Up: %v", err)
	}
	firstAt, secondAt := -1, -1
	for i, resource := range order {
		if firstAt < 0 && strings.Contains(resource, "first") {
			firstAt = i
		}
		if secondAt < 0 && strings.Contains(resource, "second") {
			secondAt = i
		}
	}
	if firstAt < 0 || secondAt < 0 {
		h.t.Fatalf("the journal did not name both services; it recorded %v", order)
	}
	if firstAt > secondAt {
		h.t.Errorf("the service that was depended on was journaled after the one that "+
			"depends on it: %v", order)
	}
}

func (h *rtHarness) upReportsACycle(ctx context.Context) {
	id := h.envID("cyc1")
	_, err := h.up(ctx, provider.EnvSpec{
		EnvID: id,
		Services: []provider.ServiceSpec{
			{Name: "a", Image: h.opts.ShellImage, Kind: "worker", DependsOn: []string{"b"}},
			{Name: "b", Image: h.opts.ShellImage, Kind: "worker", DependsOn: []string{"a"}},
		},
	})
	// The alternative to reporting it is waiting forever for a service that
	// can never start, which reads as a hung af up with no explanation.
	h.codedIs(err, aferrors.AFRUN041, "a dependency cycle")
}

func (h *rtHarness) upReportsAMissingDependency(ctx context.Context) {
	id := h.envID("miss1")
	_, err := h.up(ctx, provider.EnvSpec{
		EnvID: id,
		Services: []provider.ServiceSpec{
			{Name: "a", Image: h.opts.ShellImage, Kind: "worker", DependsOn: []string{"nothere"}},
		},
	})
	h.codedIs(err, aferrors.AFRUN042, "a dependency on a service that was never declared")
}

func (h *rtHarness) upDoesNotStartAServiceWhoseMigrationFailed(ctx context.Context) {
	id := h.envID("mig1")
	_, err := h.up(ctx, provider.EnvSpec{
		EnvID: id,
		Services: []provider.ServiceSpec{{
			Name: "web", Image: h.opts.ShellImage, Kind: "worker",
			Command: "sleep 60", Migrate: "exit 3",
		}},
	})
	if err == nil {
		h.t.Fatal("a migration that failed was reported as success")
	}
	// The property that matters is not the error. It is that the application
	// never ran against a database whose migration did not finish, which is
	// the state that corrupts data rather than merely failing.
	env, statusErr := h.r.Status(ctx, id)
	if statusErr != nil {
		h.t.Fatalf("Status: %v", statusErr)
	}
	for _, s := range env.Services {
		if s.Name == "web" && s.Ready {
			h.t.Error("the service started even though its migration failed")
		}
	}
}

func (h *rtHarness) upLeavesAFailedServiceFindable(ctx context.Context) {
	id := h.envID("fail1")
	// Reported through Up's return value or not, the container has to exist:
	// it holds the logs that say why it exited, and teardown finds it by
	// label. A runtime that rolled it back would delete the evidence.
	_, _ = h.up(ctx, provider.EnvSpec{
		EnvID:    id,
		Services: []provider.ServiceSpec{h.worker("crasher", "exit 7")},
	})
	if code := h.waitForExit(ctx, id, "crasher"); code != 7 {
		h.t.Errorf("a service that exited 7 was reported as exiting %d", code)
	}
	if len(h.resourcesFor(ctx, id)) == 0 {
		h.t.Error("nothing was left in the inventory for an environment whose service " +
			"failed; the container that failed is the evidence, and teardown finds " +
			"resources through the inventory")
	}
}

func (h *rtHarness) upCreatesNothingTheJournalRefused(ctx context.Context) {
	id := h.envID("jrn1")
	_, err := h.up(ctx, provider.EnvSpec{
		EnvID:    id,
		Services: []provider.ServiceSpec{h.webService("web", "journal")},
		Journal: func(string, string) error {
			return fmt.Errorf("the journal is unwritable")
		},
	})
	if err == nil {
		h.t.Fatal("Up ignored a journal that refused to record anything")
	}
	// A resource created before it was recorded is a resource teardown cannot
	// find. That is why the journal runs first, and why refusing it has to
	// stop the create rather than be logged and stepped over.
	if got := h.resourcesFor(ctx, id); len(got) != 0 {
		h.t.Errorf("the journal refused every record and %d resources were created "+
			"anyway: %v", len(got), got)
	}
}

func (h *rtHarness) upJournalsResourcesTeardownCanFind(ctx context.Context) {
	id := h.envID("jrn2")
	var recorded []string
	h.mustUp(ctx, provider.EnvSpec{
		EnvID:    id,
		Services: []provider.ServiceSpec{h.worker("w", "sleep 120")},
		Journal: func(_, resource string) error {
			recorded = append(recorded, resource)
			return nil
		},
	})
	if len(recorded) == 0 {
		h.t.Fatal("Up journalled nothing; the journal is the only record of a resource " +
			"created before the process was interrupted, and a runtime that writes " +
			"none has nothing to reconcile against")
	}
	// The journal is not a log. Its entries are what a later teardown looks
	// resources up by, so an entry naming something no inventory reports is a
	// record that can never be acted on: the resource it stands for is either
	// invisible to the leak detector or unreachable by the thing meant to
	// remove it. Recorded before creation, so this runs against an Up that
	// succeeded, where every entry does stand for something real.
	inventory := h.resourcesFor(ctx, id)
	for _, name := range recorded {
		if !mentions(inventory, name) {
			h.t.Errorf("the journal recorded %q and nothing in the inventory for this "+
				"environment identifies it; %d resources were reported: %v",
				name, len(inventory), inventory)
		}
	}
}

// mentions reports whether any resource is identifiable by a journalled name.
//
// Loose on purpose. A runtime journals the name it is about to create and the
// inventory reports whatever that thing ended up being addressed by, which for
// Docker is a content hash with the name alongside it as a label. Demanding
// equality would fail every runtime for a difference that is not the point.
func mentions(resources []provider.Resource, name string) bool {
	for _, res := range resources {
		if res.ID == name || strings.Contains(res.ID, name) {
			return true
		}
		for _, value := range res.Labels {
			if strings.Contains(value, name) {
				return true
			}
		}
	}
	return false
}

// --- status --------------------------------------------------------------

func (h *rtHarness) statusReportsRunningServices(ctx context.Context) {
	id := h.envID("st1")
	h.mustUp(ctx, provider.EnvSpec{
		EnvID:    id,
		Services: []provider.ServiceSpec{h.webService("web", "status")},
	})
	svc := h.waitForReady(ctx, id, "web")
	if svc.Kind != "web" {
		h.t.Errorf("Status reported service kind %q for a web service; af up reads the "+
			"kind back to decide which service to print a URL for, so losing it "+
			"means an environment that is running reports no address", svc.Kind)
	}
	if svc.State == "" {
		h.t.Error("Status reported no state for a running service")
	}
}

func (h *rtHarness) statusReportsAnExitCode(ctx context.Context) {
	id := h.envID("st2")
	_, _ = h.up(ctx, provider.EnvSpec{
		EnvID:    id,
		Services: []provider.ServiceSpec{h.worker("done", "exit 5")},
	})
	if code := h.waitForExit(ctx, id, "done"); code != 5 {
		h.t.Errorf("a service that exited 5 was reported as exiting %d", code)
	}
	// A running service must not carry one, or nothing can tell "finished
	// with this code" from "still going".
	id2 := h.envID("st3")
	h.mustUp(ctx, provider.EnvSpec{
		EnvID:    id2,
		Services: []provider.ServiceSpec{h.worker("running", "sleep 120")},
	})
	env, err := h.r.Status(ctx, id2)
	if err != nil {
		h.t.Fatalf("Status: %v", err)
	}
	for _, s := range env.Services {
		if s.Name == "running" && s.ExitCode != nil {
			h.t.Errorf("a service that is still running reported exit code %d", *s.ExitCode)
		}
	}
}

func (h *rtHarness) statusOfAnUnknownEnvironmentIsEmpty(ctx context.Context) {
	env, err := h.r.Status(ctx, "afcnothinghere"+h.runID)
	if err != nil {
		h.t.Fatalf("Status of an environment that was never created should be empty, "+
			"not an error: %v", err)
	}
	if len(env.Services) != 0 {
		h.t.Errorf("Status invented %d services for an environment that never existed", len(env.Services))
	}
}

// --- down ----------------------------------------------------------------

func (h *rtHarness) downRemovesEverythingItCreated(ctx context.Context) {
	id := h.envID("dn1")
	h.mustUp(ctx, provider.EnvSpec{
		EnvID:    id,
		Services: []provider.ServiceSpec{h.webService("web", "down")},
	})
	h.waitForReady(ctx, id, "web")

	td, err := h.r.Down(ctx, id)
	if err != nil {
		h.t.Fatalf("Down: %v", err)
	}
	if td.Removed == 0 {
		h.t.Error("Down reported removing nothing from an environment that was running")
	}
	if len(td.Pending) != 0 {
		h.t.Errorf("Down could not remove %v", td.Pending)
	}
	if got := h.resourcesFor(ctx, id); len(got) != 0 {
		h.t.Errorf("Down reported success and left %d resources: %v", len(got), got)
	}
}

func (h *rtHarness) downOfSomethingNeverUpSucceeds(ctx context.Context) {
	// af down runs on a schedule, after a failed af up, and from a pull
	// request that closed before the environment finished starting. Every one
	// of those can reach an environment that does not exist, and an error
	// there turns a cleanup into a red build.
	td, err := h.r.Down(ctx, "afcneverwas"+h.runID)
	if err != nil {
		h.t.Fatalf("Down of an environment that never existed: %v", err)
	}
	if len(td.Pending) != 0 {
		h.t.Errorf("Down of an environment that never existed reported %v pending", td.Pending)
	}
}

func (h *rtHarness) downIsIdempotent(ctx context.Context) {
	id := h.envID("dn2")
	h.mustUp(ctx, provider.EnvSpec{
		EnvID:    id,
		Services: []provider.ServiceSpec{h.worker("w", "sleep 60")},
	})
	if _, err := h.r.Down(ctx, id); err != nil {
		h.t.Fatalf("first Down: %v", err)
	}
	td, err := h.r.Down(ctx, id)
	if err != nil {
		h.t.Fatalf("second Down: %v", err)
	}
	if len(td.Pending) != 0 {
		h.t.Errorf("the second Down reported %v pending; teardown is retried, and a "+
			"retry that reports work outstanding never converges", td.Pending)
	}
}

func (h *rtHarness) downTouchesOnlyItsOwn(ctx context.Context) {
	keep := h.envID("dn3keep")
	remove := h.envID("dn3rm")
	for _, id := range []string{keep, remove} {
		h.mustUp(ctx, provider.EnvSpec{
			EnvID:    id,
			Services: []provider.ServiceSpec{h.worker("w", "sleep 120")},
		})
	}
	if _, err := h.r.Down(ctx, remove); err != nil {
		h.t.Fatalf("Down: %v", err)
	}
	if got := h.resourcesFor(ctx, keep); len(got) == 0 {
		h.t.Error("tearing one environment down removed another's resources; every " +
			"environment on a shared daemon or a shared cluster belongs to somebody, " +
			"and this is the mistake that takes down a colleague's work")
	}
	if got := h.resourcesFor(ctx, remove); len(got) != 0 {
		h.t.Errorf("Down left %d resources of the environment it was asked to remove", len(got))
	}
}

// --- inventory -----------------------------------------------------------

func (h *rtHarness) inventoryListsLiveResources(ctx context.Context) {
	id := h.envID("inv1")
	h.mustUp(ctx, provider.EnvSpec{
		EnvID:    id,
		Services: []provider.ServiceSpec{h.worker("w", "sleep 120")},
	})
	got := h.resourcesFor(ctx, id)
	if len(got) == 0 {
		h.t.Fatal("Inventory reported nothing for a running environment; the leak " +
			"detector compares this against the journal, and a runtime that reports " +
			"nothing is a runtime that appears to have leaked nothing")
	}
	for _, res := range got {
		if res.Kind == "" || res.ID == "" {
			h.t.Errorf("Inventory reported a resource with no kind or no id: %+v", res)
		}
	}
}

func (h *rtHarness) inventoryAttributesResources(ctx context.Context) {
	a := h.envID("inv2a")
	h.mustUp(ctx, provider.EnvSpec{
		EnvID:    a,
		Services: []provider.ServiceSpec{h.worker("w", "sleep 120")},
	})
	all, err := h.r.Inventory(ctx)
	if err != nil {
		h.t.Fatalf("Inventory: %v", err)
	}
	mine := 0
	for _, res := range all {
		if res.EnvID == a {
			mine++
		}
	}
	if mine == 0 {
		h.t.Error("no resource in the inventory names the environment it belongs to; " +
			"the leak detector reports untracked resources by environment, and one " +
			"that cannot say which environment it came from cannot be chased down")
	}
}

// --- egress --------------------------------------------------------------
//
// None of these is skippable and none of them is allowed to be quick. They are
// the half of the product people are actually trusting: an environment that
// can reach the internet can email a customer, charge a card, and write to a
// production analytics stream. A runtime that gets any of them wrong is not
// a runtime with a missing feature, it is a runtime that silently removes the
// guarantee the whole thing is sold on.

func (h *rtHarness) egressNoPolicyMeansNothingGetsOut(ctx context.Context) {
	h.requireInternet(ctx)
	// A manifest with no egress section is valid. It must mean nothing gets
	// out, not that the sidecar failed to parse a policy and let everything
	// through, and not that the environment refused to start.
	code := h.probe(ctx, h.envID("eg1"), nil,
		"wget -T 20 -q -O - http://"+h.opts.AllowedHost+"/")
	if code != refused {
		h.t.Errorf("an environment with no egress policy reached %s (exit %d); "+
			"absent policy has to mean deny, because the alternative is that "+
			"forgetting a section opens the environment", h.opts.AllowedHost, code)
	}
}

func (h *rtHarness) egressAllowedHostIsReached(ctx context.Context) {
	h.requireInternet(ctx)
	code := h.probe(ctx, h.envID("eg2"), h.allowOnly(),
		"wget -T 20 -q -O - http://"+h.opts.AllowedHost+"/")
	if code != reached {
		h.t.Errorf("a host the policy allows was not reachable (exit %d). Every other "+
			"behavior here asserts something was refused, and they all pass on a "+
			"runtime with no network at all; this is the one that proves they mean "+
			"something", code)
	}
}

func (h *rtHarness) egressHostWithNoRuleIsRefused(ctx context.Context) {
	h.requireInternet(ctx)
	code := h.probe(ctx, h.envID("eg3"), h.allowOnly(),
		"wget -T 20 -q -O - http://"+h.opts.RefusedHost+"/")
	if code != refused {
		h.t.Errorf("%s has no rule and was reached anyway (exit %d)", h.opts.RefusedHost, code)
	}
}

func (h *rtHarness) egressAppliesWithoutProxyVariables(ctx context.Context) {
	h.requireInternet(ctx)
	// The property the whole design rests on. Proxy variables are a request,
	// and a great many clients ignore them: Node has no proxy support at all,
	// and plenty of SDKs bundle a client that does the same. An egress control
	// that only works for clients that agreed to it is not a control.
	allowed := h.probe(ctx, h.envID("eg4a"), h.allowOnly(),
		noProxyVars+"wget -T 20 -q -O - http://"+h.opts.AllowedHost+"/")
	if allowed != reached {
		h.t.Errorf("a client with no proxy support could not reach a host the policy "+
			"allows (exit %d)", allowed)
	}
	refusedCode := h.probe(ctx, h.envID("eg4b"), h.allowOnly(),
		noProxyVars+"wget -T 20 -q -O - http://"+h.opts.RefusedHost+"/")
	if refusedCode != refused {
		h.t.Errorf("a client with no proxy support reached a host the policy refuses "+
			"(exit %d)", refusedCode)
	}
}

func (h *rtHarness) egressCannotBeBypassedByAddress(ctx context.Context) {
	h.requireInternet(ctx)
	// Interception is by DNS, so the obvious way around it is to skip DNS.
	// That has to fail for a reason that has nothing to do with DNS: the
	// environment has no route out, so a packet addressed straight at the
	// internet has nowhere to go. The policy here is allow-everything on
	// purpose, so a pass cannot be the policy refusing it.
	code := h.probe(ctx, h.envID("eg5"), &schema.Egress{Default: schema.ModeAllow},
		noProxyVars+"wget -T 15 -q -O - http://1.1.1.1/")
	if code != refused {
		h.t.Errorf("a service reached the internet by address, going around the "+
			"sidecar entirely (exit %d). Everything the policy decides is decided "+
			"at the sidecar, so a route that does not pass through it is not a "+
			"weaker control, it is no control", code)
	}
}

func (h *rtHarness) egressCannotReachMetadata(ctx context.Context) {
	// No internet needed: the metadata address is link-local and answers on
	// the host itself, which is exactly what makes it dangerous. On a cloud
	// runner it hands out credentials for the node's identity.
	code := h.probe(ctx, h.envID("eg6"), &schema.Egress{Default: schema.ModeAllow},
		noProxyVars+"wget -T 5 -q -O - http://169.254.169.254/")
	if code != refused {
		h.t.Errorf("the instance metadata endpoint was reachable from inside the "+
			"environment (exit %d); on a cloud runner that address hands out the "+
			"node's credentials to anything that asks", code)
	}
}

func (h *rtHarness) egressCannotBeBypassedByUDP(ctx context.Context) {
	h.requireInternet(ctx)
	// DNS is the one thing the environment is allowed to speak to the sidecar,
	// so UDP straight past it to somebody else's resolver is the obvious
	// tunnel: a name lookup carries whatever the client puts in it.
	code := h.probe(ctx, h.envID("eg7"), &schema.Egress{Default: schema.ModeAllow},
		"nslookup "+h.opts.AllowedHost+" 1.1.1.1")
	if code != refused {
		h.t.Errorf("a UDP query reached a public resolver directly (exit %d); the "+
			"environment resolves through the sidecar and nowhere else, or a name "+
			"lookup becomes an unlogged channel out", code)
	}
}

func (h *rtHarness) egressNamesDoNotCrossEnvironments(ctx context.Context) {
	other := h.envID("eg8a")
	h.mustUp(ctx, provider.EnvSpec{
		EnvID:    other,
		Services: []provider.ServiceSpec{h.webService("peer", "MARKERAAA")},
	})
	h.waitForReady(ctx, other, "peer")

	mine := h.envID("eg8b")
	prober := h.worker("prober", retrying(
		"wget -T 5 -q -O - http://peer:8080/ | grep -q MARKERBBB"))
	// Declared as depending on the service it fetches, and retried on top of
	// that. The dependency is what a manifest would say; the retry is because
	// a one line busybox server serves one connection at a time, so the
	// runtime's own readiness check and this request can arrive together and
	// one of them is refused. Without both, this behavior fails about half
	// the time for a reason that has nothing to do with what it is asking.
	prober.DependsOn = []string{"peer"}
	_, _ = h.up(ctx, provider.EnvSpec{
		EnvID: mine,
		Services: []provider.ServiceSpec{
			h.webService("peer", "MARKERBBB"),
			prober,
		},
	})
	// Both environments have a service called peer. The name has to resolve to
	// this environment's, because service names come from the manifest and
	// every environment of one repository has the same ones: if they shared a
	// namespace, two previews of the same pull request would answer each
	// other's requests.
	if code := h.waitForExit(ctx, mine, "prober"); code != reached {
		h.t.Errorf("a service name did not resolve to this environment's service "+
			"(exit %d); two environments of one repository declare the same service "+
			"names, so a name that crosses is two previews serving each other", code)
	}
}

// allowOnly permits the allowed host by rule and nothing else.
func (h *rtHarness) allowOnly() *schema.Egress {
	return &schema.Egress{
		Default: schema.ModeBlock,
		Rules:   []schema.EgressRule{{Host: h.opts.AllowedHost, Mode: schema.ModeAllow}},
	}
}

// --- logs ----------------------------------------------------------------

func (h *rtHarness) logsReturnWhatAServiceWrote(ctx context.Context) {
	reader, ok := h.r.(provider.LogReader)
	if !ok {
		h.t.Fatal("the runtime declares Logs but does not implement provider.LogReader")
	}
	id := h.envID("log1")
	const marker = "conformance-log-marker"
	_, _ = h.up(ctx, provider.EnvSpec{
		EnvID:    id,
		Services: []provider.ServiceSpec{h.worker("talker", "echo "+marker+"; exit 0")},
	})
	h.waitForExit(ctx, id, "talker")

	for {
		lines, err := reader.Logs(ctx, id, "", 200)
		if err != nil {
			h.t.Fatalf("Logs: %v", err)
		}
		for _, l := range lines {
			if strings.Contains(l.Text, marker) {
				return
			}
		}
		select {
		case <-ctx.Done():
			h.t.Errorf("Logs never returned the line the service printed; af logs is "+
				"how somebody finds out why a service exited, and %d lines came back "+
				"without it", len(lines))
			return
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// requireInternet skips when this machine cannot reach the host the egress
// behaviors decide about.
//
// A laptop on a plane should report a skip, not a failure that looks exactly
// like a broken egress control. The check is deliberately made from the test
// process rather than from inside an environment: what it is asking is whether
// the machine has internet at all, and asking from inside would be asking the
// question the behavior itself exists to answer.
func (h *rtHarness) requireInternet(ctx context.Context) {
	h.t.Helper()
	c := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+h.opts.AllowedHost+"/", nil)
	if err != nil {
		h.t.Fatalf("building the reachability request: %v", err)
	}
	resp, err := c.Do(req)
	if err != nil {
		h.t.Skipf("skipped: %s is not reachable from this machine: %v", h.opts.AllowedHost, err)
	}
	_ = resp.Body.Close()
}

// httpGet reads a URL and returns its body.
func httpGet(t *testing.T, ctx context.Context, url string) string {
	t.Helper()
	c := &http.Client{Timeout: 15 * time.Second}
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			t.Fatalf("building a request for %s: %v", url, err)
		}
		resp, err := c.Do(req)
		if err == nil {
			body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			_ = resp.Body.Close()
			if readErr == nil {
				return string(body)
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("%s never answered: %v", url, err)
		case <-time.After(500 * time.Millisecond):
		}
	}
}
