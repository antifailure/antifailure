package local

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/moby/moby/client"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
)

// The failure this file pins, from 2026-09-22 at 00:36 UTC, mid take:
//
//	AF-RUN-040 The environment could not be placed: Error response from
//	daemon: all predefined address pools have been fully subnetted
//	  Next: Run 'af doctor' to check the runtime, then 'af down' to clear
//	  anything left behind.
//
// Docker held thirty networks against default pools of about thirty one, and
// fourteen were Antifailure networks with nothing attached, left by killed test
// runs. Neither remedy it named could free one: doctor reported the runtime
// healthy and af down removes only the checked out branch's environment. The
// real daemon is never exhausted here, because other work depends on it, so a
// fake daemon answers with the exact error the real one gave.

// fakeNetwork is one network the fake daemon holds.
type fakeNetwork struct {
	id, name string
	ours     bool
	attached int
}

// poolDaemon is a Docker daemon that refuses every new network with the given
// message, and holds the given networks.
//
// The list endpoint answers with no Containers at all, the way the real one
// does. Only inspect carries them, so a count taken off the list would see
// every network as empty, and that is a mistake this has to be able to catch.
func poolDaemon(t *testing.T, refusal string, nets []fakeNetwork, inspectFails bool) *Runtime {
	t.Helper()
	byID := map[string]fakeNetwork{}
	for _, n := range nets {
		byID[n.id] = n
	}
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := req.URL.Path
		labelsOf := func(n fakeNetwork) map[string]string {
			if !n.ours {
				return map[string]string{"com.docker.compose.project": "someone-else"}
			}
			return map[string]string{
				dockerutil.LabelManaged: dockerutil.ManagedValue,
				dockerutil.LabelEnv:     "env-" + n.id,
				dockerutil.LabelKind:    dockerutil.KindNetwork,
			}
		}
		switch {
		case req.Method == http.MethodGet && strings.HasSuffix(path, "/containers/json"):
			_, _ = w.Write([]byte("[]"))
		case req.Method == http.MethodPost && strings.HasSuffix(path, "/networks/create"):
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": refusal})
		case req.Method == http.MethodGet && strings.HasSuffix(path, "/networks"):
			list := []map[string]any{}
			for _, n := range nets {
				list = append(list, map[string]any{
					"Name": n.name, "Id": n.id, "Labels": labelsOf(n), "Containers": map[string]any{},
				})
			}
			_ = json.NewEncoder(w).Encode(list)
		case req.Method == http.MethodGet && strings.Contains(path, "/networks/"):
			id := path[strings.LastIndex(path, "/")+1:]
			n, ok := byID[id]
			if !ok {
				// The environment's own network, asked for by name before the
				// create. Not there, so the runtime goes on to create it.
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(map[string]string{"message": "network " + id + " not found"})
				return
			}
			if inspectFails {
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{"message": "daemon busy"})
				return
			}
			containers := map[string]any{}
			for i := 0; i < n.attached; i++ {
				containers[fmt.Sprintf("c%d%s", i, n.id)] = map[string]any{"Name": "attached"}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Name": n.name, "Id": n.id, "Labels": labelsOf(n), "Containers": containers,
			})
		default:
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "unexpected " + req.Method + " " + path})
		}
	}))
	t.Cleanup(daemon.Close)
	cli, err := client.New(client.WithHost(daemon.URL), client.WithAPIVersion("1.44"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = cli.Close() })
	return &Runtime{cli: cli, clock: clock.New()}
}

// theIncident is the daemon as it was measured: thirty networks, fourteen of
// them ours with nothing attached, two of ours in use, and fourteen belonging
// to other tools.
func theIncident() []fakeNetwork {
	var nets []fakeNetwork
	for i := 0; i < 14; i++ {
		nets = append(nets, fakeNetwork{id: fmt.Sprintf("orphan%02d", i), name: fmt.Sprintf("af-net-orphan%02d", i), ours: true})
	}
	nets = append(nets,
		fakeNetwork{id: "live01", name: "af-net-live", ours: true, attached: 4},
		fakeNetwork{id: "live02", name: "af-edge-live", ours: true, attached: 2},
	)
	for i := 0; i < 14; i++ {
		// Other tools' networks, and some of them are empty too. An empty
		// network somebody else made is not ours to count or to remove.
		nets = append(nets, fakeNetwork{id: fmt.Sprintf("foreign%02d", i), name: fmt.Sprintf("compose_%02d", i)})
	}
	return nets
}

func TestAnExhaustedAddressPoolNamesTheRemedyThatFreesOne(t *testing.T) {
	const refusal = "all predefined address pools have been fully subnetted"
	r := poolDaemon(t, refusal, theIncident(), false)

	_, err := r.ensureOneNetwork(context.Background(), "ledger-main-3e9f66",
		innerNetworkName("ledger-main-3e9f66"), true, func(string, string) error { return nil })
	require.Error(t, err)

	var coded *aferrors.Error
	require.True(t, errors.As(err, &coded), "not a coded error: %v", err)
	require.Equal(t, aferrors.AFRUN052, coded.Code(),
		"the daemon being full is not the environment failing to place, and the remedy AF-RUN-040 "+
			"prints frees nothing")
	require.Contains(t, coded.NextStep(), "af env prune --orphaned",
		"the remedy has to be the command that removes the networks holding the ranges")
	require.NotContains(t, coded.NextStep(), "af down",
		"af down removes one branch's environment and cannot free a range another run is holding")

	msg := coded.Message()
	require.Contains(t, msg, "The daemon holds 30 networks",
		"every network counts against the pools, not only ours")
	require.Contains(t, msg, "14 of them are Antifailure networks with no container attached",
		"the count has to be ours with nothing attached: not the two in use, and not the foreign ones")
	require.ErrorContains(t, err, refusal, "the daemon's own words are kept as the cause")
}

func TestTheOlderSpellingOfAnExhaustedPoolIsRecognisedToo(t *testing.T) {
	// What Docker said before it said "fully subnetted".
	const refusal = "could not find an available, non-overlapping IPv4 address pool among the defaults to assign to the network"
	r := poolDaemon(t, refusal, theIncident(), false)
	_, err := r.ensureOneNetwork(context.Background(), "e", innerNetworkName("e"), true,
		func(string, string) error { return nil })
	var coded *aferrors.Error
	require.True(t, errors.As(err, &coded), "not a coded error: %v", err)
	require.Equal(t, aferrors.AFRUN052, coded.Code())
}

func TestAnyOtherRefusedNetworkIsStillAnEnvironmentThatCouldNotBePlaced(t *testing.T) {
	// The narrowing has to be narrow. A different refusal keeps AF-RUN-040,
	// because telling somebody to prune orphans for an error that has nothing
	// to do with address space would be the same mistake the other way round.
	r := poolDaemon(t, "plugin \"weave\" not found", theIncident(), false)
	_, err := r.ensureOneNetwork(context.Background(), "e", innerNetworkName("e"), true,
		func(string, string) error { return nil })
	var coded *aferrors.Error
	require.True(t, errors.As(err, &coded), "not a coded error: %v", err)
	require.Equal(t, aferrors.AFRUN040, coded.Code())
}

func TestAPoolErrorWhoseCountCouldNotBeTakenClaimsNoCount(t *testing.T) {
	// Every inspect fails. A network whose attachments could not be read is
	// not an empty network, so the message must not say fourteen, and a bare
	// zero would be a number nobody measured: it has to say what it could not
	// count.
	r := poolDaemon(t, "all predefined address pools have been fully subnetted", theIncident(), true)
	_, err := r.ensureOneNetwork(context.Background(), "e", innerNetworkName("e"), true,
		func(string, string) error { return nil })
	var coded *aferrors.Error
	require.True(t, errors.As(err, &coded), "not a coded error: %v", err)
	require.Equal(t, aferrors.AFRUN052, coded.Code())
	require.Contains(t, coded.Message(), "0 of them are Antifailure networks with no container attached",
		"an uninspectable network is never counted as orphaned")
	require.Contains(t, coded.Message(), "16 more Antifailure networks could not be inspected",
		"and the zero says why it is zero, so it is not read as a measurement")
}

func TestInventoryCarriesHowManyContainersAreAttachedToANetwork(t *testing.T) {
	r := poolDaemon(t, "", []fakeNetwork{
		{id: "orphan", name: "af-net-orphan", ours: true},
		{id: "live", name: "af-net-live", ours: true, attached: 3},
	}, false)
	counts := inventoryNetworkLabels(t, r)
	require.Equal(t, "0", counts["orphan"], "a network with nothing on it reads as zero attached")
	require.Equal(t, "3", counts["live"],
		"the count comes from inspect: the list endpoint reports no containers for any network")
}

func TestInventoryLeavesTheCountOutWhenItCouldNotBeRead(t *testing.T) {
	r := poolDaemon(t, "", []fakeNetwork{{id: "orphan", name: "af-net-orphan", ours: true}}, true)
	labels := inventoryNetworkLabels(t, r)
	v, present := labels["orphan"]
	require.False(t, present, "a count nobody could take was reported as %q", v)
}

// inventoryNetworkLabels runs Inventory against the fake and returns each
// network's attached label, present only when the runtime set one.
func inventoryNetworkLabels(t *testing.T, r *Runtime) map[string]string {
	t.Helper()
	items, err := r.Inventory(context.Background())
	require.NoError(t, err)
	out := map[string]string{}
	for _, res := range items {
		if v, ok := res.Labels["attached"]; ok {
			out[res.ID] = v
		}
	}
	return out
}
