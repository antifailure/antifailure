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
)

type cancellingTransport func(*http.Request) (*http.Response, error)

func (f cancellingTransport) Do(r *http.Request) (*http.Response, error) { return f(r) }
func TestCancellationAfterAcceptanceRemovesTheUnfinishedResource(t *testing.T) {
	for _, branching := range []bool{false, true} {
		t.Run(map[bool]string{false: "golden", true: "branch"}[branching], func(t *testing.T) {
			server := newFake(t, seedSQL)
			opts := options(t, server)
			original, err := azurepg.New(opts)
			require.NoError(t, err)
			defer func() { _ = original.Close() }()
			version := ""
			if branching {
				golden, err := original.RefreshGolden(context.Background(), goldenSpec())
				require.NoError(t, err)
				version = golden.ID
			}
			before := server.ResourceCount()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var once sync.Once
			opts.HTTPClient = cancellingTransport(func(req *http.Request) (*http.Response, error) {
				if strings.Contains(req.URL.Path, "/operations/") {
					once.Do(cancel)
				}
				return http.DefaultClient.Do(req)
			})
			p, err := azurepg.New(opts)
			require.NoError(t, err)
			defer func() { _ = p.Close() }()
			if branching {
				_, err = p.Branch(ctx, version, "cancel-during-operation")
			} else {
				_, err = p.RefreshGolden(ctx, goldenSpec())
			}
			require.ErrorIs(t, err, context.Canceled)
			require.Equal(t, before, server.ResourceCount(), "an accepted operation survived cancellation before readiness")
		})
	}
}
