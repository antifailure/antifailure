package secrets

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The Key Vault adapter against a real Key Vault.
//
// Everything else about Azure in this package is exercised against a local
// server speaking the documented wire format, which proves what the adapter
// does with each response and proves nothing about whether Azure accepts the
// request. Those are different claims and only this file makes the second one.
//
// To run it, provision the vault and the two service principals with
// testdata/azure-live/setup.sh, which prints the one variable this needs:
//
//	./testdata/azure-live/setup.sh
//	AF_AZURE_LIVE_DIR=~/.af-secrets-live go test ./ee/engine/secrets/ -run Azure_Live -v
//
// One variable naming a directory rather than four naming credentials, because
// a client secret passed on a command line is in the shell's history and in the
// process table for as long as the test runs. The directory is mode 700 outside
// any repository and setup.sh writes each file 600.

// azureLive is the credential material setup.sh leaves behind.
type azureLive struct {
	vaultURL     string
	presentValue string

	tenant       string
	clientID     string
	clientSecret string

	deniedTenant       string
	deniedClientID     string
	deniedClientSecret string
}

// loadAzureLive reads the credentials, or skips.
//
// It skips for exactly one reason, that the directory is not there, which is a
// fact about the machine rather than about the code. A directory that IS there
// and is missing a file is a FAILURE, because that is a half-finished setup and
// reporting it as "not configured" would hide it. This is the same rule the
// Vault harness learned the hard way: twelve behaviours once reported SKIP
// against a container that had started and answered nothing, and the package
// reported ok.
func loadAzureLive(t *testing.T) azureLive {
	t.Helper()

	dir := os.Getenv("AF_AZURE_LIVE_DIR")
	if dir == "" {
		t.Skip("skipped: AF_AZURE_LIVE_DIR is unset, and proving this needs a real Key Vault. " +
			"testdata/azure-live/setup.sh provisions one and prints the variable")
	}
	if expanded, err := os.UserHomeDir(); err == nil && len(dir) > 1 && dir[:2] == "~/" {
		dir = filepath.Join(expanded, dir[2:])
	}
	if _, err := os.Stat(dir); err != nil {
		t.Skip("skipped: AF_AZURE_LIVE_DIR names a directory that is not there: " + err.Error())
	}

	read := func(name string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(dir, name))
		require.NoErrorf(t, err, "%s is set and %s is missing, which is a half-finished "+
			"setup rather than a reason to skip: re-run testdata/azure-live/setup.sh", "AF_AZURE_LIVE_DIR", name)
		return string(b)
	}
	// readLine is for the metadata files, which a shell writes with a trailing
	// newline. The secret's value is read with read() and never trimmed: a
	// value that differs from what the vault holds by one byte is exactly what
	// this suite is here to notice, and trimming it would hide that.
	readLine := func(name string) string {
		t.Helper()
		return strings.TrimSpace(read(name))
	}
	sp := func(name string) (tenant, client, secret string) {
		t.Helper()
		var doc struct {
			AppID    string `json:"appId"`
			Password string `json:"password"`
			Tenant   string `json:"tenant"`
		}
		require.NoError(t, json.Unmarshal([]byte(read(name)), &doc), name+" is not the JSON az writes")
		require.NotEmpty(t, doc.AppID, name+" has no appId")
		require.NotEmpty(t, doc.Password, name+" has no password")
		require.NotEmpty(t, doc.Tenant, name+" has no tenant")
		return doc.Tenant, doc.AppID, doc.Password
	}

	live := azureLive{
		vaultURL:     "https://" + readLine("vault-name") + ".vault.azure.net",
		presentValue: read("azure-present-value"),
	}
	live.tenant, live.clientID, live.clientSecret = sp("azure-sp.json")
	live.deniedTenant, live.deniedClientID, live.deniedClientSecret = sp("azure-sp-denied.json")
	return live
}

func TestAzure_Live_Conformance(t *testing.T) {
	live := loadAzureLive(t)

	working, err := NewAzureKeyVault(AzureConfig{
		VaultURL:     live.vaultURL,
		TenantID:     live.tenant,
		ClientID:     live.clientID,
		ClientSecret: live.clientSecret,
		Getenv:       func(string) string { return "" },
	})
	require.NoError(t, err)

	// A principal Microsoft Entra is happy to authenticate and that Key Vault
	// will not let read anything.
	//
	// Reader on the resource group, which is a control plane role and grants
	// nothing on the data plane, so the token is acquired and renewed exactly
	// as the working one is and the read comes back 403. That is the shape of a
	// credential whose permissions changed underneath a running process, which
	// is the case the one-refresh rule exists for. A wrong client secret would
	// have been easier to arrange and would have tested something else: it
	// fails at the token, so the refresh fails too, and the suite could not
	// tell "renewed and still refused" from "could not renew".
	deniedSource, err := NewAzureKeyVault(AzureConfig{
		VaultURL:     live.vaultURL,
		TenantID:     live.deniedTenant,
		ClientID:     live.deniedClientID,
		ClientSecret: live.deniedClientSecret,
		Getenv:       func(string) string { return "" },
	})
	require.NoError(t, err)
	rejecting := &countingAzure{AzureBackend: deniedSource.backend.(*AzureBackend)}
	require.NoError(t, rejecting.Reach(t.Context()),
		"the denied principal must still authenticate, or this tests the wrong failure")

	unreachable, err := NewAzureKeyVault(AzureConfig{
		// A port nothing listens on, on a host that resolves immediately, so
		// the behaviour fails fast rather than waiting out a DNS timeout.
		VaultURL:     "https://127.0.0.1:1",
		TenantID:     live.tenant,
		ClientID:     live.clientID,
		ClientSecret: live.clientSecret,
		Getenv:       func(string) string { return "" },
	})
	require.NoError(t, err)

	result := Run(t.Context(), t, Harness{
		Name:         "Azure Key Vault",
		Working:      working,
		Present:      "AF_LIVE_TOKEN",
		PresentValue: live.presentValue,
		// Key Vault does store an empty secret value. The az CLI refuses to
		// send one, which is a property of the CLI and not of the service, and
		// taking the CLI's word for it would have skipped this behaviour
		// against a store that supports it. Confirmed with a PUT of {"value":""}
		// to the data plane, which answers 200 and reads back empty.
		Empty:       "AF_LIVE_EMPTY",
		Absent:      "AF_LIVE_MISSING",
		Rejecting:   New(rejecting),
		Refreshes:   rejecting.count,
		Unreachable: unreachable,
	})
	require.Empty(t, result.Failed)
	t.Logf("passed %d behaviours, skipped %d", len(result.Passed), len(result.Skipped))
	for name, why := range result.Skipped {
		t.Logf("skipped %s: %s", name, why)
	}
	// Key Vault can be put into every state the suite asks about, so a skip
	// here means the harness stopped supplying one rather than that the store
	// cannot do it.
	require.Empty(t, result.Skipped)
}
