package env

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// readMounts, the one place a mount touches the host's filesystem.

func writeMountFile(t *testing.T, root, rel, body string, mode os.FileMode) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(body), mode))
	require.NoError(t, os.Chmod(p, mode))
}

func mountService(ms ...schema.Mount) schema.Service {
	return schema.Service{Name: "db", Kind: schema.ServiceWorker, Mounts: ms}
}

func TestReadMounts_CarriesAFileWithItsMode(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeMountFile(t, root, "config/start.sh", "#!/bin/sh\n", 0o755)

	got, err := readMounts(root, mountService(schema.Mount{Path: "config/start.sh", At: "/start.sh"}))
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "/start.sh", got[0].At)
	require.Empty(t, got[0].Volume)
	require.Len(t, got[0].Files, 1)
	require.Empty(t, got[0].Files[0].Rel, "a single file mount placed its file under a name rather than AT the target")
	require.Equal(t, "#!/bin/sh\n", string(got[0].Files[0].Data))
	require.Equal(t, int64(0o755), got[0].Files[0].Mode, "an executable lost its mode on the way in")
}

// Supabase's db reads seven files out of /docker-entrypoint-initdb.d, and one of
// them is in a subdirectory.
func TestReadMounts_CarriesADirectoryInAStableOrder(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeMountFile(t, root, "volumes/db/roles.sql", "create role anon;", 0o644)
	writeMountFile(t, root, "volumes/db/init/data.sql", "select 1;", 0o644)
	writeMountFile(t, root, "volumes/db/jwt.sql", "select 2;", 0o644)

	got, err := readMounts(root, mountService(schema.Mount{Path: "volumes/db", At: "/docker-entrypoint-initdb.d"}))
	require.NoError(t, err)
	var rels []string
	for _, f := range got[0].Files {
		rels = append(rels, f.Rel)
	}
	require.Equal(t, []string{"init/data.sql", "jwt.sql", "roles.sql"}, rels)
}

func TestReadMounts_CarriesAVolumeAsANameAndNoContents(t *testing.T) {
	t.Parallel()
	got, err := readMounts(t.TempDir(), mountService(schema.Mount{Volume: "pgdata", At: "/var/lib/postgresql/data"}))
	require.NoError(t, err)
	require.Equal(t, provider.MountSpec{At: "/var/lib/postgresql/data", Volume: "pgdata"}, got[0])
}

// ORDERING: the source is missing when the environment comes up.
func TestReadMounts_RefusesASourceThatIsMissingRatherThanMountingNothing(t *testing.T) {
	t.Parallel()
	_, err := readMounts(t.TempDir(), mountService(schema.Mount{Path: "config/keeper.xml", At: "/etc/k.xml"}))
	require.Error(t, err)
	require.Contains(t, err.Error(), "AF-RUN-048")
	require.Contains(t, err.Error(), "config/keeper.xml")
}

// ORDERING: the source appears after the environment came up, and is edited
// after that. A mount is a snapshot taken once, so the first read refuses, the
// next read carries the file, and an edit after a read does not reach what was
// read. The environment is a function of the tree as it was when it started.
func TestReadMounts_IsASnapshotTakenOnceNotALiveView(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	svc := mountService(schema.Mount{Path: "config/app.conf", At: "/etc/app.conf"})

	_, err := readMounts(root, svc)
	require.Error(t, err, "a missing source produced a mount")

	writeMountFile(t, root, "config/app.conf", "first", 0o644)
	got, err := readMounts(root, svc)
	require.NoError(t, err)

	writeMountFile(t, root, "config/app.conf", "second", 0o644)
	require.Equal(t, "first", string(got[0].Files[0].Data),
		"an edit after the read reached the spec, so the environment would depend on when it was looked at")
}

func TestReadMounts_RefusesALinkThatLeavesTheRepository(t *testing.T) {
	t.Parallel()
	root, outside := t.TempDir(), t.TempDir()
	writeMountFile(t, outside, "id_rsa", "private", 0o600)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "config"), 0o755))
	require.NoError(t, os.Symlink(filepath.Join(outside, "id_rsa"), filepath.Join(root, "config", "key")))

	_, err := readMounts(root, mountService(schema.Mount{Path: "config/key", At: "/key"}))
	require.Error(t, err)
	require.Contains(t, err.Error(), "leads outside the repository")
}

// The same escape one level down, inside a directory mount, which is where it
// would actually hide: nobody reads every entry of a directory they mount.
func TestReadMounts_RefusesALinkOutOfTheRepositoryInsideADirectory(t *testing.T) {
	t.Parallel()
	root, outside := t.TempDir(), t.TempDir()
	writeMountFile(t, outside, "id_rsa", "private", 0o600)
	writeMountFile(t, root, "init/01.sql", "select 1;", 0o644)
	require.NoError(t, os.Symlink(filepath.Join(outside, "id_rsa"), filepath.Join(root, "init", "02.sql")))

	_, err := readMounts(root, mountService(schema.Mount{Path: "init", At: "/init"}))
	require.Error(t, err)
	require.Contains(t, err.Error(), "leads outside the repository")
}

func TestReadMounts_FollowsALinkThatStaysInside(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeMountFile(t, root, "config/real.xml", "<x/>", 0o644)
	require.NoError(t, os.Symlink("real.xml", filepath.Join(root, "config", "keeper.xml")))

	got, err := readMounts(root, mountService(schema.Mount{Path: "config/keeper.xml", At: "/k.xml"}))
	require.NoError(t, err)
	require.Equal(t, "<x/>", string(got[0].Files[0].Data))
}

func TestReadMounts_RefusesAnEmptyDirectory(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "init"), 0o755))
	_, err := readMounts(root, mountService(schema.Mount{Path: "init", At: "/init"}))
	require.Error(t, err)
	require.Contains(t, err.Error(), "no files in it")
}

func TestReadMounts_RefusesAFileLargerThanAMountMayCarry(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	p := filepath.Join(root, "big.bin")
	f, err := os.Create(p)
	require.NoError(t, err)
	require.NoError(t, f.Truncate(schema.MaxMountBytes+1))
	require.NoError(t, f.Close())

	_, err = readMounts(root, mountService(schema.Mount{Path: "big.bin", At: "/big.bin"}))
	require.Error(t, err)
	require.Contains(t, err.Error(), "a mount may carry")
}

func TestServiceSpec_CarriesTheHealthCommandAndTheMounts(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeMountFile(t, root, "config/keeper.xml", "<keeper/>", 0o644)
	spec, err := serviceSpec(schema.Service{
		Name: "keeper", Kind: schema.ServiceWorker, HealthCommand: "clickhouse-keeper-client -q ruok",
		Mounts: []schema.Mount{{Path: "config/keeper.xml", At: "/etc/k.xml"}, {Volume: "coord", At: "/var/lib/k"}},
	}, "img", root)
	require.NoError(t, err)
	require.Equal(t, "clickhouse-keeper-client -q ruok", spec.HealthCommand)
	require.Len(t, spec.Mounts, 2)
	require.Equal(t, "<keeper/>", string(spec.Mounts[0].Files[0].Data))
	require.Equal(t, "coord", spec.Mounts[1].Volume)
}

// THE GATE THIS BOUNDARY NEVER HAD. The comment above serviceSpec records
// health_timeout being validated, defaulted, printed and reported and never
// assigned into the spec, and says a new field forgotten here "fails one of
// these". It did not: a test per field only exists for the fields somebody
// remembered. This one enumerates schema.Service itself, so a field added to it
// with no row below fails here whether or not anybody remembered.
//
// Each row says either how the field reaches the runtime or why it does not.
func TestServiceSpec_EveryServiceFieldIsCarriedOrSaysWhyNot(t *testing.T) {
	t.Parallel()
	rows := map[string]string{
		"Name":          "carried",
		"Kind":          "carried",
		"Command":       "carried",
		"Port":          "carried",
		"HealthPath":    "carried",
		"HealthTimeout": "carried",
		"HealthCommand": "carried",
		"Env":           "carried",
		"Replicas":      "carried",
		"Resources":     "carried, as CPUMillis and MemoryBytes",
		"DependsOn":     "carried",
		"Mounts":        "carried, as contents read at this boundary",
		"Path":          "consumed by the build, which is what turns it into Image",
		"Build":         "consumed by the build, which is what turns it into Image",
		"Migrate":       "set by the caller, because buildPreviousRelease must not run it",
		"Schedule":      "NOT carried: no runtime receives a schedule; recorded as a finding, not a decision",
	}
	var fields []string
	typ := reflect.TypeOf(schema.Service{})
	for i := 0; i < typ.NumField(); i++ {
		fields = append(fields, typ.Field(i).Name)
	}
	var named []string
	for k := range rows {
		named = append(named, k)
	}
	sort.Strings(fields)
	sort.Strings(named)
	require.Equal(t, fields, named,
		"schema.Service and this table disagree. A field added to the manifest needs a row here "+
			"saying how serviceSpec carries it to the runtime, or why it does not")
}
