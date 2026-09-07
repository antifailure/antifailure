package manifest_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/internal/volume"
)

// database.volume, the block that gives every row count in a report a
// denominator.
//
// The three ways it can be written wrong are the three that would otherwise
// produce a silent unknown at the far end of the run: a block with no path, a
// path outside the repository, and an age nothing can parse. Each of those
// would leave the fidelity report saying nothing said what production holds,
// which reads exactly like a manifest that never declared the block at all.

const withVolume = `
version: 1
name: shop
services:
  - name: web
    port: 3000
database:
  provider: docker
  volume:
`

func TestParse_AcceptsAVolumeProfileAndDefaultsItsMaxAge(t *testing.T) {
	t.Parallel()
	m := mustParse(t, withVolume+"    profile: .antifailure/volume.json\n")
	require.NotNil(t, m.Database.Volume)
	require.Equal(t, ".antifailure/volume.json", m.Database.Volume.Profile)
	require.Equal(t, manifest.DefaultVolumeMaxAge, m.Database.Volume.MaxAge)
}

// Thirty days rather than the golden's seven, and the two constants that say
// so live in two packages, so a test holds them to each other. They have
// drifted before: the insights defaults and the manifest's did, and one
// command printed a pair of thresholds another caller never used.
func TestNormalize_TheDefaultVolumeMaxAgeMatchesTheOneVolumeUses(t *testing.T) {
	t.Parallel()
	d, err := manifest.ParseDuration(manifest.DefaultVolumeMaxAge)
	require.NoError(t, err)
	require.Equal(t, volume.DefaultMaxAge, d,
		"the manifest default and the one internal/volume applies disagree, so a project "+
			"that declares no max_age gets one age from the normalizer and another from the reader")
}

func TestParse_RefusesAVolumeBlockWithNoProfile(t *testing.T) {
	t.Parallel()
	msg := messages(problems(t, mustFail(t, withVolume+"    max_age: 720h\n")))
	require.Contains(t, msg, "The volume block names no profile")
	require.Contains(t, msg, "af volume record")
}

func TestParse_RefusesAProfilePathOutsideTheRepository(t *testing.T) {
	t.Parallel()
	msg := messages(problems(t, mustFail(t, withVolume+"    profile: ../../etc/volume.json\n")))
	require.Contains(t, msg, "is not a file inside the repository")
}

// An age nothing can parse is refused rather than silently treated as never
// stale, which is what a zero duration means everywhere else here. A profile
// nothing can expire is the failure the setting exists to prevent.
func TestParse_RefusesAMaxAgeThatIsNotADuration(t *testing.T) {
	t.Parallel()
	msg := messages(problems(t,
		mustFail(t, withVolume+"    profile: .antifailure/volume.json\n    max_age: soon\n")))
	require.Contains(t, msg, `The maximum age "soon" is not a duration`)
	require.Contains(t, msg, "720h or 30d")
}

func TestParse_LeavesAManifestWithNoVolumeBlockAlone(t *testing.T) {
	t.Parallel()
	m := mustParse(t, minimal)
	require.Nil(t, m.Database.Volume,
		"a manifest that says nothing about volume gained a block, which would make every "+
			"existing manifest declare a profile it does not have")
}
