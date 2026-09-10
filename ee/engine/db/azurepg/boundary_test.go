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
				fmt.Fprintf(w, `{"value":[{"name":"first"},23,null,{}, {"name":42}],"nextLink":%q}`, host+"/second")
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

func TestAzureLocationDisplayNameMatchesItsCanonicalRegion(t *testing.T) {
	p := &Provider{opts: Options{Location: "centralus", AllowCIDR: "203.0.113.4/32"}}
	require.NoError(t, p.refuseAccessCrossing(&server{Location: "Central US"}))
	require.ErrorContains(t, p.refuseAccessCrossing(&server{Location: "East US"}), "differs")
}

func TestRestoreSendsTheCanonicalLocation(t *testing.T) {
	var location string
	var restoreAt string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request restoreRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			w.WriteHeader(400)
			return
		}
		location = request.Location
		restoreAt = request.Properties.PointInTimeUTC
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	api, err := newARMAPI(Options{Endpoint: srv.URL, Token: func(context.Context) (string, error) { return "AF_FAKE_TOKEN", nil }})
	require.NoError(t, err)
	at := time.Date(2026, 9, 10, 1, 2, 3, 123456789, time.UTC)
	_, err = api.restore(context.Background(), "source", "copy", "Central US", networkProps{}, at, nil)
	require.NoError(t, err)
	require.Equal(t, "centralus", location)
	require.Equal(t, at.Format(time.RFC3339Nano), restoreAt)
}

func TestResourceNamesSeparateSourcesAndAccounts(t *testing.T) {
	base := Options{Subscription: "one", ResourceGroup: "group", SourceServer: "source"}
	first := (&Provider{opts: base}).serverName(branchPrefix, "same-environment")
	for _, field := range []string{"source", "group", "subscription"} {
		t.Run(field, func(t *testing.T) {
			changed := base
			switch field {
			case "source":
				changed.SourceServer = "another"
			case "group":
				changed.ResourceGroup = "another"
			case "subscription":
				changed.Subscription = "another"
			}
			require.NotEqual(t, first, (&Provider{opts: changed}).serverName(branchPrefix, "same-environment"))
		})
	}
}
