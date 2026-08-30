package local

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// The environment's URL is the service the manifest declares first.
//
// The regression, and it blocked every workflow: Env.URL returns the first web
// service, and the list was in start order. A web application that depends on
// an API starts second, so the API became the environment's address. `af up`
// printed it, `af test` drove it with a browser, and six workflows blocked on
// a login form that was never going to be there. What a person saw was a
// Playwright timeout about a label.
//
// Start order is an implementation detail of bringing things up. The manifest
// is where the author says which service the product is.
func TestSortByManifestOrder_TheURLIsTheFirstDeclaredService(t *testing.T) {
	t.Parallel()
	declared := []provider.ServiceSpec{
		{Name: "app", Kind: "web"},
		{Name: "api", Kind: "web"},
	}
	// As the dependency sort produces them: app depends on api, so api starts
	// first.
	started := []provider.RunningService{
		{Name: "api", Kind: "web", URL: "http://127.0.0.1:46000"},
		{Name: "app", Kind: "web", URL: "http://127.0.0.1:46001"},
	}

	sortByManifestOrder(started, declared)

	env := provider.Env{Services: started}
	require.Equal(t, "http://127.0.0.1:46001", env.URL(),
		"the address a person opens has to be the application, not its backend")
	require.Equal(t, "app", started[0].Name)
}

// A service the manifest no longer declares keeps its place rather than being
// given an invented one. That happens when a branch is checked out over a
// running environment.
func TestSortByManifestOrder_KeepsAnUndeclaredService(t *testing.T) {
	t.Parallel()
	declared := []provider.ServiceSpec{{Name: "app", Kind: "web"}}
	running := []provider.RunningService{
		{Name: "worker"},
		{Name: "app", Kind: "web", URL: "http://127.0.0.1:46001"},
	}
	sortByManifestOrder(running, declared)
	require.Equal(t, "app", running[0].Name)
	require.Equal(t, "worker", running[1].Name)
}

// Alphabetical order is not manifest order, which is the shape the Status path
// had: `api` sorts before `app`.
func TestSortByManifestOrder_IsNotAlphabetical(t *testing.T) {
	t.Parallel()
	declared := []provider.ServiceSpec{{Name: "zebra", Kind: "web"}, {Name: "alpha", Kind: "web"}}
	running := []provider.RunningService{{Name: "alpha"}, {Name: "zebra"}}
	sortByManifestOrder(running, declared)
	require.Equal(t, "zebra", running[0].Name)
}
