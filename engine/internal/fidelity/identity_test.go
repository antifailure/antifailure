package fidelity_test

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/fidelity"
	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The manifest that has to keep producing the report it produced before the
// datastores list existed.
//
// A manifest declaring only database: is what every manifest in this
// repository and every manifest anybody has written looks like, and the
// datastores list normalizes it into an entry named primary. That
// normalization is invisible or it is a breaking change, and the difference
// between the two is not a judgement somebody makes by reading a diff. It is
// these bytes.
const databaseOnlyManifest = `
version: 1
name: shop
services:
  - name: web
    port: 3000
  - name: worker
    kind: worker
database:
  provider: docker
  version: 17
egress:
  default: block
  rules:
    - host: api.stripe.com
      mode: mock
    - host: api.resend.com
      mode: capture
personas:
  - name: buyer
    email: buyer@example.test
`

var updateIdentity = flag.Bool("update-identity", false,
	"rewrite the recorded report rather than comparing against it")

// observationFor is the observation the identity check renders, with every
// input fixed so that the only thing that can change the bytes is the code.
func observationFor(t *testing.T, body string) fidelity.Observation {
	t.Helper()
	m, err := manifest.Parse([]byte(body), "antifailure.yaml", "")
	require.NoError(t, err)

	obs := full()
	obs.Manifest = m
	return obs
}

// The acceptance evidence for the datastores contract, and the reason it is a
// recorded file rather than an assertion about a substring.
//
// An assertion that the report "still mentions the database" passes a report
// that gained a whole dimension, lost a component, or renamed one. Byte for
// byte is the only comparison that cannot be satisfied by something close
// enough, and closeness is exactly what a normalization step produces when it
// is wrong.
func TestAManifestWithOnlyADatabaseProducesTheReportItAlwaysDid(t *testing.T) {
	t.Parallel()
	got := fidelity.Build(observationFor(t, databaseOnlyManifest)).Explain()

	path := filepath.Join("testdata", "database-only-report.txt")
	if *updateIdentity {
		require.NoError(t, os.WriteFile(path, []byte(got), 0o600))
		return
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err, "the recorded report is missing, so nothing is being compared")
	require.Equal(t, string(want), got,
		"the fidelity report for a manifest that declares only database: changed")
}

// Fixing the inputs is not enough on its own: the observation carries a
// provider type, and a change there would move the bytes for a reason that has
// nothing to do with this lane. This states what the recorded report was made
// from.
func TestTheRecordedReportIsMadeFromTheDeclaredObservation(t *testing.T) {
	t.Parallel()
	obs := observationFor(t, databaseOnlyManifest)
	require.Equal(t, "shop", obs.Manifest.Name)
	require.Equal(t, schema.DBDocker, obs.Manifest.Database.Provider)
	require.Len(t, obs.Running, 2)
	require.IsType(t, provider.RunningService{}, obs.Running[0])
}
