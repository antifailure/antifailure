package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// af env prune exists for environments nobody tore down, and until this check
// the only thing that named it was af env list, which is itself a command
// nobody is pointed at. Doctor is where somebody finds out, so what it decides
// from a set of ages is worth pinning.
//
// Ages come from a fixed now rather than a real clock, because the case that
// matters is a day old and waiting for one is not a test.
func TestLeftoverVerdict(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) environment { return environment{Oldest: now.Add(-d)} }

	t.Run("nothing held says so", func(t *testing.T) {
		status, detail := leftoverVerdict(nil, now)
		require.Equal(t, CheckPass, status)
		require.Contains(t, detail, "none")
	})

	t.Run("an environment somebody is working in is not a warning", func(t *testing.T) {
		// The normal state for anybody actually using the product. Warning here
		// would make the check noise, and a noisy check is one people stop
		// reading, including on the day it is right.
		status, detail := leftoverVerdict([]environment{ago(time.Minute), ago(3 * time.Hour)}, now)
		require.Equal(t, CheckPass, status)
		require.Contains(t, detail, "2 environments")
	})

	t.Run("the boundary belongs to the side that does not warn", func(t *testing.T) {
		// Exactly the cutoff is not yet stale, which is what af env prune does
		// with the same number: it removes what is older than the cutoff.
		status, _ := leftoverVerdict([]environment{ago(pruneCutoff)}, now)
		require.Equal(t, CheckPass, status)
		status, _ = leftoverVerdict([]environment{ago(pruneCutoff + time.Second)}, now)
		require.Equal(t, CheckWarn, status)
	})

	t.Run("stale ones are counted and the oldest is named", func(t *testing.T) {
		// The shape found on the machine this was written on: four over a day,
		// one under, and two live.
		envs := []environment{
			ago(41 * time.Hour), ago(41 * time.Hour), ago(35 * time.Hour), ago(26 * time.Hour),
			ago(13 * time.Hour), ago(6 * time.Minute), ago(time.Second),
		}
		status, detail := leftoverVerdict(envs, now)
		require.Equal(t, CheckWarn, status)
		require.Contains(t, detail, "4 environments older than a day")
		require.Contains(t, detail, "out of 7")
		require.Contains(t, detail, "41h")
	})
}

// Every check has to carry a remediation, including the ones that pass, or a
// user is told something is wrong and left there. The doctor suite asserts this
// across the whole report; this asserts it for the path that skips, which is
// the one a report on a machine with no daemon would otherwise reach empty.
func TestLeftoverCheck_CarriesARemediationWhenItCannotLook(t *testing.T) {
	dir := t.TempDir()
	e := &Env{WorkDir: dir, Getenv: func(string) string { return "" }, Clock: clock.New()}
	r := checkLeftoverEnvironments(context.Background(), e, systemProber{getenv: e.Getenv})
	require.Equal(t, "Leftover environments", r.Name)
	require.NotEmpty(t, r.Detail)
	require.Contains(t, r.Remediation, "af env prune")
}
