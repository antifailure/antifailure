package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
)

// The manifest af up needs to get as far as opening a session. Nothing here is
// ever built: the failure under test happens before any runtime is reached.
const minimalManifest = `version: 1
name: standing
services:
  - name: standing
    kind: web
    build:
      strategy: dockerfile
      dockerfile: Dockerfile
    command: node server.js
    port: 3000
database:
  provider: docker
  version: 17
  url_env: DATABASE_URL
egress:
  default: block
`

// af up printed a Go stack trace over an error it had already diagnosed.
//
// Up returns a nil Result whenever it fails inside open, which is every failure
// touching the state directory, the branch lock or the journal, and the failure
// path then read Services off it. The panic replaced the whole message: not
// only the cause and its next step, but the exit code a script reads.
//
// The state directory is blocked by putting a file where the directory goes,
// because that is the cheapest reachable trigger. The branch lock reaches the
// same line, and so does a journal that cannot be opened.
func TestUp_DoesNotPanicWhenItFailsBeforeThereIsAResult(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "antifailure.yaml"),
		[]byte(minimalManifest), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"),
		[]byte("FROM alpine\n"), 0o644))
	// Where .antifailure would be created, so MkdirAll fails.
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".antifailure"),
		[]byte("not a directory"), 0o644))

	var out, errW bytes.Buffer
	code := Execute(context.Background(), []string{"up"}, Options{
		Stdout: &out, Stderr: &errW, Stdin: strings.NewReader(""),
		Getenv: func(string) string { return "" },
		Clock:  clock.New(), WorkDir: dir,
	})

	require.NotZero(t, code, "a failure has to reach the exit code")
	combined := out.String() + errW.String()
	require.Contains(t, combined, "AF-RUN-040",
		"the diagnosed cause has to survive, and it is what the panic used to replace")
	require.NotContains(t, combined, "panic:")
	require.NotContains(t, combined, "goroutine ")
}

// Nothing was created, so nothing may be claimed to be standing. The report is
// read from the inventory precisely so that it cannot overstate, and this is
// the case that would catch a fixed sentence written into an error's next step.
func TestUp_SaysNothingIsStandingWhenNothingWasCreated(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "antifailure.yaml"),
		[]byte(minimalManifest), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"),
		[]byte("FROM alpine\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".antifailure"),
		[]byte("not a directory"), 0o644))

	var out, errW bytes.Buffer
	Execute(context.Background(), []string{"up"}, Options{
		Stdout: &out, Stderr: &errW, Stdin: strings.NewReader(""),
		Getenv: func(string) string { return "" },
		Clock:  clock.New(), WorkDir: dir,
	})

	combined := out.String() + errW.String()
	require.NotContains(t, combined, "still up",
		"a run that created nothing must not tell somebody to tear something down")
}
