package manifest_test

// database.image, database.extensions and database.preload_libraries, the
// three keys that say what the Postgres a golden is built in actually is.
//
// They configure the container the docker provider starts, and there is no
// container for any other provider to configure. A hosted provider furnishes
// its own Postgres: there is no image to choose, no server this engine may
// load a library into, and creating an extension would be this engine reaching
// into somebody's managed instance.
//
// So they are REFUSED beside another provider rather than ignored, which is
// the rule this repository learned from replicas, resources.cpu and
// resources.memory. Here the belief a silently ignored field would create is
// specifically dangerous: what somebody believes is that the golden carries
// PostGIS, and the way they find out is a preview whose schema will not load.

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const withDatabaseProvider = `
version: 1
name: shop
services:
  - name: web
    port: 3000
database:
  provider: `

func TestParse_AcceptsAnImageAndItsExtensionsForTheDockerProvider(t *testing.T) {
	t.Parallel()
	m := mustParse(t, withDatabaseProvider+`docker
  image: pgvector/pgvector:pg17
  extensions:
    - vector
    - pg_trgm
  preload_libraries:
    - timescaledb
`)
	require.Equal(t, "pgvector/pgvector:pg17", m.Database.Image)
	require.Equal(t, []string{"vector", "pg_trgm"}, m.Database.Extensions)
	require.Equal(t, []string{"timescaledb"}, m.Database.PreloadLibraries)
}

// One case per key, because a check written against the first of the three
// would pass while the other two were silently accepted, and a silently
// accepted key is the whole defect.
func TestParse_RefusesAContainerKeyBesideAProviderThatHasNoContainer(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		body  string
		field string
	}{
		{"image", "  image: pgvector/pgvector:pg17\n", "database.image"},
		{"extensions", "  extensions:\n    - vector\n", "database.extensions"},
		{"preload", "  preload_libraries:\n    - timescaledb\n", "database.preload_libraries"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := mustFail(t, withDatabaseProvider+"neon\n  project: shop\n"+tc.body)
			found := problems(t, err)

			var fields []string
			for _, p := range found {
				fields = append(fields, p.Path)
			}
			require.Containsf(t, fields, tc.field,
				"the manifest was refused, and not for %s, so the refusal is not evidence this "+
					"key is checked: %v", tc.field, fields)

			for _, p := range found {
				if p.Path != tc.field {
					continue
				}
				require.Contains(t, strings.ToLower(p.Message+" "+p.Hint), "docker",
					"the refusal does not say which provider the key belongs to")
			}
		})
	}
}

// The default provider is docker, and a manifest that names no provider at all
// has to keep working. Without this, the refusal above would reach every
// manifest that declares an image and leaves the provider implicit, which is
// most of them.
func TestParse_AcceptsAnImageWhenTheProviderIsLeftImplicit(t *testing.T) {
	t.Parallel()
	m := mustParse(t, `
version: 1
name: shop
services:
  - name: web
    port: 3000
database:
  image: postgis/postgis:17-3.5
  extensions:
    - postgis
`)
	require.Equal(t, "postgis/postgis:17-3.5", m.Database.Image)
	require.Equal(t, []string{"postgis"}, m.Database.Extensions)
}
