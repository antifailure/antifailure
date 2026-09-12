package manifest_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/manifest"
)

// services[].mounts and services[].health_command, the two keys that decide
// whether a published stack can be written down at all and whether a twin of it
// can say no.
//
// Every refusal here is one that would otherwise START A CONTAINER IN A STATE
// ITS AUTHOR DID NOT WRITE, and each test names the one it holds. The
// containment refusals are at the bottom and are the ones to read twice.

const withMounts = `
version: 1
name: shop
services:
  - name: web
    port: 3000
    mounts:
`

func TestParse_AcceptsAPathMountAndAVolumeMount(t *testing.T) {
	t.Parallel()
	m := mustParse(t, withMounts+
		"      - path: config/keeper.xml\n        at: /etc/clickhouse-keeper/keeper_config.xml\n"+
		"      - volume: data\n        at: /var/lib/clickhouse\n")
	require.Len(t, m.Services[0].Mounts, 2)
	require.False(t, m.Services[0].Mounts[0].IsVolume())
	require.Equal(t, "config/keeper.xml", m.Services[0].Mounts[0].Path)
	require.True(t, m.Services[0].Mounts[1].IsVolume())
	require.Equal(t, "data", m.Services[0].Mounts[1].Volume)
}

// PostHog's web, plugins, livestream, feature-flags and cymbal share
// directories, so one volume on several services is the shape the key exists
// for rather than a mistake.
func TestParse_AcceptsOneVolumeSharedBetweenServices(t *testing.T) {
	t.Parallel()
	m := mustParse(t, `
version: 1
name: shop
services:
  - name: web
    port: 3000
    mounts:
      - volume: shared
        at: /code/share
  - name: plugins
    kind: worker
    mounts:
      - volume: shared
        at: /code/share
`)
	require.Equal(t, m.Services[0].Mounts[0].Volume, m.Services[1].Mounts[0].Volume)
}

func TestParse_RefusesAMountThatNamesNeitherAPathNorAVolume(t *testing.T) {
	t.Parallel()
	msg := messages(problems(t, mustFail(t, withMounts+"      - at: /etc/x\n")))
	require.Contains(t, msg, "names neither a path nor a volume")
}

func TestParse_RefusesAMountThatNamesBothAPathAndAVolume(t *testing.T) {
	t.Parallel()
	msg := messages(problems(t, mustFail(t, withMounts+
		"      - path: config/x.xml\n        volume: data\n        at: /etc/x\n")))
	require.Contains(t, msg, "names both a path and a volume")
}

func TestParse_RefusesAMountWithNoTarget(t *testing.T) {
	t.Parallel()
	msg := messages(problems(t, mustFail(t, withMounts+"      - path: config/x.xml\n")))
	require.Contains(t, msg, "names no place to put it")
}

func TestParse_RefusesARelativeMountTarget(t *testing.T) {
	t.Parallel()
	msg := messages(problems(t, mustFail(t, withMounts+
		"      - path: config/x.xml\n        at: etc/x.xml\n")))
	require.Contains(t, msg, "is not an absolute path")
}

// /etc/../root and /root are one place and only one of them reads like it.
func TestParse_RefusesAMountTargetThatIsNotInItsSimplestForm(t *testing.T) {
	t.Parallel()
	msg := messages(problems(t, mustFail(t, withMounts+
		"      - path: config/x.xml\n        at: /etc/../root/x.xml\n")))
	require.Contains(t, msg, "is not in its simplest form")
	require.Contains(t, msg, "/root/x.xml")
}

func TestParse_RefusesAMountOverTheContainersRoot(t *testing.T) {
	t.Parallel()
	msg := messages(problems(t, mustFail(t, withMounts+"      - volume: data\n        at: /\n")))
	require.Contains(t, msg, "asks for the container's root")
}

func TestParse_RefusesTwoMountsAtOnePlace(t *testing.T) {
	t.Parallel()
	msg := messages(problems(t, mustFail(t, withMounts+
		"      - path: config/a.xml\n        at: /etc/x.xml\n"+
		"      - path: config/b.xml\n        at: /etc/x.xml\n")))
	require.Contains(t, msg, `both land at "/etc/x.xml"`)
}

func TestParse_RefusesOneVolumeTwiceInOneService(t *testing.T) {
	t.Parallel()
	msg := messages(problems(t, mustFail(t, withMounts+
		"      - volume: data\n        at: /a\n"+
		"      - volume: data\n        at: /b\n")))
	require.Contains(t, msg, `mounts the volume "data" twice`)
}

func TestParse_RefusesTheRepositoryRootAsAMountSource(t *testing.T) {
	t.Parallel()
	msg := messages(problems(t, mustFail(t, withMounts+"      - path: .\n        at: /app\n")))
	require.Contains(t, msg, "names the repository root as its source")
}

// A health command is the check for a service that has one, and the web
// default of "/" must not be filled in beside it: if it were, every service that
// wrote a command would read as having written both and be refused below.
func TestParse_AcceptsAHealthCommandAndLeavesTheHealthPathUnset(t *testing.T) {
	t.Parallel()
	m := mustParse(t, minimal+"    health_command: pg_isready -U postgres\n")
	require.Equal(t, "pg_isready -U postgres", m.Services[0].HealthCommand)
	require.Empty(t, m.Services[0].HealthPath,
		"the normalizer filled a health path in beside a declared command")
}

func TestParse_AcceptsAHealthCommandOnAWorker(t *testing.T) {
	t.Parallel()
	m := mustParse(t, `
version: 1
name: shop
services:
  - name: web
    port: 3000
  - name: consumer
    kind: worker
    health_command: test -f /tmp/ready
`)
	require.Equal(t, "test -f /tmp/ready", m.Services[1].HealthCommand)
}

func TestParse_RefusesAHealthPathAndAHealthCommandTogether(t *testing.T) {
	t.Parallel()
	msg := messages(problems(t, mustFail(t, minimal+
		"    health_path: /healthz\n    health_command: pg_isready\n")))
	require.Contains(t, msg, "declares both a health path and a health command")
}

func TestParse_RefusesAHealthCommandOnACronService(t *testing.T) {
	t.Parallel()
	msg := messages(problems(t, mustFail(t, minimal+`  - name: nightly
    kind: cron
    schedule: "0 3 * * *"
    command: ./report
    health_command: "true"
`)))
	require.Contains(t, msg, "is a cron service and declares a health command")
}

// THE CONTAINMENT REFUSALS.
//
// A mount reads the repository and nothing else on the machine. There is no key
// that asks for a host path: the compose spelling of a bind is refused as an
// unknown key by the decoder, and the two spellings of a host path that fit the
// keys that do exist are refused by name.

func TestParse_HasNoWayToAskForABindMount(t *testing.T) {
	t.Parallel()
	err := mustFail(t, withMounts+"      - type: bind\n        source: /etc\n        at: /host-etc\n")
	require.Contains(t, err.Error(), "type",
		"a compose style bind was not refused by name, so a manifest may have asked for a host path")
}

func TestParse_RefusesAnAbsoluteHostPathAsAMountSource(t *testing.T) {
	t.Parallel()
	msg := messages(problems(t, mustFail(t, withMounts+"      - path: /etc/passwd\n        at: /x\n")))
	require.Contains(t, msg, "resolves outside the repository")
}

func TestParse_RefusesAMountSourceThatClimbsOutOfTheRepository(t *testing.T) {
	t.Parallel()
	msg := messages(problems(t, mustFail(t, withMounts+"      - path: ../../.ssh/id_rsa\n        at: /x\n")))
	require.Contains(t, msg, "resolves outside the repository")
}

// The one no lexical rule can see. A committed link named config/keeper.xml
// pointing at a file elsewhere on the machine is a clean relative path to a file
// that exists, and the copy would put its target inside a container running the
// code under rehearsal.
func TestParse_RefusesAMountSourceThatLeavesTheRepositoryThroughALink(t *testing.T) {
	t.Parallel()
	root, outside := t.TempDir(), t.TempDir()
	secret := filepath.Join(outside, "id_rsa")
	require.NoError(t, os.WriteFile(secret, []byte("not for a container"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "config"), 0o755))
	require.NoError(t, os.Symlink(secret, filepath.Join(root, "config", "keeper.xml")))

	_, err := manifest.Parse([]byte(withMounts+
		"      - path: config/keeper.xml\n        at: /etc/keeper.xml\n"), "antifailure.yaml", root)
	msg := messages(problems(t, err))
	require.Contains(t, msg, "is a link that leads outside the repository")
}

// The positive control for the test above: a link that stays inside is fine, so
// the refusal is about where the link leads and not about links.
func TestParse_AcceptsAMountSourceThatIsALinkInsideTheRepository(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "config"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "config", "real.xml"), []byte("<x/>"), 0o644))
	require.NoError(t, os.Symlink("real.xml", filepath.Join(root, "config", "keeper.xml")))

	_, err := manifest.Parse([]byte(withMounts+
		"      - path: config/keeper.xml\n        at: /etc/keeper.xml\n"), "antifailure.yaml", root)
	require.NoError(t, err)
}

func TestParse_RefusesAMountSourceThatDoesNotExist(t *testing.T) {
	t.Parallel()
	_, err := manifest.Parse([]byte(withMounts+
		"      - path: config/keeper.xml\n        at: /etc/keeper.xml\n"), "antifailure.yaml", t.TempDir())
	require.Contains(t, messages(problems(t, err)), "does not exist")
}
