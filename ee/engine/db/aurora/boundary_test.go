// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
package aurora_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/antifailure/antifailure/ee/engine/db/aurora"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/secret"
	"github.com/stretchr/testify/require"
)

func TestRestorePreservesNetworkingAndDisablesIAM(t *testing.T) {
	s := newFake(t, seedSQL, "")
	p := newProvider(t, s)
	g, _ := spec("network")
	v, err := p.RefreshGolden(context.Background(), g)
	require.NoError(t, err)
	_, err = p.Branch(context.Background(), v.ID, "network-env")
	require.NoError(t, err)
	for _, action := range []string{"RestoreDBClusterToPointInTime", "ModifyDBCluster"} {
		require.Equal(t, "false", s.LastRequest(action).Get("EnableIAMDatabaseAuthentication"))
	}
	restore := s.LastRequest("RestoreDBClusterToPointInTime")
	require.Equal(t, "fixture-subnet", restore.Get("DBSubnetGroupName"))
	require.Equal(t, "sg-fixture", restore.Get("VpcSecurityGroupIds.VpcSecurityGroupId.1"))
	require.Equal(t, "false", s.LastRequest("CreateDBInstance").Get("PubliclyAccessible"))
}

func TestConcurrentProvidersReserveBranchAdmission(t *testing.T) {
	s := newFake(t, seedSQL, "")
	opts := options(t, s)
	opts.MaxBranches = 1
	p, err := scopedNew(context.Background(), opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	q, err := scopedNew(context.Background(), opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = q.Close() })
	g, _ := spec("admission")
	v, err := p.RefreshGolden(context.Background(), g)
	require.NoError(t, err)
	entered, release := make(chan struct{}), make(chan struct{})
	aurora.SetHTTPForTest(p, transportFunc(func(r *http.Request) (*http.Response, error) {
		if action(r) == "RestoreDBClusterToPointInTime" {
			close(entered)
			<-release
		}
		return http.DefaultTransport.RoundTrip(r)
	}))
	done := make(chan error, 1)
	go func() { _, err := p.Branch(context.Background(), v.ID, "first"); done <- err }()
	<-entered
	var requests atomic.Int64
	aurora.SetHTTPForTest(q, transportFunc(func(r *http.Request) (*http.Response, error) {
		requests.Add(1)
		return http.DefaultTransport.RoundTrip(r)
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	_, secondErr := q.Branch(ctx, v.ID, "second")
	cancel()
	observed := requests.Load()
	close(release)
	require.NoError(t, <-done)
	require.ErrorIs(t, secondErr, context.DeadlineExceeded)
	require.Zero(t, observed, "second provider reached AWS while first held admission")
	_, err = q.Branch(context.Background(), v.ID, "second")
	require.Error(t, err)
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func action(r *http.Request) string {
	b, _ := r.GetBody()
	defer func() { _ = b.Close() }()
	data, _ := io.ReadAll(b)
	form, _ := url.ParseQuery(string(data))
	return form.Get("Action")
}

func TestSourceScopeProtectsInventoryRetriesAndDestruction(t *testing.T) {
	s := newFake(t, seedSQL, "")
	p := newProvider(t, s)
	ctx := context.Background()
	g, _ := spec("scope")
	v, err := p.RefreshGolden(ctx, g)
	require.NoError(t, err)
	b, err := p.Branch(ctx, v.ID, "same-env")
	require.NoError(t, err)
	require.NoError(t, s.SeedSource("another-production", seedSQL))
	opts := options(t, s)
	opts.SourceCluster = "another-production"
	q, err := scopedNew(ctx, opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = q.Close() })
	items, err := q.Inventory(ctx)
	require.NoError(t, err)
	require.Empty(t, items)
	versions, err := q.ListGoldens(ctx)
	require.NoError(t, err)
	require.Empty(t, versions)
	require.ErrorIs(t, q.Destroy(ctx, b), aurora.ErrNotOurs)
	_, err = q.ConnString(ctx, b, provider.ConnDirect)
	require.Error(t, err)
	require.NoError(t, q.DestroyGolden(ctx, v.ID))
	_, exists := s.DatabaseOf(v.ProviderRef)
	require.True(t, exists)
	w, err := q.RefreshGolden(ctx, g)
	require.NoError(t, err)
	c, err := q.Branch(ctx, w.ID, "same-env")
	require.NoError(t, err)
	require.NotEqual(t, b.ProviderRef, c.ProviderRef)
}

func TestRetryRequiresExactVersionEnvironmentAndPreparation(t *testing.T) {
	s := newFake(t, seedSQL, "")
	p := newProvider(t, s)
	ctx := context.Background()
	g, _ := spec("retry")
	v, err := p.RefreshGolden(ctx, g)
	require.NoError(t, err)
	b, err := p.Branch(ctx, v.ID, "retry-env")
	require.NoError(t, err)
	u, err := p.ConnString(ctx, provider.Branch{EnvID: b.EnvID}, provider.ConnDirect)
	require.NoError(t, err)
	require.NoError(t, dial(u))
	_, err = p.Branch(ctx, "different-version", b.EnvID)
	require.Error(t, err)
	for _, key := range []string{"antifailure:env", "antifailure:prepared", "antifailure:source"} {
		t.Run(key, func(t *testing.T) {
			other, err := p.Branch(ctx, v.ID, "retry-"+key)
			require.NoError(t, err)
			s.SetTags(other.ProviderRef, map[string]string{key: "incorrect"})
			_, err = p.Branch(ctx, v.ID, other.EnvID)
			require.Error(t, err)
			_, err = p.ConnString(ctx, other, provider.ConnDirect)
			require.Error(t, err)
		})
	}
	changed := options(t, s)
	changed.BranchKey = secret.New("another-key")
	q, err := scopedNew(ctx, changed)
	require.NoError(t, err)
	t.Cleanup(func() { _ = q.Close() })
	_, err = q.Branch(ctx, v.ID, b.EnvID)
	require.Error(t, err)
}

func TestReceiptBindsMetadataEvenWhenCallerMatchesForgedTags(t *testing.T) {
	s := newFake(t, seedSQL, "")
	p := newProvider(t, s)
	ctx := context.Background()
	g, _ := spec("receipt")
	v, err := p.RefreshGolden(ctx, g)
	require.NoError(t, err)
	b, err := p.Branch(ctx, v.ID, "receipt-env")
	require.NoError(t, err)
	s.SetTags(b.ProviderRef, map[string]string{"antifailure:version": "forged-version"})
	_, err = p.Branch(ctx, "forged-version", b.EnvID)
	require.Error(t, err)
	s.SetTags(b.ProviderRef, map[string]string{"antifailure:version": v.ID, "antifailure:env": "forged-env"})
	b.EnvID = "forged-env"
	_, err = p.ConnString(ctx, b, provider.ConnDirect)
	require.Error(t, err)
}

func TestMissingVerificationCannotPublish(t *testing.T) {
	for _, kind := range []string{"mask", "verify", "attestation"} {
		t.Run(kind, func(t *testing.T) {
			s := newFake(t, seedSQL, "")
			p := newProvider(t, s)
			g, _ := spec("missing")
			switch kind {
			case "mask":
				g.Mask = nil
			case "verify":
				g.Verify = nil
			case "attestation":
				g.Verify = func(context.Context, secret.Value) (string, error) { return "", nil }
			}
			_, err := p.RefreshGolden(context.Background(), g)
			require.Error(t, err)
			versions, err := p.ListGoldens(context.Background())
			require.NoError(t, err)
			require.Empty(t, versions)
		})
	}
}

func TestInterruptedBranchReturnsHandleAndCannotBeAdopted(t *testing.T) {
	s := newFake(t, seedSQL, "")
	p := newProvider(t, s)
	ctx := context.Background()
	g, _ := spec("interrupted")
	v, err := p.RefreshGolden(ctx, g)
	require.NoError(t, err)
	aurora.SetHTTPForTest(p, transportFunc(func(r *http.Request) (*http.Response, error) {
		if action(r) == "CreateDBInstance" {
			return nil, fmt.Errorf("interrupted writer creation")
		}
		return http.DefaultTransport.RoundTrip(r)
	}))
	b, err := p.Branch(ctx, v.ID, "interrupted-env")
	require.Error(t, err)
	require.NotEmpty(t, b.ProviderRef)
	aurora.SetHTTPForTest(p, http.DefaultTransport)
	_, err = p.Branch(ctx, v.ID, b.EnvID)
	require.Error(t, err)
	_, err = p.ConnString(ctx, b, provider.ConnDirect)
	require.Error(t, err)
	require.NoError(t, p.Destroy(ctx, b))
}

type brokenBody struct{}

func (brokenBody) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (brokenBody) Close() error             { return nil }

func TestAcceptedRestoreUnreadableResponseIsCleaned(t *testing.T) {
	for _, kind := range []string{"read", "xml", "transport"} {
		t.Run(kind, func(t *testing.T) {
			s := newFake(t, seedSQL, "")
			p := newProvider(t, s)
			aurora.SetHTTPForTest(p, transportFunc(func(r *http.Request) (*http.Response, error) {
				resp, err := http.DefaultTransport.RoundTrip(r)
				if err != nil || action(r) != "RestoreDBClusterToPointInTime" {
					return resp, err
				}
				_ = resp.Body.Close()
				switch kind {
				case "read":
					resp.Body = brokenBody{}
				case "xml":
					resp.Body = io.NopCloser(strings.NewReader("<broken"))
				case "transport":
					return nil, io.ErrUnexpectedEOF
				}
				return resp, nil
			}))
			g, _ := spec("lost")
			_, err := p.RefreshGolden(context.Background(), g)
			require.Error(t, err)
			items, err := p.Inventory(context.Background())
			require.NoError(t, err)
			require.Empty(t, items)
		})
	}
}

func TestLostBranchResponseCarriesAttemptBoundCleanupHandle(t *testing.T) {
	for _, replaceAttempt := range []bool{false, true} {
		t.Run(fmt.Sprint(replaceAttempt), func(t *testing.T) {
			s := newFake(t, seedSQL, "")
			p := newProvider(t, s)
			ctx := context.Background()
			g, _ := spec("branch-response")
			v, err := p.RefreshGolden(ctx, g)
			require.NoError(t, err)
			aurora.SetHTTPForTest(p, transportFunc(func(r *http.Request) (*http.Response, error) {
				resp, err := http.DefaultTransport.RoundTrip(r)
				if err != nil || action(r) != "RestoreDBClusterToPointInTime" {
					return resp, err
				}
				_ = resp.Body.Close()
				if replaceAttempt {
					b, _ := r.GetBody()
					raw, _ := io.ReadAll(b)
					_ = b.Close()
					form, _ := url.ParseQuery(string(raw))
					s.SetTags(form.Get("DBClusterIdentifier"), map[string]string{"antifailure:attempt": "another-attempt"})
				}
				return nil, io.ErrUnexpectedEOF
			}))
			b, err := p.Branch(ctx, v.ID, "lost-branch")
			require.Error(t, err)
			require.Contains(t, b.ProviderRef, "#")
			aurora.SetHTTPForTest(p, http.DefaultTransport)
			err = p.Destroy(ctx, b)
			if replaceAttempt {
				require.ErrorIs(t, err, aurora.ErrNotOurs)
			} else {
				require.NoError(t, err)
			}
			_, found := s.DatabaseOf(strings.SplitN(b.ProviderRef, "#", 2)[0])
			require.Equal(t, replaceAttempt, found)
		})
	}
}

func TestAmbiguousRestoreDoesNotDeleteExistingUnownedResource(t *testing.T) {
	s := newFake(t, seedSQL, "")
	p := newProvider(t, s)
	aurora.SetHTTPForTest(p, transportFunc(func(r *http.Request) (*http.Response, error) {
		if action(r) == "RestoreDBClusterToPointInTime" {
			b, _ := r.GetBody()
			raw, _ := io.ReadAll(b)
			_ = b.Close()
			form, _ := url.ParseQuery(string(raw))
			require.NoError(t, s.SeedSource(form.Get("DBClusterIdentifier"), seedSQL))
			return nil, io.ErrUnexpectedEOF
		}
		return http.DefaultTransport.RoundTrip(r)
	}))
	g, _ := spec("ambiguous")
	v, err := p.RefreshGolden(context.Background(), g)
	require.Error(t, err)
	require.NotEmpty(t, v.ProviderRef)
	_, found := s.DatabaseOf(v.ProviderRef)
	require.True(t, found)
	require.NotContains(t, s.Actions(), "DeleteDBCluster")
}

func TestSweepCannotDeleteAnotherSourcesCandidate(t *testing.T) {
	s := newFake(t, seedSQL, "")
	p := newProvider(t, s)
	g, _ := spec("candidate")
	v, err := p.RefreshGolden(context.Background(), g)
	require.NoError(t, err)
	s.SetTags(v.ProviderRef, map[string]string{"antifailure:kind": "candidate", "antifailure:created": time.Now().Add(-48 * time.Hour).Format(time.RFC3339Nano)})
	require.NoError(t, s.SeedSource("other-source", seedSQL))
	opts := options(t, s)
	opts.SourceCluster = "other-source"
	q, err := scopedNew(context.Background(), opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = q.Close() })
	_, err = q.RefreshGolden(context.Background(), g)
	require.NoError(t, err)
	_, exists := s.DatabaseOf(v.ProviderRef)
	require.True(t, exists)
	_, err = p.RefreshGolden(context.Background(), g)
	require.NoError(t, err)
	_, exists = s.DatabaseOf(v.ProviderRef)
	require.False(t, exists)
}

func TestPlaintextIsRefusedForARemoteEndpoint(t *testing.T) {
	s := newFake(t, seedSQL, "")
	p := newProvider(t, s)
	g, _ := spec("plaintext")
	v, err := p.RefreshGolden(context.Background(), g)
	require.NoError(t, err)
	b, err := p.Branch(context.Background(), v.ID, "plaintext-env")
	require.NoError(t, err)
	_, err = p.ConnString(context.Background(), b, provider.ConnDirect)
	require.NoError(t, err, "the loopback fixture may use sslmode disable")
	s.SetEndpoint("branch.cluster-fixture.eu-west-1.rds.amazonaws.com", 5432)
	_, err = p.ConnString(context.Background(), b, provider.ConnDirect)
	require.ErrorContains(t, err, "loopback", "a copy of production was handed out over plaintext")
}
