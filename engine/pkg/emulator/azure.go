package emulator

import "github.com/antifailure/antifailure/engine/pkg/extension"

// The Azure surface, and why it is five registrations rather than one.
//
// Azurite is ONE IMAGE THAT LISTENS ON THREE PORTS, blob 10000, queue 10001
// and table 10002, and extension.EmulatorContainer carries one port. So blob,
// queue and table are registered separately, same image and same environment,
// one port each. That is not a workaround dressed up as a design. An
// environment starts the emulators its egress rules name, so a manifest that
// touches blob only starts one container and pays for one, and the refusal
// outside the surface is written per service rather than per cloud: a request
// to Azure Files is refused with Files named, not with "Azure" named.
//
// Service Bus and Cosmos DB are NOT registered, and the reasons are measured
// rather than tasteful. They are named in Outside below and the numbers are in
// docs/guides/azure.md. A service named in the surface and not answered would
// be worse than one absent, so neither appears in Services until it can be
// started, and Service Bus cannot be declared at all yet: it needs a second
// container beside it and EmulatorContainer carries one image.

// The names an egress rule names these by.
//
// Hyphens rather than underscores, because the alias the sidecar reaches an
// emulator by is af-emu-<name> and that has to be a legal DNS label. An
// underscore is legal in a Docker network alias and illegal in a hostname, so
// a name carrying one resolves through some resolvers and not others, which is
// worse than either resolving or failing everywhere.
const (
	AzureBlobName  = "azure-blob"
	AzureQueueName = "azure-queue"
	AzureTableName = "azure-table"
)

// azuriteImage is Azurite, pinned by the digest of its multi architecture
// index, so an arm64 laptop and an amd64 runner each resolve the one
// declaration to their own image and neither is pinned to the other's
// architecture.
const azuriteImage = "mcr.microsoft.com/azure-storage/azurite@sha256:" +
	"830430c1da1a2d537e08f3e6764dd1f5ae00cf0346bcaf625b968ec3f0971fd5"

// The ports Azurite's own default command binds, which is why there are three
// registrations rather than one.
const (
	AzureBlobPort  = 10000
	AzureQueuePort = 10001
	AzureTablePort = 10002
)

func init() {
	register(azureBlob)
	register(azureQueue)
	register(azureTable)
}

// azuriteLicence is Azurite's, and it is the only one of the three Azure
// emulators that is open source. That difference is the reason Service Bus and
// Cosmos are opt in as much as their weight is.
var azuriteLicence = Licence{
	Name:   "MIT License",
	Holder: "Copyright (c) Microsoft Corporation",
	URL:    "https://github.com/Azure/Azurite/blob/main/LICENSE",
}

// azuriteEnv is what every Azurite container is started with.
//
// It is deliberately almost empty. Azurite serves the account devstoreaccount1
// with the well known development key, and it reads the account name out of
// the HOST HEADER when the host is a name rather than an address, which is
// what production style addressing means and is why the sidecar preserving
// Host is load bearing here as much as it is for a virtual hosted S3 bucket.
// So an application handed a substituted connection string naming
// devstoreaccount1 builds the URL https://devstoreaccount1.blob.core.windows.net
// itself, the name resolves to the sidecar, and nothing in the application
// knows an emulator exists.
//
// AZURITE_ACCOUNTS is not set here on purpose. It would name accounts, and the
// account an environment needs is the one the manifest's substituted
// credential carries, which this build cannot know. An application that reads
// its account name from configuration needs nothing; one that hard codes a
// production account name is the documented limit.
func azuriteEnv() map[string]string {
	return map[string]string{
		// The log is read by a person diagnosing a failed twin, so it carries
		// the request line and not a debug trace.
		"AZURITE_LOOSE": "false",
	}
}

var azureBlob = &Emulator{
	EmulatorName: AzureBlobName,
	Vendor:       "Azure",
	Project:      "Azurite",
	ProjectURL:   "https://github.com/Azure/Azurite",
	// Azure publishes Azurite, so the vendor whose API is emulated.
	Maintainer: extension.MaintainerVendor,
	Image:      azuriteImage,
	Port:       AzureBlobPort,
	Env:        azuriteEnv(),
	Services: []Service{
		{
			Name: "Azure Blob Storage",
			// One spelling, because Azure puts the account in the first label
			// and there is no path style alternative to cover. The secondary
			// read endpoint account-secondary.blob.core.windows.net is one
			// label too and is matched by the same pattern, which is correct:
			// an emulator has one copy and answers a secondary read from it.
			Hosts:  []string{"*.blob.core.windows.net"},
			Proves: "CreateContainer, UploadBlob, DownloadBlob and ListBlobs",
			Note: "Shared Key and SAS are the credential shapes. Entra ID tokens are not, " +
				"because Azurite's OAuth mode requires it to terminate TLS itself and in an " +
				"environment the sidecar terminates it.",
		},
	},
	Outside: []Service{
		{
			Name: "Azure Data Lake Storage Gen2",
			Note: "The hierarchical namespace answers on dfs.core.windows.net, which is a " +
				"different host and is not routed here. Azurite implements the blob " +
				"endpoint of a storage account and not the DFS one, so a request that " +
				"reached it would get a plausible answer to the wrong question.",
		},
		{
			Name: "Azure Files",
			Note: "file.core.windows.net is a different service on a different host and " +
				"Azurite does not implement it.",
		},
		{
			Name: "The sovereign cloud suffixes, core.chinacloudapi.cn and core.usgovcloudapi.net",
			Note: "A different suffix is a different host and is not routed here. It falls " +
				"through to the egress policy, whose default is block, so it is refused " +
				"rather than answered.",
		},
	},
	Licence: azuriteLicence,
}

var azureQueue = &Emulator{
	EmulatorName: AzureQueueName,
	Vendor:       "Azure",
	Project:      "Azurite",
	ProjectURL:   "https://github.com/Azure/Azurite",
	// Azure publishes Azurite, so the vendor whose API is emulated.
	Maintainer: extension.MaintainerVendor,
	Image:      azuriteImage,
	Port:       AzureQueuePort,
	Env:        azuriteEnv(),
	Services: []Service{
		{
			Name:   "Azure Queue Storage",
			Hosts:  []string{"*.queue.core.windows.net"},
			Proves: "CreateQueue, SendMessage, ReceiveMessages and DeleteMessage",
		},
	},
	Outside: []Service{
		{
			Name: "Azure Service Bus",
			Note: "A Service Bus queue is not a Queue Storage queue and is not answered " +
				"here. It answers on servicebus.windows.net, Microsoft's emulator for it " +
				"needs a SQL Server container beside it, and this build cannot declare a " +
				"second container yet. The measured cost is in the Azure guide.",
		},
	},
	Licence: azuriteLicence,
}

var azureTable = &Emulator{
	EmulatorName: AzureTableName,
	Vendor:       "Azure",
	Project:      "Azurite",
	ProjectURL:   "https://github.com/Azure/Azurite",
	// Azure publishes Azurite, so the vendor whose API is emulated.
	Maintainer: extension.MaintainerVendor,
	Image:      azuriteImage,
	Port:       AzureTablePort,
	Env:        azuriteEnv(),
	Services: []Service{
		{
			Name:   "Azure Table Storage",
			Hosts:  []string{"*.table.core.windows.net"},
			Proves: "CreateTable, CreateEntity, GetEntity and ListEntities",
		},
	},
	Outside: []Service{
		{
			Name: "The Cosmos DB Table API",
			Note: "It speaks the Table protocol and answers on table.cosmos.azure.com, " +
				"which is a different host and is not routed here. Azurite is Table " +
				"Storage, so a Cosmos account's throughput and partitioning behaviour is " +
				"not what would be exercised.",
		},
	},
	Licence: azuriteLicence,
}
