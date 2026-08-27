package conformance

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// fakeRuntime is a runtime that exists only to be broken.
//
// It is not a mock of anything and it is not a second implementation anybody
// should run. Its whole job is to be a correct runtime that one named flaw can
// be introduced into, so that every assertion in the suite can be shown to
// fail when the thing it asserts is not true. Without it the suite is a list
// of behaviors nobody has ever seen go red, which is worth less than it looks:
// the database suite in this same package carries a comment recording that one
// of its checks was silently disabled for weeks, and that a negative control
// is what found it.
//
// It keeps no containers and opens no sockets except the ones a web service
// needs in order to be fetched, so the self test runs offline in under a
// second.
type fakeRuntime struct {
	// flaw is the single named thing wrong with this runtime, or empty.
	flaw string
	// allowedHost is the host the suite's policies name. The fake decides
	// probes by matching it rather than by connecting anywhere.
	allowedHost string
	// state is shared by every fakeRuntime in one run.
	//
	// The suite builds a new runtime for each behavior, exactly as a real one
	// would open a new connection to the same daemon, and its own inventory
	// snapshots build another. A fake that kept its environments in itself
	// would show each of those an empty world, and the leak check at the end
	// of the run would be looking at a runtime that had never been used.
	state *fakeState
}

type fakeState struct {
	mu      sync.Mutex
	envs    map[string]*fakeEnv
	leaked  map[string]bool
	servers []*httptest.Server
	closed  bool
}

func newFakeState() *fakeState {
	return &fakeState{envs: map[string]*fakeEnv{}, leaked: map[string]bool{}}
}

// Shutdown closes the web servers the fake started. It is separate from Close
// because the suite closes a runtime after every behavior, and closing the
// shared state there would take down environments the next behavior still
// expects to find.
func (s *fakeState) Shutdown() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	for _, srv := range s.servers {
		srv.Close()
	}
	s.servers = nil
}

type fakeEnv struct {
	id       string
	proxy    bool
	services []*fakeService
	egress   *schema.Egress
}

type fakeService struct {
	name  string
	kind  string
	ready bool
	state string
	exit  *int
	url   string
	logs  []string
}

func newFakeRuntime(state *fakeState, flaw, allowedHost string) *fakeRuntime {
	return &fakeRuntime{state: state, flaw: flaw, allowedHost: allowedHost}
}

func (f *fakeRuntime) is(flaw string) bool { return f.flaw == flaw }

func (f *fakeRuntime) Name() string {
	if f.is(flawNoName) {
		return ""
	}
	return "fake"
}

func (f *fakeRuntime) Capabilities() provider.RuntimeCaps {
	caps := provider.RuntimeCaps{Ingress: true, Logs: true}
	if f.is(flawLiesAboutLogs) {
		// Declares the capability while the LogReader assertion below stops
		// the type assertion from succeeding.
		caps.Logs = true
	}
	return caps
}

// Close releases this handle. The environments outlive it, because they live
// in the daemon this handle was talking to.
func (f *fakeRuntime) Close() error { return nil }

func (f *fakeRuntime) Up(ctx context.Context, spec provider.EnvSpec) (provider.Env, error) {
	f.state.mu.Lock()
	defer f.state.mu.Unlock()

	if spec.EnvID == "" && !f.is(flawAcceptsEmptyEnvID) {
		return provider.Env{}, aferrors.Coded(aferrors.AFRUN040,
			"detail", "the environment has no id")
	}
	journal := spec.Journal
	if journal == nil {
		journal = func(string, string) error { return nil }
	}

	order, err := fakeStartOrder(spec.Services, f.flaw)
	if err != nil {
		return provider.Env{}, err
	}

	// Journalled before anything is created, and a refusal stops the create.
	// A resource that exists before it was recorded is a resource teardown
	// cannot find, which is why the order is this way round and not the other.
	netName := spec.EnvID + "-net"
	if f.is(flawJournalsUnfindableNames) {
		// Records something no inventory will ever report, which is a record
		// teardown can do nothing with.
		netName = "some-other-thing"
	}
	if err := journal("network", netName); err != nil {
		if !f.is(flawIgnoresJournalRefusal) {
			return provider.Env{EnvID: spec.EnvID}, err
		}
	}
	if err := journal("container", spec.EnvID+"-proxy"); err != nil {
		if !f.is(flawIgnoresJournalRefusal) {
			return provider.Env{EnvID: spec.EnvID}, err
		}
	}

	env, existing := f.state.envs[spec.EnvID]
	if !existing {
		env = &fakeEnv{id: spec.EnvID}
		f.state.envs[spec.EnvID] = env
	}
	env.egress = spec.Egress
	env.proxy = !f.is(flawNoProxy)

	out := provider.Env{
		EnvID: spec.EnvID, NetworkID: spec.EnvID + "-net",
		CreatedAt: time.Now().UTC(), ProxyReady: env.proxy,
	}
	for _, s := range order {
		if err := journal("container", spec.EnvID+"-"+s.Name); err != nil {
			if !f.is(flawIgnoresJournalRefusal) {
				return out, err
			}
		}
		if s.Migrate != "" {
			if code := f.runCommand(env, s.Migrate); code != 0 {
				if !f.is(flawStartsAfterFailedMigration) {
					return out, aferrors.Coded(aferrors.AFRUN005,
						"service", s.Name, "code", strconv.Itoa(code))
				}
			}
		}
		svc := f.place(env, s)
		if svc.exit != nil && *svc.exit != 0 {
			if f.is(flawRollsBackFailedService) {
				// Tidies away the container that holds the only explanation
				// of why the environment did not come up.
				env.services = env.services[:len(env.services)-1]
			}
			out.Services = append(out.Services, f.report(svc))
			return out, aferrors.Coded(aferrors.AFRUN005,
				"service", s.Name, "code", strconv.Itoa(*svc.exit))
		}
		out.Services = append(out.Services, f.report(svc))
	}
	return out, nil
}

// place runs one service and records what it did.
func (f *fakeRuntime) place(env *fakeEnv, s provider.ServiceSpec) *fakeService {
	// Bringing one environment up twice has to leave one of each service. af
	// up is run again after every failure, and a runtime that does not look
	// for what it already made leaves the first copy of everything running
	// and unreferenced.
	if !f.is(flawDuplicatesOnSecondUp) {
		for _, existing := range env.services {
			if existing.name == s.Name {
				return existing
			}
		}
	}
	svc := &fakeService{name: s.Name, kind: s.Kind, state: "running"}
	env.services = append(env.services, svc)

	switch {
	case isServeCommand(s.Command):
		body := serveBody(s.Command)
		svc.ready = true
		if !f.is(flawNoURL) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			f.state.servers = append(f.state.servers, srv)
			svc.url = srv.URL
		}
	case strings.HasPrefix(strings.TrimSpace(s.Command), "sleep"):
		svc.ready = true
	default:
		code := f.runCommand(env, s.Command)
		svc.exit = &code
		svc.ready = false
		svc.state = "exited"
		if out := echoed(s.Command); out != "" && !f.is(flawNoLogs) {
			svc.logs = append(svc.logs, out)
		}
	}
	return svc
}

func (f *fakeRuntime) report(s *fakeService) provider.RunningService {
	out := provider.RunningService{
		Name: s.name, Kind: s.kind, ContainerID: s.name + "-id",
		URL: s.url, Ready: s.ready, State: s.state, ExitCode: s.exit,
	}
	if f.is(flawLosesServiceKind) {
		out.Kind = ""
	}
	if f.is(flawNoExitCode) {
		out.ExitCode = nil
	}
	if f.is(flawExitCodeWhileRunning) && out.ExitCode == nil {
		zero := 0
		out.ExitCode = &zero
	}
	return out
}

func (f *fakeRuntime) Status(ctx context.Context, envID string) (provider.Env, error) {
	f.state.mu.Lock()
	defer f.state.mu.Unlock()
	env, ok := f.state.envs[envID]
	if !ok {
		if f.is(flawErrorsOnUnknownEnv) {
			return provider.Env{}, fmt.Errorf("no such environment %q", envID)
		}
		return provider.Env{EnvID: envID}, nil
	}
	out := provider.Env{EnvID: envID, NetworkID: envID + "-net", ProxyReady: env.proxy}
	for _, s := range env.services {
		out.Services = append(out.Services, f.report(s))
	}
	sort.Slice(out.Services, func(i, j int) bool { return out.Services[i].Name < out.Services[j].Name })
	return out, nil
}

func (f *fakeRuntime) Down(ctx context.Context, envID string) (provider.Teardown, error) {
	f.state.mu.Lock()
	defer f.state.mu.Unlock()

	env, ok := f.state.envs[envID]
	if !ok {
		if f.is(flawDownErrorsWhenAbsent) {
			return provider.Teardown{}, fmt.Errorf("no such environment %q", envID)
		}
		return provider.Teardown{}, nil
	}
	if f.is(flawDownNotIdempotent) {
		// Reports work outstanding forever, so a retried teardown never
		// converges and af down never exits zero.
		return provider.Teardown{Pending: []provider.PendingResource{
			{Kind: "container", ID: envID + "-proxy", Reason: "still going"},
		}}, nil
	}

	removed := len(env.services) + 2 // the services, the network, the sidecar
	if f.is(flawLeaksOnTeardown) && !strings.HasPrefix(envID, "afcdn") {
		// Leaks the network of every environment except the ones the teardown
		// behaviors inspect afterwards. That is deliberate, and it is the
		// whole argument for the run-wide leak check: a per-behavior
		// assertion only sees the environment it just tore down, so a runtime
		// that cleans up correctly exactly where it is watched passes all
		// twenty nine behaviors and still leaves a network per environment
		// behind. Nothing but the check at the end of the run can see that.
		f.state.leaked[envID] = true
	}
	switch {
	case f.is(flawDownLeavesResources):
		// Says it removed everything and keeps the services.
		env.services = env.services[:0]
		f.state.envs[envID] = &fakeEnv{id: envID, services: []*fakeService{{name: "orphan", state: "running"}}}
	case f.is(flawDownRemovesEverything):
		for id := range f.state.envs {
			delete(f.state.envs, id)
		}
	default:
		delete(f.state.envs, envID)
	}
	return provider.Teardown{Removed: removed}, nil
}

func (f *fakeRuntime) Inventory(ctx context.Context) ([]provider.Resource, error) {
	f.state.mu.Lock()
	defer f.state.mu.Unlock()
	if f.is(flawEmptyInventory) {
		return nil, nil
	}
	var out []provider.Resource
	for id := range f.state.leaked {
		out = append(out, provider.Resource{
			Kind: "network", ID: id + "-net", EnvID: id, CreatedAt: time.Now().UTC(),
		})
	}
	for id, env := range f.state.envs {
		owner := id
		if f.is(flawInventoryWithoutEnvID) {
			owner = ""
		}
		out = append(out, provider.Resource{
			Kind: "network", ID: id + "-net", EnvID: owner, CreatedAt: time.Now().UTC(),
		})
		out = append(out, provider.Resource{
			Kind: "container/sidecar", ID: id + "-proxy", EnvID: owner, CreatedAt: time.Now().UTC(),
		})
		for _, s := range env.services {
			out = append(out, provider.Resource{
				Kind: "container/service", ID: id + "-" + s.name, EnvID: owner,
				CreatedAt: time.Now().UTC(), Labels: map[string]string{"service": s.name},
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Logs is on fakeRuntime rather than on a wrapper so the LogReader assertion
// in Capabilities_MatchWhatIsImplemented succeeds, except where the flaw is
// precisely that it should not.
func (f *fakeRuntime) Logs(ctx context.Context, envID, service string, tail int) ([]provider.LogLine, error) {
	f.state.mu.Lock()
	defer f.state.mu.Unlock()
	env, ok := f.state.envs[envID]
	if !ok {
		return nil, nil
	}
	var out []provider.LogLine
	for _, s := range env.services {
		if service != "" && s.name != service {
			continue
		}
		for _, line := range s.logs {
			out = append(out, provider.LogLine{Service: s.name, Stream: "stdout", Text: line})
		}
	}
	return out, nil
}

// noLogsFake is the fake with log reading taken away, which is how the suite
// gets to see a runtime that declares a capability it does not implement. It
// delegates every method by hand rather than embedding, because embedding
// would promote Logs and there would be nothing to catch.
type noLogsFake struct{ inner *fakeRuntime }

func (n noLogsFake) Name() string                       { return n.inner.Name() }
func (n noLogsFake) Capabilities() provider.RuntimeCaps { return n.inner.Capabilities() }
func (n noLogsFake) Close() error                       { return n.inner.Close() }
func (n noLogsFake) Up(ctx context.Context, spec provider.EnvSpec) (provider.Env, error) {
	return n.inner.Up(ctx, spec)
}
func (n noLogsFake) Down(ctx context.Context, envID string) (provider.Teardown, error) {
	return n.inner.Down(ctx, envID)
}
func (n noLogsFake) Status(ctx context.Context, envID string) (provider.Env, error) {
	return n.inner.Status(ctx, envID)
}
func (n noLogsFake) Inventory(ctx context.Context) ([]provider.Resource, error) {
	return n.inner.Inventory(ctx)
}

// fakeStartOrder is the same topological sort every runtime has to do, so that
// the ordering behaviors have something real to check.
func fakeStartOrder(services []provider.ServiceSpec, flaw string) ([]provider.ServiceSpec, error) {
	if flaw == flawIgnoresDependencies {
		return services, nil
	}
	byName := make(map[string]provider.ServiceSpec, len(services))
	for _, s := range services {
		byName[s.Name] = s
	}
	var out []provider.ServiceSpec
	state := map[string]int{}

	var visit func(name string, path []string) error
	visit = func(name string, path []string) error {
		switch state[name] {
		case 2:
			return nil
		case 1:
			if flaw == flawHangsOnCycle {
				return nil
			}
			return aferrors.Coded(aferrors.AFRUN041,
				"cycle", strings.Join(append(path, name), " -> "))
		}
		s, ok := byName[name]
		if !ok {
			if flaw == flawIgnoresMissingDependency {
				return nil
			}
			return aferrors.Coded(aferrors.AFRUN042,
				"service", strings.Join(path, " -> "), "missing", name)
		}
		state[name] = 1
		deps := append([]string(nil), s.DependsOn...)
		sort.Strings(deps)
		for _, d := range deps {
			if err := visit(d, append(path, name)); err != nil {
				return err
			}
		}
		state[name] = 2
		out = append(out, s)
		return nil
	}
	for _, s := range services {
		if err := visit(s.Name, nil); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// runCommand decides what a command the suite ran would have done.
//
// It recognises exactly the shapes the suite generates and nothing else, which
// is the honest scope of a fake: it is not a shell, it is a table of the
// questions this suite asks and the answers a correct runtime would give.
func (f *fakeRuntime) runCommand(env *fakeEnv, command string) int {
	cmd := strings.TrimSpace(command)

	// A retrying probe: everything between the loop header and the redirect
	// is the command it is retrying, and the answer does not change between
	// attempts, so one evaluation is the whole loop.
	if rest, ok := strings.CutPrefix(cmd, "i=0; while [ $i -lt 30 ]; do "); ok {
		inner, _, _ := strings.Cut(rest, " >/dev/null 2>&1 && exit 0;")
		if f.probeReaches(env, inner) {
			return reached
		}
		return refused
	}
	if inner, ok := strings.CutSuffix(cmd, " >/dev/null 2>&1 && exit 0 || exit 9"); ok {
		if f.probeReaches(env, inner) {
			return reached
		}
		return refused
	}
	if code, ok := literalExit(cmd); ok {
		return code
	}
	if strings.HasPrefix(cmd, "echo ") {
		if _, after, found := strings.Cut(cmd, ";"); found {
			if code, ok := literalExit(strings.TrimSpace(after)); ok {
				return code
			}
		}
		return 0
	}
	return 0
}

// probeReaches is the whole containment model of a correct runtime, in the
// order the real one decides it.
func (f *fakeRuntime) probeReaches(env *fakeEnv, inner string) bool {
	ignoresProxyVars := strings.HasPrefix(inner, noProxyVars)
	inner = strings.TrimPrefix(inner, noProxyVars)

	switch {
	case strings.Contains(inner, "169.254.169.254"):
		// Link local, so no policy is consulted and no rule can permit it.
		return f.is(flawMetadataReachable)
	case strings.HasPrefix(inner, "nslookup"):
		return f.is(flawUDPEscapes)
	case strings.Contains(inner, "http://1.1.1.1/"):
		return f.is(flawRawAddressEscapes)
	case strings.Contains(inner, "MARKERBBB"):
		// The service name resolved to this environment's service, unless the
		// flaw is that names are shared.
		return !f.is(flawNamesCrossEnvironments)
	}
	if f.is(flawHonoursProxyVarsOnly) && ignoresProxyVars {
		return true
	}
	host := hostIn(inner)
	return f.policyAllows(env.egress, host)
}

func (f *fakeRuntime) policyAllows(egress *schema.Egress, host string) bool {
	if egress == nil {
		return f.is(flawNoPolicyAllowsEverything)
	}
	for _, rule := range egress.Rules {
		if rule.Host != host {
			continue
		}
		if f.is(flawBlocksAllowedHost) {
			return false
		}
		return rule.Mode == schema.ModeAllow
	}
	if f.is(flawAllowsUnnamedHost) {
		return true
	}
	return egress.Default == schema.ModeAllow
}

// hostIn pulls the host out of the one URL shape the suite's probes use.
func hostIn(command string) string {
	_, after, ok := strings.Cut(command, "http://")
	if !ok {
		return ""
	}
	host, _, _ := strings.Cut(after, "/")
	return host
}

func literalExit(cmd string) (int, bool) {
	rest, ok := strings.CutPrefix(cmd, "exit ")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(rest))
	if err != nil {
		return 0, false
	}
	return n, true
}

// isServeCommand recognises the busybox web server the suite starts.
func isServeCommand(command string) bool {
	return strings.Contains(command, "httpd -f -p 8080")
}

// serveBody recovers the body that server was told to answer with.
func serveBody(command string) string {
	_, after, ok := strings.Cut(command, "printf '%s' '")
	if !ok {
		return ""
	}
	body, _, _ := strings.Cut(after, "'")
	return body
}

// echoed recovers what an echo command printed.
func echoed(command string) string {
	rest, ok := strings.CutPrefix(strings.TrimSpace(command), "echo ")
	if !ok {
		return ""
	}
	out, _, _ := strings.Cut(rest, ";")
	return strings.TrimSpace(out)
}
