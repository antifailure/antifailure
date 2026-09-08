package emulator

// GCP is not one emulator, and that is the first thing this file records.
//
// AWS is one LocalStack container answering nine services on one gateway port.
// Google ships nothing of that shape. The official emulators are five separate
// programs, started one per process by the gcloud CLI or by their own image,
// and Cloud Storage has no official emulator at all. So this file registers
// SIX emulators rather than one, and the per environment cost of a GCP
// manifest is the sum of the containers it asks for rather than a single
// number. That is the unflattering half of this lane and it is measured in
// docs/src/content/docs/guides/gcp.md rather than estimated here.
//
// The second thing it records is who ships each one. Five of the six are
// Google's own. Cloud Storage is answered by fake-gcs-server, which is a
// community project by Francisco Souza and is NOT affiliated with Google, and
// nothing in this repository may present it as though it were. A user who
// discovers that from a failing test has been misled by us.

// The names an egress rule names these emulators by.
//
// One constant per emulator rather than a slice, because these are the words
// that appear in a user's manifest and a typo in one of them has to fail to
// compile here rather than fail to match at af up.
const (
	GCSName       = "gcs"
	PubSubName    = "pubsub"
	FirestoreName = "firestore"
	DatastoreName = "datastore"
	BigtableName  = "bigtable"
	SpannerName   = "spanner"
)

// The images, pinned by digest.
//
// gcloudImage is the Google Cloud CLI image in its -emulators flavour, which
// is the plain CLI plus a JRE and the emulator bundles. FOUR of the six
// emulators are this one image started with a different command, so the layers
// are pulled once and the per environment cost is four JVMs rather than four
// downloads.
//
// Two of the three digests are multi architecture indexes, so an arm64 laptop
// and an amd64 runner resolve the same declaration to their own image.
// spannerImage is NOT. Google publishes the Cloud Spanner emulator as a single
// linux/amd64 image with no arm64 member, so on Apple Silicon it runs under
// emulation. That is recorded here rather than found later, because it is the
// one entry whose start time and memory will not resemble what a reader
// measures on a Linux runner.
const (
	gcsImage = "fsouza/fake-gcs-server@sha256:" +
		"797ce226d62f947c009dc40246b30cfb456b8473d8241407f9d6f2c04e4d69ef"

	gcloudImage = "gcr.io/google.com/cloudsdktool/google-cloud-cli@sha256:" +
		"07e4b8c3075ca793552fcfaf4808f104ef155d7805d87ade8e01b440463be262"

	spannerImage = "gcr.io/cloud-spanner-emulator/emulator@sha256:" +
		"4987860c9f8ecf1fffbbcdac115cb88cb9d1a42bd966c235a9ab843aea34fbd1"
)

// The ports each emulator listens on inside its container.
const (
	// GCSPort is fake-gcs-server's default. It is served as plain HTTP here
	// rather than as the TLS the image defaults to: the sidecar terminates
	// TLS with the environment's own authority and forwards inside the
	// network, so a second certificate on the emulator would be one the
	// sidecar then has to be told to distrust.
	GCSPort = 4443
	// PubSubPort and the three below are the ports this file starts the
	// gcloud emulators on. Each is chosen rather than defaulted, because
	// every one of these emulators has to be given --host-port explicitly
	// anyway: their defaults bind localhost, and the address the sidecar
	// forwards to is the container's address on the environment's inner
	// network. They differ from each other for no reason stronger than a
	// person reading four containers of one image in a log being able to
	// tell them apart; four containers have four addresses and the ports
	// could all be equal without anything breaking.
	PubSubPort    = 8085
	FirestorePort = 8086
	DatastorePort = 8087
	BigtablePort  = 8088
	// SpannerGRPCPort is the Spanner emulator's gRPC port. Its REST port is
	// 9020 and is not what a client library uses.
	SpannerGRPCPort = 9010
)

func init() {
	register(gcs)
	register(pubsub)
	register(firestore)
	register(datastore)
	register(bigtable)
	register(spanner)
}

// gcs is Cloud Storage, answered by fake-gcs-server.
//
// THIS IS THE ONE GOOGLE DOES NOT SHIP. Google publishes emulators for
// Pub/Sub, Firestore, Datastore, Bigtable and Spanner, and none for Cloud
// Storage, and the gap is old enough that the community filled it. Saying so
// is not a disclaimer at the bottom of a page: it decides what a reader
// should trust a passing test to mean, so it is the first sentence of the
// guide and it is the reason Official is false here.
//
// The Host header is what makes this work with no endpoint override, and it
// is why the sidecar preserves that header rather than rewriting it.
// fake-gcs-server routes on Host and its public host defaults to exactly
// storage.googleapis.com, so an unmodified client addressing the real service
// is addressing the emulator correctly. A sidecar that rewrote Host to the
// container's alias would miss every route in the emulator and answer 404 to
// a request that was correct.
var gcs = &Emulator{
	EmulatorName: GCSName,
	Vendor:       "Google Cloud",
	Project:      "fake-gcs-server",
	ProjectURL:   "https://github.com/fsouza/fake-gcs-server",
	Official:     false,
	Image:        gcsImage,
	Port:         GCSPort,
	// No Command. The image's entrypoint is /bin/fake-gcs-server, so the
	// emulator is what a bare container runs, and everything below is set
	// through its FAKE_GCS_ environment variables instead.
	Env: map[string]string{
		// Plain HTTP inside the environment. The sidecar is the thing holding
		// a certificate the application trusts.
		"FAKE_GCS_SCHEME": "http",
		// The host the emulator believes it is. It is the default, written
		// out because the whole no endpoint override claim rests on it and a
		// default that changes upstream would change what this build does
		// with nothing in this repository changing.
		"FAKE_GCS_PUBLIC_HOST": "storage.googleapis.com",
		// In memory. Nothing an environment does to the emulator survives the
		// environment, because a twin that inherited the last twin's buckets
		// would be reproducible only by accident.
		"FAKE_GCS_BACKEND": "memory",
	},
	Services: []Service{
		{
			Name: "Cloud Storage",
			// Two spellings, which is path style and virtual hosted style.
			// A bucket may contain dots, so the leading star has to cover
			// more than one label, which is the same rule S3 needs.
			Hosts:  []string{"storage.googleapis.com", "*.storage.googleapis.com"},
			Proves: "create a bucket, upload an object, download it and list the bucket",
			Note: "The JSON API and the XML API in both addressing styles. The regional and " +
				"dual region endpoints under storage.<location>.rep.googleapis.com are outside " +
				"this, because the emulator answers for exactly one public host and a request " +
				"to a second spelling would be routed here and then refused by the emulator " +
				"itself, which reads as a broken twin rather than as an unsupported endpoint.",
		},
	},
	Outside: []Service{
		{
			Name: "The Cloud Storage regional and dual region endpoints",
			Note: "storage.<location>.rep.googleapis.com is a second spelling of the same API " +
				"and the emulator matches on the Host header against one public host. Routing " +
				"it here would produce a 404 from inside the emulated surface, which is a " +
				"worse failure than a refusal because it looks like the object is missing.",
		},
		{
			Name: "www.googleapis.com, which also serves the Cloud Storage JSON API",
			Note: "That host serves dozens of Google APIs and is not Cloud Storage's. Routing " +
				"it to a storage emulator would answer for every other Google API on it with " +
				"a storage 404. An emulator answering the wrong service is the failure this " +
				"whole surface exists to avoid.",
		},
	},
	Licence: Licence{
		Name:   "BSD 2-Clause License",
		Holder: "Copyright (c) Francisco Souza. Not affiliated with Google.",
		URL:    "https://github.com/fsouza/fake-gcs-server/blob/main/LICENSE",
	},
}

// pubsub, firestore, datastore and bigtable are the gcloud CLI emulators.
//
// One image, four containers, four commands. Google ships them inside the
// CLI rather than as service images, so the container's command is what
// decides which emulator it is, and --host-port binds it to every interface
// because the address the sidecar forwards to is the container's address on
// the environment's inner network and not localhost.
var pubsub = &Emulator{
	EmulatorName: PubSubName,
	Vendor:       "Google Cloud",
	Project:      "Google Cloud CLI emulators",
	ProjectURL:   "https://cloud.google.com/pubsub/docs/emulator",
	Official:     true,
	Image:        gcloudImage,
	Port:         PubSubPort,
	Env:          map[string]string{"CLOUDSDK_CORE_DISABLE_PROMPTS": "1"},
	// The image is the CLI, so the command is what decides which emulator
	// this container is. 0.0.0.0 rather than the default localhost bind,
	// because the address the sidecar forwards to is the container's
	// address on the inner network.
	Command: []string{
		"gcloud", "beta", "emulators", "pubsub", "start",
		"--host-port=0.0.0.0:8085", "--project=af-environment",
	},
	Services: []Service{
		{
			Name:   "Cloud Pub/Sub",
			Hosts:  []string{"pubsub.googleapis.com"},
			Proves: "create a topic and a subscription, publish a message and pull it",
			Note: "The regional endpoints under <location>-pubsub.googleapis.com are outside " +
				"this. The emulator has no notion of a region, so answering for a regional " +
				"spelling would emulate a property it does not have.",
		},
	},
	Outside: []Service{{
		Name: "Pub/Sub Lite and the Pub/Sub regional endpoints",
		Note: "Pub/Sub Lite is a different service with a different API and no emulator at " +
			"all. It is refused rather than answered by the Pub/Sub emulator.",
	}},
	Licence: gcloudLicence,
}

var firestore = &Emulator{
	EmulatorName: FirestoreName,
	Vendor:       "Google Cloud",
	Project:      "Google Cloud CLI emulators",
	ProjectURL:   "https://cloud.google.com/firestore/docs/emulator",
	Official:     true,
	Image:        gcloudImage,
	Port:         FirestorePort,
	Env:          map[string]string{"CLOUDSDK_CORE_DISABLE_PROMPTS": "1"},
	// The image is the CLI, so the command is what decides which emulator
	// this container is. 0.0.0.0 rather than the default localhost bind,
	// because the address the sidecar forwards to is the container's
	// address on the inner network.
	Command: []string{
		"gcloud", "emulators", "firestore", "start",
		"--host-port=0.0.0.0:8086",
	},
	Services: []Service{
		{
			Name:   "Cloud Firestore",
			Hosts:  []string{"firestore.googleapis.com"},
			Proves: "write a document, read it back and run a query with a filter",
			Note: "Security rules, indexes and the Firebase console are not part of this. The " +
				"Firebase Local Emulator Suite carries those and is a different program.",
		},
	},
	Outside: []Service{{
		Name: "Firebase Authentication, Realtime Database, Cloud Functions and Hosting",
		Note: "These are the Firebase Local Emulator Suite, not the gcloud Firestore " +
			"emulator. Naming them here would claim a surface this container does not have.",
	}},
	Licence: gcloudLicence,
}

var datastore = &Emulator{
	EmulatorName: DatastoreName,
	Vendor:       "Google Cloud",
	Project:      "Google Cloud CLI emulators",
	ProjectURL:   "https://cloud.google.com/datastore/docs/tools/datastore-emulator",
	Official:     true,
	Image:        gcloudImage,
	Port:         DatastorePort,
	Env:          map[string]string{"CLOUDSDK_CORE_DISABLE_PROMPTS": "1"},
	// The image is the CLI, so the command is what decides which emulator
	// this container is. 0.0.0.0 rather than the default localhost bind,
	// because the address the sidecar forwards to is the container's
	// address on the inner network.
	Command: []string{
		"gcloud", "beta", "emulators", "datastore", "start",
		"--host-port=0.0.0.0:8087", "--project=af-environment",
	},
	Services: []Service{
		{
			Name:   "Cloud Datastore",
			Hosts:  []string{"datastore.googleapis.com"},
			Proves: "put an entity, get it by key and run a query",
			Note: "Google's own page for this emulator opens with \"This content applies to " +
				"the emulator for legacy Cloud Datastore\". A database created today is " +
				"Firestore in Datastore mode and is served by the Firestore emulator with " +
				"--database-mode=datastore-mode, which is a different container. This one is " +
				"here for a database that predates that, and a project on the current product " +
				"should be reaching for the Firestore emulator instead.",
		},
	},
	Outside: []Service{{
		Name: "Datastore in Firestore mode",
		Note: "A Firestore in Datastore mode database is served by Firestore and is emulated " +
			"by the Firestore emulator, not this one. They are two containers on purpose.",
	}},
	Licence: gcloudLicence,
}

var bigtable = &Emulator{
	EmulatorName: BigtableName,
	Vendor:       "Google Cloud",
	Project:      "Google Cloud CLI emulators",
	ProjectURL:   "https://cloud.google.com/bigtable/docs/emulator",
	Official:     true,
	Image:        gcloudImage,
	Port:         BigtablePort,
	Env:          map[string]string{"CLOUDSDK_CORE_DISABLE_PROMPTS": "1"},
	// The image is the CLI, so the command is what decides which emulator
	// this container is. 0.0.0.0 rather than the default localhost bind,
	// because the address the sidecar forwards to is the container's
	// address on the inner network.
	Command: []string{
		"gcloud", "beta", "emulators", "bigtable", "start",
		"--host-port=0.0.0.0:8088",
	},
	Services: []Service{
		{
			Name: "Cloud Bigtable",
			// Both hosts, because creating a table is an admin call and a
			// surface that answered for the data plane alone would fail at
			// the first setup step with an error naming the wrong thing.
			// This is the STS lesson from the AWS surface in Google's shape.
			Hosts:  []string{"bigtable.googleapis.com", "bigtableadmin.googleapis.com"},
			Proves: "create a table and a column family, write a row and read it back",
			Note: "The emulator holds one unnamed instance in memory. Replication, app " +
				"profiles and cluster management are not emulated and the admin API answers " +
				"for table level calls only.",
		},
	},
	Outside: []Service{{
		Name: "Bigtable instance and cluster administration",
		Note: "The emulator has no instances to administer. A call that creates a cluster " +
			"would be answered by something that has no clusters, which is a wrong answer " +
			"rather than a missing one.",
	}},
	Licence: gcloudLicence,
}

// spanner is Google's own emulator image, which is not the CLI.
var spanner = &Emulator{
	EmulatorName: SpannerName,
	Vendor:       "Google Cloud",
	Project:      "Cloud Spanner Emulator",
	ProjectURL:   "https://github.com/GoogleCloudPlatform/cloud-spanner-emulator",
	Official:     true,
	Image:        spannerImage,
	Port:         SpannerGRPCPort,
	// No Command. The image already runs ./gateway_main --hostname 0.0.0.0,
	// read from its published config rather than assumed, so it binds every
	// interface without being told to.
	Services: []Service{
		{
			Name:   "Cloud Spanner",
			Hosts:  []string{"spanner.googleapis.com"},
			Proves: "create an instance and a database, insert a row and read it back",
			Note: "Google documents that the emulator does not check whether a statement is " +
				"partitionable, so a partitioned DML statement can pass here and fail in " +
				"production. It also has no query plans, no ANALYZE, no audit logging and no " +
				"monitoring. A twin is not a substitute for a plan review on Spanner.",
		},
	},
	Outside: []Service{{
		Name: "Spanner backups, instance configuration and the query plan surface",
		Note: "Not implemented by the emulator. Google says so in its own documentation and " +
			"repeating it here is cheaper for the reader than finding it in a failure.",
	}},
	Licence: Licence{
		Name:   "Apache License 2.0",
		Holder: "Copyright Google LLC",
		URL:    "https://github.com/GoogleCloudPlatform/cloud-spanner-emulator/blob/master/LICENSE",
	},
}

// gcloudLicence is the Google Cloud CLI's licence, read out of the image.
//
// CHECKED RATHER THAN ASSUMED, and the assumption was wrong. This file first
// recorded the CLI as proprietary and used rather than redistributed, on the
// reasoning that a vendor tool with a terms of service page is not open
// source. /google-cloud-sdk/LICENSE inside the image itself says otherwise in
// its first sentence: "The Google Cloud CLI and its source code are licensed
// under Apache License v. 2.0 (the License), unless otherwise specified by an
// alternate license file." A compliance document is the wrong place to be
// confidently wrong in either direction, so this one is quoted from the image
// the engine starts rather than from a docs page about installing it.
//
// The second clause of that licence is why Note exists at all: using the CLI
// against a Google Cloud product is additionally governed by that product's
// own terms. Nothing here reaches a Google Cloud product, because the
// emulator sits on the environment's inner network and has no route out, but
// the sentence belongs beside the grant rather than left out of it.
var gcloudLicence = Licence{
	Name: "Apache License 2.0",
	Holder: "Copyright Google LLC. /google-cloud-sdk/LICENSE inside the image is the " +
		"grant, and it adds that use against a Google Cloud product is additionally " +
		"governed by that product's own terms.",
	URL: "https://www.apache.org/licenses/LICENSE-2.0",
}
