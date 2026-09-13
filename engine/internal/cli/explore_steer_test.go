package cli_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// steerableManifest declares two personas and one goal, and nothing that has to
// be running for the steering to be checked.
const steerableManifest = `version: 1
name: steerable
services:
  - name: web
    kind: web
    build:
      strategy: dockerfile
      dockerfile: Dockerfile
    command: node server.js
    port: 3000
egress:
  default: block
personas:
  - name: owner
    email: owner@example.test
  - name: viewer
    email: viewer@example.test
explore:
  enabled: true
  goals:
    - name: billing
      goal: Download the latest invoice.
      persona: owner
`

// A steering flag nobody could run is refused before the manifest is read, so
// the answer to a typo is the sizes and forms that would have worked rather
// than a complaint about a file the caller was not asking about.
func TestExplore_ASteeringFlagThatCannotBeUsedIsRefusedBeforeTheManifest(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"--viewport", "banana"},
		{"--start", "https://evil.example/billing"},
		{"--budget", "0"},
	} {
		dir := t.TempDir()
		r := runCLI(t, dir, nil, append([]string{"explore"}, args...)...)
		require.Equalf(t, 2, r.code, "%v: %s", args, r.stderr)
		require.Containsf(t, r.stderr, "AF-AGT-023", "%v", args)
	}

	// The control: the same empty directory with no steering is refused for
	// the missing manifest, so the refusal above came from the flag.
	r := runCLI(t, t.TempDir(), nil, "explore")
	require.NotZero(t, r.code)
	require.NotContains(t, r.stderr, "AF-AGT-023")
}

// A flag that is parsed, validated and then left out of the options the
// orchestrator receives looks exactly like a working flag in every test that
// only feeds it bad values. So this names a persona the manifest does not
// declare, which only the orchestrator can refuse, and only if the flag got
// there.
func TestExplore_ThePersonaFlagReachesTheOrchestrator(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeManifest(t, dir, steerableManifest)
	// The manifest names a Dockerfile, and a manifest naming one that is not
	// there is refused before anything else is read.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0o600))

	r := runCLI(t, dir, nil, "explore", "--branch", "main", "--persona", "admin")
	require.Equal(t, 2, r.code, r.stderr)
	require.Contains(t, r.stderr, "AF-AGT-022")
	require.Contains(t, prose(r.stderr), "owner, viewer")
}
