// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
package azurepg

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCollectionsContinuePastMalformedRowsAndFirstPage(t *testing.T) {
	for _, collection := range []string{"servers", "databases"} {
		t.Run(collection, func(t *testing.T) {
			var calls atomic.Int32
			var host string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path == "/second" {
					fmt.Fprint(w, `{"value":[{"name":"last"}]}`)
					return
				}
				fmt.Fprintf(w, `{"value":[{"name":"first"},23,null,{"name":42}],"nextLink":%q}`, host+"/second")
			}))
			defer srv.Close()
			host = srv.URL
			api, err := newARMAPI(Options{Endpoint: host, Token: func(context.Context) (string, error) { return "AF_FAKE_TOKEN", nil }})
			require.NoError(t, err)
			var names []string
			if collection == "servers" {
				rows, err := api.listServers(context.Background())
				require.NoError(t, err)
				for _, row := range rows {
					names = append(names, row.Name)
				}
			} else {
				rows, err := api.listDatabases(context.Background(), "source")
				require.NoError(t, err)
				for _, row := range rows {
					names = append(names, row.Name)
				}
			}
			require.Equal(t, []string{"first", "last"}, names)
			require.EqualValues(t, 2, calls.Load())
		})
	}
}

func TestContinuationCannotSendIdentityToAnotherOrigin(t *testing.T) {
	var calls atomic.Int32
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); fmt.Fprint(w, `{"status":"Succeeded"}`) }))
	defer foreign.Close()
	api, err := newARMAPI(Options{Endpoint: "https://management.azure.com", Token: func(context.Context) (string, error) { return "AF_FAKE_TOKEN", nil }})
	require.NoError(t, err)
	err = api.wait(context.Background(), &asyncResult{Poll: foreign.URL}, time.Millisecond)
	require.ErrorContains(t, err, "crossed the configured origin")
	require.Zero(t, calls.Load())
}

func TestRepeatedContinuationIsRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `{"value":[],"nextLink":"/repeat"}`) }))
	defer srv.Close()
	api, err := newARMAPI(Options{Endpoint: srv.URL, Token: func(context.Context) (string, error) { return "AF_FAKE_TOKEN", nil }})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = api.listServers(ctx)
	require.ErrorContains(t, err, "continuation repeated")
}

func TestTagChunksSurviveUnicodeJSONRoundTrip(t *testing.T) {
	original := strings.Repeat("x", 255) + strings.Repeat("雪", 120)
	tags, err := chunkTag("proof", original)
	require.NoError(t, err)
	wire, err := json.Marshal(tags)
	require.NoError(t, err)
	var decoded map[string]string
	require.NoError(t, json.Unmarshal(wire, &decoded))
	require.Equal(t, original, unchunkTag("proof", decoded))
	for _, value := range decoded {
		require.LessOrEqual(t, len(value), tagValueMax)
	}
	_, err = chunkTag("proof", string([]byte{255}))
	require.ErrorContains(t, err, "UTF-8")
}

func TestDatabaseAmbiguityRequiresAnExplicitSelection(t *testing.T) {
	found := []database{{Name: "orders"}, {Name: "billing"}, {Name: "postgres"}}
	_, err := pickDatabase("", found)
	require.ErrorContains(t, err, DatabaseVariable)
	selected, err := pickDatabase("billing", found)
	require.NoError(t, err)
	require.Equal(t, "billing", selected)
}
