// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
package azurepg_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path"
	"strings"
	"testing"

	"github.com/antifailure/antifailure/ee/engine/db/azurepg"
	"github.com/antifailure/antifailure/engine/pkg/secret"
	"github.com/stretchr/testify/require"
)

func TestRetryFinishesAnAcceptedBranchWhosePreparationWasInterrupted(t *testing.T) {
	server := newFake(t, seedSQL)
	original := newProvider(t, server)
	ctx := context.Background()
	golden, err := original.RefreshGolden(ctx, goldenSpec())
	require.NoError(t, err)
	opts := options(t, server)
	var pending string
	opts.HTTPClient = cancellingTransport(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodPut && !strings.Contains(req.URL.Path, "/firewallRules/") {
			pending = path.Base(req.URL.Path)
		}
		if req.Method == http.MethodPatch || req.Method == http.MethodDelete {
			return &http.Response{StatusCode: 500, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":{"code":"Unavailable","message":"simulated interruption"}}`))}, nil
		}
		return http.DefaultClient.Do(req)
	})
	interrupted, err := azurepg.NewWithFixtureRoles(opts)
	require.NoError(t, err)
	defer interrupted.Close()
	_, err = interrupted.Branch(ctx, golden.ID, "resume-after-interruption")
	require.Error(t, err)
	require.NotEmpty(t, pending)
	require.True(t, server.Exists(pending))
	branch, err := original.Branch(ctx, golden.ID, "resume-after-interruption")
	require.NoError(t, err)
	health, err := original.Health(ctx, branch)
	require.NoError(t, err)
	require.True(t, health.Reachable)
	_, err = original.Branch(ctx, "a-different-golden", "resume-after-interruption")
	require.ErrorContains(t, err, "does not match")
}

func TestRetryWithANewBranchKeyRotatesTheExistingCredential(t *testing.T) {
	server := newFake(t, seedSQL)
	original := newProvider(t, server)
	ctx := context.Background()
	golden, err := original.RefreshGolden(ctx, goldenSpec())
	require.NoError(t, err)
	branch, err := original.Branch(ctx, golden.ID, "key-rotation")
	require.NoError(t, err)
	before, ok := server.PasswordOf(branch.ProviderRef)
	require.True(t, ok)
	opts := options(t, server)
	opts.BranchKey = secret.New("AF_FAKE_REPLACEMENT_BRANCH_KEY")
	replacement, err := azurepg.NewWithFixtureRoles(opts)
	require.NoError(t, err)
	defer replacement.Close()
	resumed, err := replacement.Branch(ctx, golden.ID, "key-rotation")
	require.NoError(t, err)
	health, err := replacement.Health(ctx, resumed)
	require.NoError(t, err)
	require.True(t, health.Reachable)
	after, ok := server.PasswordOf(branch.ProviderRef)
	require.True(t, ok)
	require.True(t, before != after, "the branch retained the former credential")
}

type unreadableBody struct{}

func (unreadableBody) Read([]byte) (int, error) {
	return 0, errors.New("response interrupted after acceptance")
}
func (unreadableBody) Close() error { return nil }

func TestAcceptedRestoreWithAnUnreadableResponseIsCleanedUp(t *testing.T) {
	for _, branching := range []bool{false, true} {
		t.Run(map[bool]string{false: "golden", true: "branch"}[branching], func(t *testing.T) {
			server := newFake(t, seedSQL)
			original := newProvider(t, server)
			ctx := context.Background()
			version := ""
			if branching {
				golden, err := original.RefreshGolden(ctx, goldenSpec())
				require.NoError(t, err)
				version = golden.ID
			}
			before := server.ResourceCount()
			opts := options(t, server)
			opts.HTTPClient = cancellingTransport(func(req *http.Request) (*http.Response, error) {
				resp, err := http.DefaultClient.Do(req)
				if err == nil && req.Method == http.MethodPut && !strings.Contains(req.URL.Path, "/firewallRules/") {
					_ = resp.Body.Close()
					resp.Body = unreadableBody{}
				}
				return resp, err
			})
			p, err := azurepg.NewWithFixtureRoles(opts)
			require.NoError(t, err)
			defer p.Close()
			if branching {
				_, err = p.Branch(ctx, version, "unreadable-create")
			} else {
				_, err = p.RefreshGolden(ctx, goldenSpec())
			}
			require.Error(t, err)
			require.Equal(t, before, server.ResourceCount())
			require.ErrorContains(t, err, "response could not be read")
		})
	}
}

func TestAnotherSourceCannotInventoryOrDestroyThisProvidersBranch(t *testing.T) {
	server := newFake(t, seedSQL)
	original := newProvider(t, server)
	ctx := context.Background()
	golden, err := original.RefreshGolden(ctx, goldenSpec())
	require.NoError(t, err)
	branch, err := original.Branch(ctx, golden.ID, "owned-branch")
	require.NoError(t, err)
	opts := options(t, server)
	opts.SourceServer = "another-source"
	other, err := azurepg.NewWithFixtureRoles(opts)
	require.NoError(t, err)
	defer other.Close()
	inventory, err := other.Inventory(ctx)
	require.NoError(t, err)
	require.Empty(t, inventory)
	require.ErrorIs(t, other.Destroy(ctx, branch), azurepg.ErrNotOurs)
	require.True(t, server.Exists(branch.ProviderRef))
}
