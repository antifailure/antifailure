// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The Secret Manager adapter against a real Secret Manager.
//
// Everything else about Google in this package is exercised against a local
// server speaking the documented wire format, which proves what the adapter
// does with each response and proves nothing about whether Google accepts the
// request. Those are different claims and only this file makes the second one.
//
// THIS SUITE HAS NEVER RUN. There is no billing account on this machine that
// Secret Manager will enable against, and testdata/gcp-live/setup.sh stops at
// that step and says so. What is here is a harness that runs the day there is
// one, written while the reasoning behind it was in front of somebody, rather
// than under time pressure on the day the account arrives. It is not evidence
// about the adapter and STATUS.md does not claim it is.
//
// To run it, provision the project and the two service accounts with
// testdata/gcp-live/setup.sh, which prints the one variable this needs:
//
//	./testdata/gcp-live/setup.sh
//	AF_GCP_LIVE_DIR=~/.af-secrets-live-gcp go test ./ee/engine/secrets/ -run GCP_Live -v
//
// One variable naming a directory rather than several naming credentials,
// because a service account key passed on a command line is in the shell's
// history and in the process table for as long as the test runs. The directory
// is mode 700 outside any repository and setup.sh writes each file 600.

// gcpLive is the credential material setup.sh leaves behind.
type gcpLive struct {
	project      string
	presentValue string

	key       []byte
	deniedKey []byte

	// emptySupported records whether Secret Manager accepted a version with an
	// empty payload, as measured by setup.sh at the moment it tried.
	//
	// A marker file rather than a constant because nobody here knows the
	// answer. Key Vault's empty secret was settled by sending one and watching
	// the service answer 200, and that is the only kind of evidence worth
	// having; asserting either way about a service this has never spoken to
	// would be a guess wearing a test's clothes. The script writes what
	// happened and this suite asserts the consequence exactly, so the behaviour
	// is either run or skipped-and-named, never silently passed.
	emptySupported bool
}

// loadGCPLive reads the credentials, or skips.
//
// It skips for exactly one reason, that the directory is not there, which is a
// fact about the machine rather than about the code. A directory that IS there
// and is missing a file is a FAILURE, because that is a half-finished setup and
// reporting it as "not configured" would hide it. This is the same rule the
// Vault harness learned the hard way: twelve behaviours once reported SKIP
// against a container that had started and answered nothing, and the package
// reported ok.
func loadGCPLive(t *testing.T) gcpLive {
	t.Helper()

	dir := os.Getenv("AF_GCP_LIVE_DIR")
	if dir == "" {
		t.Skip("skipped: AF_GCP_LIVE_DIR is unset, and proving this needs a real Secret Manager " +
			"project. testdata/gcp-live/setup.sh provisions one and prints the variable")
	}
	if expanded, err := os.UserHomeDir(); err == nil && len(dir) > 1 && dir[:2] == "~/" {
		dir = filepath.Join(expanded, dir[2:])
	}
	if _, err := os.Stat(dir); err != nil {
		t.Skip("skipped: AF_GCP_LIVE_DIR names a directory that is not there: " + err.Error())
	}

	read := func(name string) []byte {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(dir, name))
		require.NoErrorf(t, err, "%s is set and %s is missing, which is a half-finished "+
			"setup rather than a reason to skip: re-run testdata/gcp-live/setup.sh", "AF_GCP_LIVE_DIR", name)
		return b
	}
	// readLine is for the metadata files, which a shell writes with a trailing
	// newline. The secret's value is read with read() and never trimmed: a
	// value that differs from what the store holds by one byte is exactly what
	// this suite is here to notice, and trimming it would hide that.
	readLine := func(name string) string {
		t.Helper()
		return strings.TrimSpace(string(read(name)))
	}

	live := gcpLive{
		project:      readLine("project"),
		presentValue: string(read("gcp-present-value")),
		key:          read("gcp-sa.json"),
		deniedKey:    read("gcp-sa-denied.json"),
	}
	switch answer := readLine("empty-supported"); answer {
	case "yes":
		live.emptySupported = true
	case "no":
		live.emptySupported = false
	default:
		require.Failf(t, "empty-supported is unreadable",
			"it holds %q and the only answers are yes and no; re-run testdata/gcp-live/setup.sh", answer)
	}
	require.NotEmpty(t, live.project, "project is empty")
	return live
}

func TestGCP_Live_Conformance(t *testing.T) {
	live := loadGCPLive(t)
	none := func(string) string { return "" }

	working, err := NewGCPSecretManager(GCPConfig{
		Project: live.project, CredentialsJSON: live.key, Getenv: none,
	})
	require.NoError(t, err)

	// A service account Google is happy to authenticate and that Secret Manager
	// will not let read anything.
	//
	// No role at all on the project, so the token is minted and renewed exactly
	// as the working one is and the access comes back 403 PERMISSION_DENIED.
	// That is the shape of a credential whose permissions changed underneath a
	// running process, which is the case the one-refresh rule exists for. A
	// corrupt key would have been easier to arrange and would have tested
	// something else: it fails at the assertion, so the refresh fails too, and
	// the suite could not tell "renewed and still refused" from "could not
	// renew".
	deniedSource, err := NewGCPSecretManager(GCPConfig{
		Project: live.project, CredentialsJSON: live.deniedKey, Getenv: none,
	})
	require.NoError(t, err)
	rejecting := &countingGCP{GCPBackend: deniedSource.backend.(*GCPBackend)}
	require.NoError(t, rejecting.Reach(t.Context()),
		"the denied service account must still authenticate, or this tests the wrong failure")

	// THE ARM THAT FOUND THE FAULT ON AZURE, and it can only find it here.
	//
	// A real token endpoint and a store address nothing answers on. Offline,
	// both halves live in one fake process and a dead address breaks them
	// together, so the token failure arrives first and hides the fact that
	// nothing ever asked the store anything. Here Google mints a perfectly good
	// token and the store is not there, which is what a project with the API
	// not enabled, a VPC Service Controls perimeter, or a typo in the project
	// id all look like. Before this pull request Reach returned nil for it.
	unreachable, err := NewGCPSecretManager(GCPConfig{
		Project: live.project, CredentialsJSON: live.key,
		// A port nothing listens on, on a host that resolves immediately, so
		// the behaviour fails fast rather than waiting out a DNS timeout.
		Endpoint: "https://127.0.0.1:1", Getenv: none,
	})
	require.NoError(t, err)

	empty := "AF_LIVE_EMPTY"
	if !live.emptySupported {
		empty = ""
	}

	result := Run(t.Context(), t, Harness{
		Name:         "Google Secret Manager",
		Working:      working,
		Present:      "AF_LIVE_TOKEN",
		PresentValue: live.presentValue,
		Empty:        empty,
		Absent:       "AF_LIVE_MISSING",
		Rejecting:    New(rejecting),
		Refreshes:    rejecting.count,
		Unreachable:  unreachable,
	})
	require.Empty(t, result.Failed)
	t.Logf("passed %d behaviours, skipped %d", len(result.Passed), len(result.Skipped))
	for name, why := range result.Skipped {
		t.Logf("skipped %s: %s", name, why)
	}

	// The skip set is asserted exactly rather than merely bounded. An empty
	// value is the one state nobody here has been able to put this store into,
	// so it is the one skip that may exist, it carries the reason the suite
	// itself supplies, and any other skip means the harness stopped supplying a
	// state rather than that the store cannot reach one.
	if live.emptySupported {
		require.Empty(t, result.Skipped)
		return
	}
	require.Equal(t, map[string]string{
		"reports a value it holds as empty as present": "this store cannot hold an empty value",
	}, result.Skipped,
		"setup.sh recorded that Secret Manager refused an empty payload, so exactly that one "+
			"behaviour may skip and nothing else may")
}
