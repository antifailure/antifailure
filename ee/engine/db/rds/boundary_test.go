// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package rds_test

// Which resources are this provider's, what it will hand out, and what it does
// with a response it never received.

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

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/db/rds"
	"github.com/antifailure/antifailure/ee/engine/db/rds/fakerds"
	"github.com/antifailure/antifailure/engine/conformance"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/secret"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// formOf reads a request's form without consuming the body the transport
// still has to send.
func formOf(r *http.Request) url.Values {
	b, _ := r.GetBody()
	defer func() { _ = b.Close() }()
	data, _ := io.ReadAll(b)
	form, _ := url.ParseQuery(string(data))
	return form
}

// A restore goes into the source's own subnet group and security group, with
// IAM authentication off and no public address, and the tags are sent in the
// form AWS's model names.
func TestRestorePreservesNetworkingAndDisablesIAM(t *testing.T) {
	s := newFake(t, conformance.DefaultSeedSQL, "")
	p := newProvider(t, s)
	version := refresh(t, p)
	_, err := p.Branch(context.Background(), version.ID, "network-env")
	require.NoError(t, err)

	restore := s.LastRequest("RestoreDBInstanceFromDBSnapshot")
	require.Equal(t, fakerds.FixtureSubnetGroup, restore.Get("DBSubnetGroupName"))
	require.Equal(t, fakerds.FixtureSecurityGroup, restore.Get("VpcSecurityGroupIds.VpcSecurityGroupId.1"))
	require.Equal(t, "false", restore.Get("EnableIAMDatabaseAuthentication"))
	require.Equal(t, "false", restore.Get("PubliclyAccessible"))
	require.NotEmpty(t, restore.Get("Tags.Tag.1.Key"), "tags were not sent as Tags.Tag.N")
	require.Empty(t, restore.Get("Tags.member.1.Key"), "tags were sent as Tags.member.N")
	require.Equal(t, "false", s.LastRequest("ModifyDBInstance").Get("EnableIAMDatabaseAuthentication"))
}

// An instance that reports IAM authentication or a public address is not
// handed out, whatever its tags say.
func TestABranchThatReportsIAMOrPublicAccessIsNotHandedOut(t *testing.T) {
	for _, tc := range []struct {
		name        string
		iam, public bool
	}{{"iam", true, false}, {"public", false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			s := newFake(t, conformance.DefaultSeedSQL, "")
			p := newProvider(t, s)
			version := refresh(t, p)
			b, err := p.Branch(context.Background(), version.ID, "security-env-"+tc.name)
			require.NoError(t, err)
			_, err = p.ConnString(context.Background(), b, provider.ConnDirect)
			require.NoError(t, err)
			s.SetInstanceSecurity(b.ProviderRef, tc.iam, tc.public)
			_, err = p.ConnString(context.Background(), b, provider.ConnDirect)
			require.Error(t, err)
		})
	}
}

// Two providers for one source in one process do not both count the same
// headroom under a branch limit.
func TestConcurrentProvidersReserveBranchAdmission(t *testing.T) {
	s := newFake(t, conformance.DefaultSeedSQL, "")
	opts := options(t, s)
	opts.MaxBranches = 1
	p, err := scopedNew(context.Background(), opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	q, err := scopedNew(context.Background(), opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = q.Close() })
	version := refresh(t, p)

	entered, release := make(chan struct{}), make(chan struct{})
	rds.SetHTTPForTest(p, transportFunc(func(r *http.Request) (*http.Response, error) {
		if formOf(r).Get("Action") == "RestoreDBInstanceFromDBSnapshot" {
			close(entered)
			<-release
		}
		return http.DefaultTransport.RoundTrip(r)
	}))
	done := make(chan error, 1)
	go func() { _, err := p.Branch(context.Background(), version.ID, "first"); done <- err }()
	<-entered

	var requests atomic.Int64
	rds.SetHTTPForTest(q, transportFunc(func(r *http.Request) (*http.Response, error) {
		requests.Add(1)
		return http.DefaultTransport.RoundTrip(r)
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	_, secondErr := q.Branch(ctx, version.ID, "second")
	cancel()
	observed := requests.Load()
	close(release)
	require.NoError(t, <-done)
	require.ErrorIs(t, secondErr, context.DeadlineExceeded)
	require.Zero(t, observed, "the second provider reached AWS while the first held admission")
	_, err = q.Branch(context.Background(), version.ID, "second")
	require.Error(t, err, "the second branch went past a limit of one")
}

// A second source in the same account sees none of the first source's
// resources, cannot delete them, and gets its own identifiers for the same
// environment.
func TestSourceScopeProtectsInventoryRetriesAndDestruction(t *testing.T) {
	s := newFake(t, conformance.DefaultSeedSQL, "")
	p := newProvider(t, s)
	ctx := context.Background()
	version := refresh(t, p)
	b, err := p.Branch(ctx, version.ID, "same-env")
	require.NoError(t, err)

	require.NoError(t, s.SeedSource("another-production", conformance.DefaultSeedSQL))
	opts := options(t, s)
	opts.SourceInstance = "another-production"
	q, err := scopedNew(ctx, opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = q.Close() })

	items, err := q.Inventory(ctx)
	require.NoError(t, err)
	require.Empty(t, items)
	versions, err := q.ListGoldens(ctx)
	require.NoError(t, err)
	require.Empty(t, versions)
	require.ErrorIs(t, q.Destroy(ctx, b), rds.ErrNotOurs)
	_, err = q.ConnString(ctx, b, provider.ConnDirect)
	require.Error(t, err)
	require.NoError(t, q.DestroyGolden(ctx, version.ID))
	_, exists := s.SnapshotDatabaseOf(version.ProviderRef)
	require.True(t, exists, "another source's DestroyGolden removed this source's golden")

	other := refresh(t, q)
	c, err := q.Branch(ctx, other.ID, "same-env")
	require.NoError(t, err)
	require.NotEqual(t, b.ProviderRef, c.ProviderRef)
}

// A retry returns an existing branch only when it is exactly the one asked for
// and it finished preparing.
func TestRetryRequiresExactVersionEnvironmentAndPreparation(t *testing.T) {
	s := newFake(t, conformance.DefaultSeedSQL, "")
	p := newProvider(t, s)
	ctx := context.Background()
	version := refresh(t, p)
	b, err := p.Branch(ctx, version.ID, "retry-env")
	require.NoError(t, err)
	u, err := p.ConnString(ctx, provider.Branch{EnvID: b.EnvID}, provider.ConnDirect)
	require.NoError(t, err)
	require.NoError(t, reachable(u.Reveal()))
	_, err = p.Branch(ctx, "different-version", b.EnvID)
	require.Error(t, err)

	for _, key := range []string{"antifailure:env", "antifailure:prepared", "antifailure:source"} {
		t.Run(key, func(t *testing.T) {
			other, err := p.Branch(ctx, version.ID, "retry-"+strings.ReplaceAll(key, ":", "-"))
			require.NoError(t, err)
			s.SetTags(other.ProviderRef, map[string]string{key: "incorrect"})
			_, err = p.Branch(ctx, version.ID, other.EnvID)
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
	_, err = q.Branch(ctx, version.ID, b.EnvID)
	require.Error(t, err, "a provider with a different branch key adopted a branch it could not have prepared")
}

// Tags anybody could write do not make a receipt match.
func TestReceiptBindsMetadataEvenWhenCallerMatchesForgedTags(t *testing.T) {
	s := newFake(t, conformance.DefaultSeedSQL, "")
	p := newProvider(t, s)
	ctx := context.Background()
	version := refresh(t, p)
	b, err := p.Branch(ctx, version.ID, "receipt-env")
	require.NoError(t, err)

	s.SetTags(b.ProviderRef, map[string]string{"antifailure:version": "forged-version"})
	_, err = p.Branch(ctx, "forged-version", b.EnvID)
	require.Error(t, err)

	s.SetTags(b.ProviderRef, map[string]string{"antifailure:version": version.ID, "antifailure:env": "forged-env"})
	b.EnvID = "forged-env"
	_, err = p.ConnString(ctx, b, provider.ConnDirect)
	require.Error(t, err)

	s.SetTags(version.ProviderRef, map[string]string{"antifailure:rules": "forged"})
	_, err = p.Branch(ctx, version.ID, "after-forged-golden")
	require.NoError(t, err, "a tag the receipt does not bind should not unpublish a golden")
	s.SetTags(version.ProviderRef, map[string]string{"antifailure:version": "another"})
	versions, err := p.ListGoldens(ctx)
	require.NoError(t, err)
	require.Empty(t, versions, "a golden whose version tag was changed is still published")
}

// A refresh with no masking step, no scanner, or a scanner that attests
// nothing publishes nothing.
func TestMissingVerificationCannotPublish(t *testing.T) {
	for _, kind := range []string{"mask", "verify", "attestation"} {
		t.Run(kind, func(t *testing.T) {
			s := newFake(t, conformance.DefaultSeedSQL, "")
			p := newProvider(t, s)
			g := verifiedSpec(nil)
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

// A branch interrupted after its restore returns a handle teardown can use,
// and is never handed out or adopted by a retry.
func TestInterruptedBranchReturnsHandleAndCannotBeAdopted(t *testing.T) {
	s := newFake(t, conformance.DefaultSeedSQL, "")
	p := newProvider(t, s)
	ctx := context.Background()
	version := refresh(t, p)
	rds.SetHTTPForTest(p, transportFunc(func(r *http.Request) (*http.Response, error) {
		if formOf(r).Get("Action") == "ModifyDBInstance" {
			return nil, fmt.Errorf("interrupted password rotation")
		}
		return http.DefaultTransport.RoundTrip(r)
	}))
	b, err := p.Branch(ctx, version.ID, "interrupted-env")
	require.Error(t, err)
	require.NotEmpty(t, b.ProviderRef)
	rds.SetHTTPForTest(p, http.DefaultTransport)
	_, err = p.Branch(ctx, version.ID, b.EnvID)
	require.Error(t, err, "a retry adopted a branch whose inherited credential was never rotated")
	_, err = p.ConnString(ctx, b, provider.ConnDirect)
	require.Error(t, err)
	require.NoError(t, p.Destroy(ctx, b))
	_, found := s.DatabaseOf(b.ProviderRef)
	require.False(t, found, "teardown left the interrupted branch behind")
}

type brokenBody struct{}

func (brokenBody) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (brokenBody) Close() error             { return nil }

// A candidate restore that AWS accepted and whose response was lost is still
// found and removed, however the response was lost.
func TestAcceptedRestoreUnreadableResponseIsCleaned(t *testing.T) {
	for _, kind := range []string{"read", "xml", "transport"} {
		t.Run(kind, func(t *testing.T) {
			s := newFake(t, conformance.DefaultSeedSQL, "")
			p := newProvider(t, s)
			rds.SetHTTPForTest(p, transportFunc(func(r *http.Request) (*http.Response, error) {
				resp, err := http.DefaultTransport.RoundTrip(r)
				if err != nil || formOf(r).Get("Action") != "RestoreDBInstanceFromDBSnapshot" {
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
			_, err := p.RefreshGolden(context.Background(), verifiedSpec(nil))
			require.Error(t, err)
			items, err := p.Inventory(context.Background())
			require.NoError(t, err)
			require.Empty(t, items, "a candidate whose restore response was lost was left billing")
		})
	}
}

// A branch restore whose response was lost returns a handle bound to its
// attempt, so teardown removes that instance and never a different one at the
// same name.
func TestLostBranchResponseCarriesAttemptBoundCleanupHandle(t *testing.T) {
	for _, replaceAttempt := range []bool{false, true} {
		t.Run(fmt.Sprint("replaced=", replaceAttempt), func(t *testing.T) {
			s := newFake(t, conformance.DefaultSeedSQL, "")
			p := newProvider(t, s)
			ctx := context.Background()
			version := refresh(t, p)
			rds.SetHTTPForTest(p, transportFunc(func(r *http.Request) (*http.Response, error) {
				resp, err := http.DefaultTransport.RoundTrip(r)
				form := formOf(r)
				if err != nil || form.Get("Action") != "RestoreDBInstanceFromDBSnapshot" {
					return resp, err
				}
				_ = resp.Body.Close()
				if replaceAttempt {
					s.SetTags(form.Get("DBInstanceIdentifier"), map[string]string{"antifailure:attempt": "another-attempt"})
				}
				return nil, io.ErrUnexpectedEOF
			}))
			b, err := p.Branch(ctx, version.ID, "lost-branch")
			require.Error(t, err)
			rds.SetHTTPForTest(p, http.DefaultTransport)
			require.Contains(t, b.ProviderRef, "#", "a lost restore response returned no attempt bound handle")
			name := strings.SplitN(b.ProviderRef, "#", 2)[0]
			err = p.Destroy(ctx, b)
			if replaceAttempt {
				// The instance at that name belongs to another attempt, so the
				// handle must refuse to remove it.
				require.ErrorIs(t, err, rds.ErrNotOurs)
			} else {
				require.NoError(t, err)
			}
			_, found := s.DatabaseOf(name)
			require.Equal(t, replaceAttempt, found,
				"teardown removed an instance it did not create, or left one it did")
		})
	}
}

// A lost response for a name somebody else already holds removes nothing.
func TestAmbiguousRestoreDoesNotDeleteExistingUnownedResource(t *testing.T) {
	s := newFake(t, conformance.DefaultSeedSQL, "")
	p := newProvider(t, s)
	rds.SetHTTPForTest(p, transportFunc(func(r *http.Request) (*http.Response, error) {
		form := formOf(r)
		if form.Get("Action") == "RestoreDBInstanceFromDBSnapshot" {
			require.NoError(t, s.SeedSource(form.Get("DBInstanceIdentifier"), conformance.DefaultSeedSQL))
			return nil, io.ErrUnexpectedEOF
		}
		return http.DefaultTransport.RoundTrip(r)
	}))
	v, err := p.RefreshGolden(context.Background(), verifiedSpec(nil))
	require.Error(t, err)
	require.NotEmpty(t, v.ProviderRef)
	_, found := s.DatabaseOf(v.ProviderRef)
	require.True(t, found, "an instance this provider did not create was deleted")
	require.NotContains(t, s.Actions(), "DeleteDBInstance")
}

// The orphan sweep removes this source's stale candidates and nobody else's.
func TestSweepCannotDeleteAnotherSourcesCandidate(t *testing.T) {
	s := newFake(t, conformance.DefaultSeedSQL, "")
	p := newProvider(t, s)
	ctx := context.Background()
	refresh(t, p)

	// A stale candidate of this source, made by relabelling a branch.
	b, err := p.Branch(ctx, refresh(t, p).ID, "stale")
	require.NoError(t, err)
	s.SetTags(b.ProviderRef, map[string]string{
		"antifailure:kind":    "candidate",
		"antifailure:created": time.Now().Add(-48 * time.Hour).Format(time.RFC3339Nano),
	})

	require.NoError(t, s.SeedSource("other-source", conformance.DefaultSeedSQL))
	opts := options(t, s)
	opts.SourceInstance = "other-source"
	q, err := scopedNew(ctx, opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = q.Close() })
	refresh(t, q)
	_, exists := s.DatabaseOf(b.ProviderRef)
	require.True(t, exists, "another source's refresh swept this source's candidate")

	refresh(t, p)
	_, exists = s.DatabaseOf(b.ProviderRef)
	require.False(t, exists, "this source's own stale candidate was not swept")
}

// Plaintext is refused for anything but a loopback endpoint.
func TestPlaintextIsRefusedForARemoteEndpoint(t *testing.T) {
	s := newFake(t, conformance.DefaultSeedSQL, "")
	p := newProvider(t, s)
	version := refresh(t, p)
	b, err := p.Branch(context.Background(), version.ID, "plaintext-env")
	require.NoError(t, err)
	_, err = p.ConnString(context.Background(), b, provider.ConnDirect)
	require.NoError(t, err, "the loopback fixture may use sslmode disable")
	s.SetEndpoint("af-b-x.abcdefghij.eu-west-1.rds.amazonaws.com", 5432)
	_, err = p.ConnString(context.Background(), b, provider.ConnDirect)
	require.ErrorContains(t, err, "loopback", "a copy of production was handed out over plaintext")
}
