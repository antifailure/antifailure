// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package secrets

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The Secrets Manager adapter against a real Secrets Manager.
//
// Everything else about AWS in this package is exercised against a local server
// speaking the documented wire format, which proves what the adapter does with
// each response and proves nothing about whether AWS accepts the request. Those
// are different claims and only this file makes the second one.
//
// THIS SUITE HAS NEVER RUN. There is no AWS account on this machine at all, so
// testdata/aws-live/setup.sh has never been executed either. What is here is a
// harness that runs the day an account exists, written while the reasoning
// behind it was in front of somebody, rather than under time pressure on the
// day the account arrives. It is not evidence about the adapter and STATUS.md
// does not claim it is.
//
// To run it, provision the secret and the two principals with
// testdata/aws-live/setup.sh, which prints the one variable this needs:
//
//	./testdata/aws-live/setup.sh
//	AF_AWS_LIVE_DIR=~/.af-secrets-live-aws go test ./ee/engine/secrets/ -run AWS_Live -v
//
// One variable naming a directory rather than several naming credentials,
// because a secret access key passed on a command line is in the shell's
// history and in the process table for as long as the test runs. The directory
// is mode 700 outside any repository and setup.sh writes each file 600. The
// keys reach the adapter through an injected Getenv rather than through the
// process environment, so they are never inherited by anything this test
// starts.

// awsLive is the credential material setup.sh leaves behind.
type awsLive struct {
	region       string
	presentValue string

	keyID, secretKey             string
	deniedKeyID, deniedSecretKey string

	// emptySupported records whether Secrets Manager accepted a secret with an
	// empty string value, as measured by setup.sh at the moment it tried.
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

// loadAWSLive reads the credentials, or skips.
//
// It skips for exactly one reason, that the directory is not there, which is a
// fact about the machine rather than about the code. A directory that IS there
// and is missing a file is a FAILURE, because that is a half-finished setup and
// reporting it as "not configured" would hide it. This is the same rule the
// Vault harness learned the hard way: twelve behaviours once reported SKIP
// against a container that had started and answered nothing, and the package
// reported ok.
func loadAWSLive(t *testing.T) awsLive {
	t.Helper()

	dir := os.Getenv("AF_AWS_LIVE_DIR")
	if dir == "" {
		t.Skip("skipped: AF_AWS_LIVE_DIR is unset, and proving this needs a real Secrets Manager " +
			"account. testdata/aws-live/setup.sh provisions one and prints the variable")
	}
	if expanded, err := os.UserHomeDir(); err == nil && len(dir) > 1 && dir[:2] == "~/" {
		dir = filepath.Join(expanded, dir[2:])
	}
	if _, err := os.Stat(dir); err != nil {
		t.Skip("skipped: AF_AWS_LIVE_DIR names a directory that is not there: " + err.Error())
	}

	read := func(name string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(dir, name))
		require.NoErrorf(t, err, "%s is set and %s is missing, which is a half-finished "+
			"setup rather than a reason to skip: re-run testdata/aws-live/setup.sh", "AF_AWS_LIVE_DIR", name)
		return string(b)
	}
	// readLine is for the metadata files, which a shell writes with a trailing
	// newline. The secret's value is read with read() and never trimmed: a
	// value that differs from what the store holds by one byte is exactly what
	// this suite is here to notice, and trimming it would hide that.
	readLine := func(name string) string {
		t.Helper()
		return strings.TrimSpace(read(name))
	}
	// keys reads the JSON the aws CLI writes for a new access key.
	keys := func(name string) (id, secret string) {
		t.Helper()
		var doc struct {
			AccessKey struct {
				AccessKeyID     string `json:"AccessKeyId"`
				SecretAccessKey string `json:"SecretAccessKey"`
			} `json:"AccessKey"`
		}
		require.NoError(t, json.Unmarshal([]byte(read(name)), &doc), name+" is not the JSON aws writes")
		require.NotEmpty(t, doc.AccessKey.AccessKeyID, name+" has no AccessKeyId")
		require.NotEmpty(t, doc.AccessKey.SecretAccessKey, name+" has no SecretAccessKey")
		return doc.AccessKey.AccessKeyID, doc.AccessKey.SecretAccessKey
	}

	live := awsLive{
		region:       readLine("region"),
		presentValue: read("aws-present-value"),
	}
	live.keyID, live.secretKey = keys("aws-keys.json")
	live.deniedKeyID, live.deniedSecretKey = keys("aws-keys-denied.json")
	switch answer := readLine("empty-supported"); answer {
	case "yes":
		live.emptySupported = true
	case "no":
		live.emptySupported = false
	default:
		require.Failf(t, "empty-supported is unreadable",
			"it holds %q and the only answers are yes and no; re-run testdata/aws-live/setup.sh", answer)
	}
	require.NotEmpty(t, live.region, "region is empty")
	return live
}

// env hands the adapter one principal's keys without touching the process
// environment, so two principals can be used in one process and neither is
// inherited by anything this test starts.
func awsEnv(id, secret string) func(string) string {
	return func(name string) string {
		switch name {
		case "AWS_ACCESS_KEY_ID":
			return id
		case "AWS_SECRET_ACCESS_KEY":
			return secret
		}
		return ""
	}
}

func TestAWS_Live_Conformance(t *testing.T) {
	live := loadAWSLive(t)

	// Through Getenv rather than through AWSConfig.Credentials, because
	// credentials supplied directly are declared unrenewable and the refresh
	// behaviour would then skip. The refresh rule is one of the two things this
	// suite is really here for.
	working, err := NewAWSSecretsManager(AWSConfig{
		Region: live.region, Getenv: awsEnv(live.keyID, live.secretKey),
	})
	require.NoError(t, err)

	// A principal AWS is happy to authenticate and that Secrets Manager will
	// not let read anything.
	//
	// A real IAM user with real keys and no secretsmanager permission, so the
	// signature verifies exactly as the working one does and the call comes
	// back AccessDeniedException. That is the shape of a credential whose
	// permissions changed underneath a running process, which is the case the
	// one-refresh rule exists for. A wrong secret key would have been easier to
	// arrange and would have tested something else: it fails signature
	// verification, which the adapter reads as a different exception, and the
	// suite could not tell "renewed and still refused" from "never valid".
	deniedSource, err := NewAWSSecretsManager(AWSConfig{
		Region: live.region, Getenv: awsEnv(live.deniedKeyID, live.deniedSecretKey),
	})
	require.NoError(t, err)
	rejecting := &countingAWS{AWSBackend: deniedSource.backend.(*AWSBackend)}
	require.NoError(t, rejecting.Reach(t.Context()),
		"the denied principal must still reach the store, or this tests the wrong failure")

	// THE ARM THAT FOUND THE FAULT ON AZURE, and this store had it worse.
	//
	// Azure at least proved that Microsoft Entra answered. On this path the
	// keys resolve out of an injected environment with no network call at all,
	// so before this pull request Reach could not fail for ANY unreachable
	// store: a VPC endpoint pointed at the wrong place, a region not enabled on
	// the account, or a typo in Endpoint all reported the source usable.
	unreachable, err := NewAWSSecretsManager(AWSConfig{
		Region: live.region, Getenv: awsEnv(live.keyID, live.secretKey),
		// A port nothing listens on, on a host that resolves immediately, so
		// the behaviour fails fast rather than waiting out a DNS timeout.
		Endpoint: "https://127.0.0.1:1/",
	})
	require.NoError(t, err)

	empty := "AF_LIVE_EMPTY"
	if !live.emptySupported {
		empty = ""
	}

	result := Run(t.Context(), t, Harness{
		Name:         "AWS Secrets Manager",
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
		"setup.sh recorded that Secrets Manager refused an empty value, so exactly that one "+
			"behaviour may skip and nothing else may")
}
