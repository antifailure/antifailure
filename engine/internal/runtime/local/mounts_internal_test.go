package local

import (
	"archive/tar"
	"bytes"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

func tarEntries(t *testing.T, body []byte) map[string]*tar.Header {
	t.Helper()
	out := map[string]*tar.Header{}
	tr := tar.NewReader(bytes.NewReader(body))
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return out
		}
		require.NoError(t, err)
		out[h.Name] = h
	}
}

// Every ancestor of the target is in the archive, because Docker's extraction
// creates a parent only when the archive names it, and the target of a mount is
// routinely a directory the image does not have.
func TestMountTar_ASingleFileCarriesEveryParent(t *testing.T) {
	body, err := mountTar(time.Unix(0, 0), provider.MountSpec{
		At:    "/etc/clickhouse-server/config.d/keeper.xml",
		Files: []provider.MountFile{{Mode: 0o644, Data: []byte("<k/>")}},
	})
	require.NoError(t, err)
	got := tarEntries(t, body)
	for _, dir := range []string{"etc/", "etc/clickhouse-server/", "etc/clickhouse-server/config.d/"} {
		require.Contains(t, got, dir, "the archive does not create %s", dir)
		require.Equal(t, byte(tar.TypeDir), got[dir].Typeflag)
	}
	f := got["etc/clickhouse-server/config.d/keeper.xml"]
	require.NotNil(t, f, "the file itself is not in the archive")
	require.Equal(t, int64(0o644), f.Mode)
}

func TestMountTar_ADirectoryPlacesItsFilesUnderTheTarget(t *testing.T) {
	body, err := mountTar(time.Unix(0, 0), provider.MountSpec{
		At: "/docker-entrypoint-initdb.d",
		Files: []provider.MountFile{
			{Rel: "roles.sql", Mode: 0o644, Data: []byte("a")},
			{Rel: "init/data.sql", Mode: 0o755, Data: []byte("b")},
		},
	})
	require.NoError(t, err)
	got := tarEntries(t, body)
	require.Contains(t, got, "docker-entrypoint-initdb.d/")
	require.Contains(t, got, "docker-entrypoint-initdb.d/init/")
	require.Contains(t, got, "docker-entrypoint-initdb.d/roles.sql")
	require.Equal(t, int64(0o755), got["docker-entrypoint-initdb.d/init/data.sql"].Mode)
}

// A hand built spec is the only way to reach this, and it is still refused: a
// Rel that climbs out would write outside the mount's own target.
func TestMountTar_RefusesAFileThatLeavesItsTarget(t *testing.T) {
	_, err := mountTar(time.Unix(0, 0), provider.MountSpec{
		At: "/init", Files: []provider.MountFile{{Rel: "../etc/passwd", Data: []byte("x")}},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "leaves its target")
}

func TestMountTar_RefusesTheRootAndAnEmptyMount(t *testing.T) {
	_, err := mountTar(time.Unix(0, 0), provider.MountSpec{At: "/", Files: []provider.MountFile{{Data: []byte("x")}}})
	require.Error(t, err)
	_, err = mountTar(time.Unix(0, 0), provider.MountSpec{At: "/etc/x"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "carries no files")
}

func TestVolumeMounts_AreNamedVolumesScopedToTheEnvironment(t *testing.T) {
	got := volumeMounts("e1", provider.ServiceSpec{Mounts: []provider.MountSpec{
		{At: "/data", Volume: "pgdata"},
		{At: "/etc/x", Files: []provider.MountFile{{Data: []byte("x")}}},
	}})
	require.Len(t, got, 1, "a file mount became a daemon mount, which is the path a bind would take")
	require.Equal(t, "volume", string(got[0].Type))
	require.Equal(t, "af-vol-e1-pgdata", got[0].Source)
	require.Equal(t, "/data", got[0].Target)
}

func TestReadinessCheckOf_NamesTheCheckEachShapeCarries(t *testing.T) {
	cases := []struct {
		s    provider.ServiceSpec
		want string
	}{
		{provider.ServiceSpec{Kind: "worker"}, checkNone},
		{provider.ServiceSpec{Kind: "worker", HealthCommand: "true"}, checkCommand},
		{provider.ServiceSpec{Kind: "web", Port: 80}, checkPort},
		{provider.ServiceSpec{Kind: "web", Port: 80, HealthPath: "/h"}, checkHTTP},
		{provider.ServiceSpec{Kind: "web", Port: 80, HealthPath: "/h", HealthCommand: "true"}, checkCommand},
		{provider.ServiceSpec{Kind: "cron"}, checkSchedule},
	}
	for _, c := range cases {
		require.Equal(t, c.want, readinessCheckOf(c.s), "%+v", c.s)
	}
}

func TestStatusReadiness_ARunningContainerWithNoCheckIsUnproved(t *testing.T) {
	require.Equal(t, provider.ReadinessUnproved,
		statusReadiness(map[string]string{dockerutil.LabelReadinessCheck: checkNone}))
	require.Equal(t, provider.ReadinessUnproved, statusReadiness(map[string]string{}),
		"a container from before the label read as proved, which is a promise nothing made")
	require.Equal(t, provider.ReadinessProved,
		statusReadiness(map[string]string{dockerutil.LabelReadinessCheck: checkCommand}))
}
