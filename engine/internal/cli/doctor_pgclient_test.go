package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/cli"
)

// af doctor said this machine could run Antifailure without looking at the two
// programs that copy production.
//
// Its own promise is that every problem it names is one you would otherwise
// meet halfway through a run, and this was the clearest example it did not
// look at. Copying a source shells out to pg_dump and pg_restore. A machine
// with neither, or with a client older than the server, fails at the step
// after everything else: the repository read, the manifest written, the images
// built, and only then does the copy stop. pg_dump refuses a server newer than
// itself outright, with no flag to get past it.
func TestDoctor_ReportsThePostgresClientAndItsCeiling(t *testing.T) {
	t.Parallel()
	got := runCLI(t, t.TempDir(), nil, "doctor", "-o", "json")

	var report cli.DoctorReport
	require.NoError(t, json.Unmarshal([]byte(got.stdout), &report))

	var found *cli.CheckResult
	for i := range report.Checks {
		if report.Checks[i].Name == "Postgres client" {
			found = &report.Checks[i]
		}
	}
	require.NotNil(t, found,
		"doctor answers for the machine and says nothing about what copies production")
	require.NotEmpty(t, found.Detail)
	require.NotEmpty(t, found.Remediation)

	// The ceiling rather than the version alone. "pg_dump 17" is a fact about
	// a binary; "up to Postgres 17" is what decides whether a refresh works,
	// and it is the sentence somebody whose production is newer has to read.
	if found.Status == cli.CheckPass {
		require.Contains(t, strings.ToLower(found.Detail), "up to postgres",
			"a version with no ceiling leaves the reader to know pg_dump's rule themselves")
	}

	// Never a hard failure. A project that fills its golden from database.seed
	// runs neither program, so a machine without them can still do everything
	// else, and this check has no manifest to tell the two apart.
	require.NotEqual(t, cli.CheckFail, found.Status,
		"a machine that copies no production database is not a broken machine")
}
