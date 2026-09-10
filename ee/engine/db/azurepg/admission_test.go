// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
package azurepg_test

import (
	"context"
	"github.com/antifailure/antifailure/ee/engine/db/azurepg"
	"github.com/stretchr/testify/require"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// Done marks the point the second branch enters its cancellable admission wait.
// Its HTTP transport has its own signal, so bypassing admission is observable
// without relying on a sleep to infer that a request did not happen.
type admissionContext struct {
	context.Context
	once    sync.Once
	waiting chan struct{}
}

func (c *admissionContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func TestAdmissionSerializesIndependentProviderObjects(t *testing.T) {
	server := newFake(t, seedSQL)
	base := newProvider(t, server)
	g, err := base.RefreshGolden(context.Background(), goldenSpec())
	require.NoError(t, err)
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	opts := options(t, server)
	opts.MaxBranches = 1
	opts.HTTPClient = cancellingTransport(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodPut && !strings.Contains(req.URL.Path, "/firewallRules/") {
			close(entered)
			<-release
		}
		return http.DefaultClient.Do(req)
	})
	first, err := azurepg.NewWithFixtureRoles(opts)
	require.NoError(t, err)
	defer func() { _ = first.Close() }()
	firstDone := make(chan error, 1)
	go func() { _, err := first.Branch(context.Background(), g.ID, "first-admission"); firstDone <- err }()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("first restore never started")
	}
	secondHTTP := make(chan struct{})
	var once sync.Once
	opts.HTTPClient = cancellingTransport(func(req *http.Request) (*http.Response, error) {
		once.Do(func() { close(secondHTTP) })
		<-release
		return http.DefaultClient.Do(req)
	})
	second, err := azurepg.NewWithFixtureRoles(opts)
	require.NoError(t, err)
	defer func() { _ = second.Close() }()
	ctx := &admissionContext{Context: context.Background(), waiting: make(chan struct{})}
	secondDone := make(chan error, 1)
	go func() { _, err := second.Branch(ctx, g.ID, "second-admission"); secondDone <- err }()
	select {
	case <-ctx.waiting:
	case <-secondHTTP:
		t.Error("second provider read admission state while the first create was unfinished")
	case <-time.After(10 * time.Second):
		t.Error("second provider never entered admission")
	}
	select {
	case <-secondHTTP:
		t.Error("second provider reached HTTP before admission was released")
	default:
	}
	releaseOnce.Do(func() { close(release) })
	require.NoError(t, <-firstDone)
	require.Error(t, <-secondDone, "two provider objects exceeded their shared branch cap")
	require.Equal(t, 3, server.ResourceCount())
}
