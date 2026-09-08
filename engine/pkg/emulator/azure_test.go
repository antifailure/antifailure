package emulator_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/policy"
	"github.com/antifailure/antifailure/engine/pkg/emulator"
	"github.com/antifailure/antifailure/engine/pkg/extension"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// Azure is three registrations of one image, and every property below is one
// somebody downstream depends on.

func mustAzure(t *testing.T, name string) *emulator.Emulator {
	t.Helper()
	e, ok := emulator.Named(name)
	require.True(t, ok, "an egress rule naming %s must find an emulator", name)
	return e
}

// The three names, the three ports, and the one image. A second registration
// that reused the first one's port would answer blob for a queue call, which
// Azurite would answer plausibly rather than refuse, and nothing else in this
// package would notice.
func TestAzure_ThreeRegistrationsOfOneImageOnThreeDistinctPorts(t *testing.T) {
	t.Parallel()

	want := map[string]int{
		emulator.AzureBlobName:  emulator.AzureBlobPort,
		emulator.AzureQueueName: emulator.AzureQueuePort,
		emulator.AzureTableName: emulator.AzureTablePort,
	}

	images := make(map[string]bool)
	ports := make(map[int]string)
	for name, port := range want {
		e := mustAzure(t, name)
		require.Equal(t, name, e.Name())
		require.Contains(t, emulator.Names(), name)

		c := e.Container()
		require.Equal(t, port, c.Port,
			"%s must forward to the port Azurite binds that service on", name)

		prior, taken := ports[c.Port]
		require.False(t, taken,
			"%s and %s both forward to port %d, so one of them answers for the other's "+
				"service and Azurite would answer it plausibly rather than refuse",
			name, prior, c.Port)
		ports[c.Port] = name

		images[c.Image] = true
	}
	require.Len(t, images, 1,
		"the three registrations are one Azurite image, so an environment naming all "+
			"three pays for one image on disk")
}

func TestAzure_ImagesArePinnedByDigestRatherThanATag(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		emulator.AzureBlobName, emulator.AzureQueueName, emulator.AzureTableName,
	} {
		require.Contains(t, mustAzure(t, name).Container().Image, "@sha256:",
			"%s: a tag that moves changes what an environment was tested against "+
				"with nothing in this repository changing", name)
	}
}

// A built in emulator that could not pass the check outside registrations are
// held to would be a double standard the first outside emulator discovers.
func TestAzure_PassesTheRegistryValidationOutsideEmulatorsAreHeldTo(t *testing.T) {
	t.Parallel()
	r := extension.NewRegistry()
	for _, name := range []string{
		emulator.AzureBlobName, emulator.AzureQueueName, emulator.AzureTableName,
	} {
		r.AddEmulator(mustAzure(t, name))
	}
	require.NoError(t, r.Validate(nil))
}

// AZURITE_ACCOUNTS is the one variable that would break the claim this lane
// makes, because naming accounts here would mean the account an environment can
// use is decided by this build rather than by the manifest's substituted
// credential.
func TestAzure_DoesNotPinTheStorageAccountThisBuildCannotKnow(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		emulator.AzureBlobName, emulator.AzureQueueName, emulator.AzureTableName,
	} {
		env := mustAzure(t, name).Container().Env
		require.NotContains(t, env, "AZURITE_ACCOUNTS",
			"%s: naming accounts here decides in this build which storage account an "+
				"environment may serve, and that is the manifest's to decide", name)
	}
}

// Host matching lives in more than one place in this repository and what holds
// those places together is a corpus, not a shared belief. This is that corpus
// for Azure: every host the surface claims is compiled by the REAL policy
// engine, and the endpoint an SDK resolves is decided by the rule the emulator
// would have written.
func TestAzure_HostPatternsDecideTheEndpointsAnSDKResolves(t *testing.T) {
	t.Parallel()

	var rules []schema.EgressRule
	byHost := map[string]*emulator.Emulator{}
	for _, name := range []string{
		emulator.AzureBlobName, emulator.AzureQueueName, emulator.AzureTableName,
	} {
		e := mustAzure(t, name)
		for _, h := range e.Hosts() {
			rules = append(rules, schema.EgressRule{Host: h, Mode: schema.ModeAllow})
			byHost[h] = e
		}
	}
	engine, err := policy.New(&schema.Egress{Default: schema.ModeBlock, Rules: rules})
	require.NoError(t, err, "every host in the surface must compile as a policy rule")

	// The account travels in the first label, so these are the shapes an SDK
	// actually resolves, including the secondary read endpoint, which is one
	// label and is correctly answered from the one copy an emulator holds.
	reached := map[string]string{
		"shopfront.blob.core.windows.net":            emulator.AzureBlobName,
		"shopfront-secondary.blob.core.windows.net":  emulator.AzureBlobName,
		"shopfront.queue.core.windows.net":           emulator.AzureQueueName,
		"shopfront-secondary.queue.core.windows.net": emulator.AzureQueueName,
		"shopfront.table.core.windows.net":           emulator.AzureTableName,
	}
	for host, wantName := range reached {
		req, parseErr := policy.ParseRequest("POST", "https://"+host+"/")
		require.NoError(t, parseErr)
		require.True(t, engine.Evaluate(req).Matched(),
			"%s matched no rule the surface writes", host)

		var claimed []string
		for _, e := range byHost {
			if _, ok := e.ServiceFor(host); ok {
				claimed = append(claimed, e.Name())
			}
		}
		require.Len(t, claimed, 1,
			"%s is claimed by %v, and a host claimed by none is decided by a rule the "+
				"surface table refuses while a host claimed by two answers by rule order",
			host, claimed)
		require.Equal(t, wantName, claimed[0],
			"%s is routed to the wrong emulator, so a queue call would be answered by "+
				"the blob service, plausibly rather than refused", host)
	}

	// Outside the surface. Each of these is a host somebody could reasonably
	// expect to be covered by something that is, which is why they are here
	// rather than a single obviously foreign name.
	refused := []string{
		"shopfront.file.core.windows.net",       // Azure Files
		"shopfront.dfs.core.windows.net",        // Data Lake Storage Gen2
		"shopfront.servicebus.windows.net",      // Service Bus, not Queue Storage
		"shopfront.table.cosmos.azure.com",      // the Cosmos Table API, not Table Storage
		"shopfront.documents.azure.com",         // Cosmos DB
		"shopfront.vault.azure.net",             // Key Vault
		"blob.core.windows.net",                 // the apex, which carries no account
		"shopfront.blob.core.chinacloudapi.cn",  // a sovereign suffix
		"shopfront.blob.core.usgovcloudapi.net", // a sovereign suffix
		"shopfront.blob.core.windows.net.evil.example",
	}
	for _, host := range refused {
		req, parseErr := policy.ParseRequest("POST", "https://"+host+"/")
		require.NoError(t, parseErr)
		require.False(t, engine.Evaluate(req).Matched(),
			"%s is outside the surface and a rule the emulator writes decided it", host)

		for _, e := range byHost {
			_, covered := e.ServiceFor(host)
			require.False(t, covered,
				"%s is outside the surface and %s answered it", host, e.Name())
		}
	}
}
