package manifest_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/internal/traffic"
)

// load.traffic, the block that gives every route in a load run a denominator.
//
// The three ways it can be written wrong are the three that would otherwise
// produce a silent unknown at the far end of the run: a block with no path, a
// path outside the repository, and an age nothing can parse. Each would leave
// the fidelity report saying nothing said what production serves, which reads
// exactly like a manifest that never declared the block at all.

const withTraffic = `
version: 1
name: shop
services:
  - name: web
    port: 3000
load:
  enabled: true
  traffic:
`

func TestParse_AcceptsATrafficProfileAndDefaultsItsMaxAge(t *testing.T) {
	t.Parallel()
	m := mustParse(t, withTraffic+"    profile: .antifailure/traffic.json\n")
	require.NotNil(t, m.Load.Traffic)
	require.Equal(t, ".antifailure/traffic.json", m.Load.Traffic.Profile)
	require.Equal(t, manifest.DefaultTrafficMaxAge, m.Load.Traffic.MaxAge)
}

// Fourteen days rather than the volume profile's thirty, and the two constants
// that say so live in two packages, so a test holds them to each other. They
// have drifted before, and a project that declares no max_age would get one
// age from the normalizer and another from the reader.
func TestNormalize_TheDefaultTrafficMaxAgeMatchesTheOneTrafficUses(t *testing.T) {
	t.Parallel()
	d, err := manifest.ParseDuration(manifest.DefaultTrafficMaxAge)
	require.NoError(t, err)
	require.Equal(t, traffic.DefaultMaxAge, d,
		"the manifest default and the one internal/traffic applies disagree")
}

func TestParse_RefusesATrafficBlockWithNoProfile(t *testing.T) {
	t.Parallel()
	msg := messages(problems(t, mustFail(t, withTraffic+"    max_age: 336h\n")))
	require.Contains(t, msg, "The traffic block names no profile")
	require.Contains(t, msg, "af traffic record")
}

func TestParse_RefusesATrafficProfilePathOutsideTheRepository(t *testing.T) {
	t.Parallel()
	msg := messages(problems(t, mustFail(t, withTraffic+"    profile: ../../etc/traffic.json\n")))
	require.Contains(t, msg, "is not a file inside the repository")
}

func TestParse_RefusesATrafficMaxAgeThatIsNotADuration(t *testing.T) {
	t.Parallel()
	msg := messages(problems(t,
		mustFail(t, withTraffic+"    profile: .antifailure/traffic.json\n    max_age: soon\n")))
	require.Contains(t, msg, `The maximum age "soon" is not a duration`)
	require.Contains(t, msg, "336h or 14d")
}

func TestParse_LeavesALoadBlockWithNoTrafficAlone(t *testing.T) {
	t.Parallel()
	m := mustParse(t, `
version: 1
name: shop
services:
  - name: web
    port: 3000
load:
  enabled: true
  safe_routes:
    - GET /health
`)
	require.NotNil(t, m.Load)
	require.Nil(t, m.Load.Traffic,
		"a manifest that says nothing about traffic gained a block, which would make every "+
			"existing manifest declare a profile it does not have")
}

// p95_increase used to be refused outright under every source but otel,
// because a route arrived with no baseline and the threshold could never fire.
// A declared traffic profile is the second place a baseline can come from, so
// the refusal is conditional now. Refusing it there would refuse a comparison
// that works.
func TestParse_P95IncreaseIsAllowedWhenATrafficProfileSuppliesTheBaseline(t *testing.T) {
	t.Parallel()
	const withThreshold = `
version: 1
name: shop
services:
  - name: web
    port: 3000
load:
  enabled: true
  source: none
  thresholds:
    p95_increase: 0.5
`
	msg := messages(problems(t, mustFail(t, withThreshold)))
	require.Contains(t, msg, "The load source is none and p95_increase is set")
	require.Contains(t, msg, "declare load.traffic.profile")

	m := mustParse(t, withThreshold+"  traffic:\n    profile: .antifailure/traffic.json\n")
	require.Equal(t, 0.5, m.Load.Thresholds.P95Increase)
}

// And under access_log, which is the source that carries a mix and no
// durations at all.
func TestParse_P95IncreaseUnderAnAccessLogNeedsAProfile(t *testing.T) {
	t.Parallel()
	const withLog = `
version: 1
name: shop
services:
  - name: web
    port: 3000
load:
  enabled: true
  source: access_log
  source_config:
    path: access.log
  thresholds:
    p95_increase: 0.5
`
	msg := messages(problems(t, mustFail(t, withLog)))
	require.Contains(t, msg, "The load source is access_log and p95_increase is set")

	m := mustParse(t, withLog+"  traffic:\n    profile: .antifailure/traffic.json\n")
	require.Equal(t, 0.5, m.Load.Thresholds.P95Increase)
}
