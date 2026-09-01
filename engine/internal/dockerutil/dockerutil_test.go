package dockerutil_test

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
)

var epoch = time.Date(2026, 3, 4, 5, 6, 7, 0, time.FixedZone("plus2", 2*60*60))

func TestManaged_StampsEnoughToFindAndAgeAResource(t *testing.T) {
	t.Parallel()
	l := dockerutil.Managed(dockerutil.KindService, "env-abc", epoch)
	require.Equal(t, dockerutil.ManagedValue, l[dockerutil.LabelManaged])
	require.Equal(t, "service", l[dockerutil.LabelKind])
	require.Equal(t, "env-abc", l[dockerutil.LabelEnv])
	// UTC, so that two machines in different zones sort their orphans the same
	// way and an age comparison is not off by the offset.
	require.Equal(t, "2026-03-04T03:06:07Z", l[dockerutil.LabelCreated])
	_, err := time.Parse(time.RFC3339, l[dockerutil.LabelCreated])
	require.NoError(t, err)
}

func TestManaged_OmitsTheEnvironmentWhenThereIsNone(t *testing.T) {
	t.Parallel()
	// A golden belongs to the machine, not to an environment. An empty env
	// label would match an env filter for the empty string, which is how a
	// teardown ends up removing a golden every environment shares.
	l := dockerutil.Managed(dockerutil.KindGolden, "", epoch)
	_, present := l[dockerutil.LabelEnv]
	require.False(t, present)
}

func TestIsOurs_OnlyClaimsWhatCarriesTheLabel(t *testing.T) {
	t.Parallel()
	require.True(t, dockerutil.IsOurs(dockerutil.Managed(dockerutil.KindBranch, "e", epoch)))
	require.False(t, dockerutil.IsOurs(nil))
	require.False(t, dockerutil.IsOurs(map[string]string{}))
	// The shape of somebody's own container. Touching this is the failure the
	// label exists to prevent.
	require.False(t, dockerutil.IsOurs(map[string]string{
		"com.docker.compose.project": "their-app",
		"dev.antifailure.kind":       "branch",
	}), "the kind label alone is not a claim of ownership")
}

func TestFilter_AlwaysRequiresTheManagedLabel(t *testing.T) {
	t.Parallel()
	f := dockerutil.Filter(dockerutil.LabelKind, dockerutil.KindService)
	got := f.Get("label")
	// The bare key is an existence test, so a resource an older release
	// stamped with a different value is still found.
	require.Contains(t, got, "dev.antifailure.managed")
	require.NotContains(t, got, "dev.antifailure.managed=true")
	require.Contains(t, got, "dev.antifailure.kind=service")

	env := dockerutil.EnvFilter("env-1")
	require.Contains(t, env.Get("label"), "dev.antifailure.managed")
	require.Contains(t, env.Get("label"), "dev.antifailure.env=env-1")
}

func TestFilter_IgnoresATrailingKeyWithNoValue(t *testing.T) {
	t.Parallel()
	// A caller that passes an odd number of arguments has a bug, but the
	// filter it gets must still be a narrowing one. Dropping the managed
	// label instead would widen a delete to every container on the machine.
	f := dockerutil.Filter(dockerutil.LabelKind)
	require.Equal(t, []string{"dev.antifailure.managed"}, f.Get("label"))
}

func TestPortAllocator_NeverHandsOutTheSamePortTwice(t *testing.T) {
	t.Parallel()
	a := dockerutil.NewPortAllocator(0)

	const n = 24
	var mu sync.Mutex
	seen := map[int]bool{}
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := a.Free()
			require.NoError(t, err)
			mu.Lock()
			defer mu.Unlock()
			require.False(t, seen[p], "port %d was handed out twice", p)
			seen[p] = true
		}()
	}
	wg.Wait()
	require.Len(t, seen, n)
	for p := range seen {
		require.GreaterOrEqual(t, p, dockerutil.DefaultPortFrom)
	}
}

func TestPortAllocator_ReleaseReturnsAPortToThePool(t *testing.T) {
	t.Parallel()
	a := dockerutil.NewPortAllocator(45000)
	first, err := a.Free()
	require.NoError(t, err)
	second, err := a.Free()
	require.NoError(t, err)
	require.NotEqual(t, first, second)

	a.Release(first)
	again, err := a.Free()
	require.NoError(t, err)
	require.Equal(t, first, again, "a released port is the next one offered")
}

func TestPortAllocator_RefusesARangeAboveThePortSpace(t *testing.T) {
	t.Parallel()
	// The message has to name the range, because the only useful next step is
	// to move it.
	a := dockerutil.NewPortAllocator(70000)
	_, err := a.Free()
	require.Error(t, err)
	var coded *aferrors.Error
	require.True(t, aferrors.As(err, &coded))
	require.Contains(t, coded.Message(), "70000")
}

func TestPortAllocator_SearchesOnlyRealPortsNearTheTop(t *testing.T) {
	t.Parallel()
	// Started near the top, the search must stop at 65535 rather than spend
	// itself on numbers that cannot be a port and then report exhaustion of a
	// range that was never searchable.
	a := dockerutil.NewPortAllocator(65530)
	for i := 0; i < 6; i++ {
		p, err := a.Free()
		if err != nil {
			// Something else on the machine holds one of the six. That is
			// allowed; what is not allowed is a port outside the space.
			break
		}
		require.LessOrEqual(t, p, 65535)
		require.GreaterOrEqual(t, p, 65530)
	}
	_, err := a.Free()
	require.Error(t, err, "there are only six ports left above 65529")
	var coded *aferrors.Error
	require.True(t, aferrors.As(err, &coded))
	require.Contains(t, coded.Message(), "65530-65535")
}

func TestShortID_MatchesWhatDockerPsShows(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("a", 64)
	require.Equal(t, strings.Repeat("a", 12), dockerutil.ShortID(long))
	require.Equal(t, "abc", dockerutil.ShortID("abc"), "a short id is left alone")
	require.Equal(t, "", dockerutil.ShortID(""))
}

func TestFirstName_StripsDockersSlash(t *testing.T) {
	t.Parallel()
	require.Equal(t, "af-db-env1", dockerutil.FirstName([]string{"/af-db-env1", "/other"}))
	require.Equal(t, "", dockerutil.FirstName(nil))
}

func TestHost_FallsBackToTheUnixSocket(t *testing.T) {
	t.Parallel()
	// Not parallel-safe to set the variable, so only the default is asserted
	// here; the override is a one line read of the environment.
	h := dockerutil.Host()
	require.NotEmpty(t, h)
	require.True(t, strings.Contains(h, "://"), "an endpoint is a URL, and errors print it")
}

func TestPortRangeFrom_DefaultsWhenTheVariableIsUnset(t *testing.T) {
	t.Parallel()
	from, err := dockerutil.PortRangeFrom(func(string) string { return "" })
	require.NoError(t, err)
	require.Equal(t, dockerutil.DefaultPortFrom, from)
}

func TestPortRangeFrom_MovesTheRangeTheVariableNames(t *testing.T) {
	t.Parallel()
	from, err := dockerutil.PortRangeFrom(func(k string) string {
		require.Equal(t, "AF_PORT_RANGE_START", k)
		return " 51000 "
	})
	require.NoError(t, err)
	require.Equal(t, 51000, from)
}

func TestPortRangeFrom_RefusesAValueThatCannotBeAPort(t *testing.T) {
	t.Parallel()
	// Silently ignoring a value the user set would leave them looking at the
	// range they were trying to move away from with nothing to explain it, so
	// every one of these is an error that names the variable.
	for _, raw := range []string{"no", "80", "70000", "-1", "43000ports", "65535"} {
		_, err := dockerutil.PortRangeFrom(func(string) string { return raw })
		require.Error(t, err, "%q is not a usable range start", raw)
		var coded *aferrors.Error
		require.True(t, aferrors.As(err, &coded))
		require.Contains(t, coded.Message(), "AF_PORT_RANGE_START")
		require.Contains(t, coded.Message(), raw)
	}
}

func TestIsPortTaken_MatchesBothWaysTheDaemonSaysIt(t *testing.T) {
	t.Parallel()
	// Both messages come from the daemon: the first when another container
	// holds the port, the second when a process on the host does. Both were
	// observed against a real daemon, and both mean the same retry.
	require.True(t, dockerutil.IsPortTaken(errors.New(
		"Error response from daemon: driver failed programming external "+
			"connectivity on endpoint x: Bind for 127.0.0.1:46000 failed: "+
			"port is already allocated")))
	require.True(t, dockerutil.IsPortTaken(errors.New(
		"Error response from daemon: ports are not available: exposing port "+
			"TCP 127.0.0.1:50133 -> 127.0.0.1:0: listen tcp4 127.0.0.1:50133: "+
			"bind: address already in use")))

	// Narrow on purpose. A retry of any of these would spend four attempts
	// arriving at the same answer more slowly.
	require.False(t, dockerutil.IsPortTaken(nil))
	require.False(t, dockerutil.IsPortTaken(errors.New(
		"Error response from daemon: permission denied while trying to "+
			"connect to the Docker daemon socket")))
	require.False(t, dockerutil.IsPortTaken(errors.New(
		"Cannot connect to the Docker daemon at unix:///var/run/docker.sock")))
	require.False(t, dockerutil.IsPortTaken(errors.New(
		"Error response from daemon: no such image")))
}
