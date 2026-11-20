package cli

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// af up --only, on the shape a design partner needed it for: two services
// worth bringing up out of a manifest of many that could never pass a health
// check.
func onlyManifest() *schema.Manifest {
	return &schema.Manifest{
		Services: []schema.Service{
			{Name: "web", DependsOn: []string{"api"}},
			{Name: "api", DependsOn: []string{"inngest"}},
			{Name: "agent-1"},
			{Name: "inngest"},
			{Name: "indexer"},
		},
		Datastores: []schema.Datastore{
			{Rebuild: &schema.DatastoreRebuild{Service: "indexer", Command: "index"}},
		},
	}
}

func TestOnlyServices_FollowsDependsOnAllTheWayDownInManifestOrder(t *testing.T) {
	t.Parallel()
	m := onlyManifest()
	narrowed, kept, err := onlyServices(m, []string{"web"})
	require.NoError(t, err)
	// indexer comes too, because af up runs the datastore rebuild in it.
	require.Equal(t, []string{"web", "api", "inngest", "indexer"}, kept)
	require.Len(t, narrowed.Services, 4)
	require.Len(t, m.Services, 5, "the loaded manifest is not modified")
}

func TestOnlyServices_LeavesOutWhatNothingNamed(t *testing.T) {
	t.Parallel()
	_, kept, err := onlyServices(onlyManifest(), []string{" inngest ", ""})
	require.NoError(t, err)
	require.Equal(t, []string{"inngest", "indexer"}, kept)
}

func TestOnlyServices_RefusesANameTheManifestDoesNotHave(t *testing.T) {
	t.Parallel()
	_, _, err := onlyServices(onlyManifest(), []string{"admin"})
	require.ErrorContains(t, err, `"admin"`)
	require.ErrorContains(t, err, "web, api, agent-1, inngest, indexer")

	_, _, err = onlyServices(onlyManifest(), []string{" "})
	require.ErrorContains(t, err, "no service names")
}
