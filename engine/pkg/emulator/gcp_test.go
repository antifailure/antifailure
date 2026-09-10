package emulator_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/policy"
	"github.com/antifailure/antifailure/engine/pkg/emulator"
	"github.com/antifailure/antifailure/engine/pkg/extension"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The GCP surface is six emulators rather than one, and every test below is
// about a property somebody downstream depends on: the registry refuses an
// unpinned image, the sidecar routes what Hosts returns, the notices file
// prints what Licence holds, and the guide prints who ships each one.

func TestGCP_RegistersSixEmulatorsRatherThanOne(t *testing.T) {
	t.Parallel()
	// Not a stylistic preference. AWS is one LocalStack container answering
	// nine services on one port; Google ships five separate emulator programs
	// and no storage emulator at all, so a GCP environment starts up to six
	// containers and its cost is their sum. A version of this that registered
	// one emulator would be describing a product Google does not ship.
	for _, name := range emulator.GCPNames() {
		e, ok := emulator.Named(name)
		require.True(t, ok, "an egress rule naming %q must find an emulator", name)
		require.Equal(t, name, e.Name())
		require.Contains(t, emulator.Names(), name)
	}
	require.Len(t, emulator.GCPNames(), 6)
}

func TestGCP_EveryImageIsPinnedByDigestRatherThanByATag(t *testing.T) {
	t.Parallel()
	for _, name := range emulator.GCPNames() {
		e := mustGCP(t, name)
		require.Contains(t, e.Container().Image, "@sha256:",
			"%s is pinned by a tag, and a tag that moves changes what an "+
				"environment was tested against with nothing here changing", name)
		require.Positive(t, e.Container().Port, "%s forwards to no port", name)
	}
}

// The registry validates registrations before an environment is created and
// already refuses a hostless emulator and an unpinned image. Six built in
// emulators that could not pass the check an outside emulator is held to
// would be a double standard the first outside emulator would discover.
func TestGCP_AllSixPassTheRegistryValidationTogether(t *testing.T) {
	t.Parallel()
	r := extension.NewRegistry()
	for _, name := range emulator.GCPNames() {
		r.AddEmulator(mustGCP(t, name))
	}
	require.NoError(t, r.Validate(map[string][]string{}))
	for _, name := range emulator.GCPNames() {
		named, ok := r.EmulatorNamed(name)
		require.True(t, ok, "%s did not survive registration", name)
		require.NotEmpty(t, named.Hosts(), "%s answers for no host", name)
	}
}

func TestGCP_CoversEveryServiceTheLaneOwesAndNothingElse(t *testing.T) {
	t.Parallel()
	owed := []string{
		"Cloud Storage", "Cloud Pub/Sub", "Cloud Firestore",
		"Cloud Datastore", "Cloud Bigtable", "Cloud Spanner",
	}
	have := make(map[string]bool)
	for _, name := range emulator.GCPNames() {
		for _, s := range mustGCP(t, name).Services {
			have[s.Name] = true
		}
	}
	for _, name := range owed {
		require.True(t, have[name], "%s is missing from the emulated surface", name)
	}
	require.Len(t, have, len(owed),
		"a service in the surface that nothing above owes is a claim nobody checks")
}

// The most useful thing this lane publishes. Google ships emulators for five
// of these services and none for Cloud Storage, so one of the six is somebody
// else's software. A table that printed all six identically would present a
// community project as Google's.
func TestGCP_RecordsWhichEmulatorsGoogleActuallyShips(t *testing.T) {
	t.Parallel()
	official := map[string]bool{
		emulator.PubSubName:    true,
		emulator.FirestoreName: true,
		emulator.DatastoreName: true,
		emulator.BigtableName:  true,
		emulator.SpannerName:   true,
		emulator.GCSName:       false,
	}
	for name, want := range official {
		e := mustGCP(t, name)
		require.Equal(t, want, e.Official,
			"%s is recorded as Official=%v and that is not what Google ships", name, e.Official)
	}

	// The one that is not Google's says so in the words a reader sees, in
	// both the project name and the copyright line the notices file prints.
	// A boolean nobody renders is not a disclosure.
	gcs := mustGCP(t, emulator.GCSName)
	require.Equal(t, "fake-gcs-server", gcs.Project)
	require.Contains(t, gcs.Licence.Holder, "Not affiliated with Google")
}

// Every built in emulator, not only this lane's, has to answer the question.
// A cloud added later that left Official at its zero value would be recorded
// as third party, which is the safe direction, and this is what would catch
// an emulator that is Google's or Amazon's and was never marked.
func TestEveryBuiltInEmulatorNamesItsProjectAndItsLicence(t *testing.T) {
	t.Parallel()
	for _, e := range emulator.Builtin() {
		require.NotEmpty(t, e.Project, "%s names no project", e.Name())
		require.True(t, strings.HasPrefix(e.ProjectURL, "https://"),
			"%s has no project URL", e.Name())
		require.NotEmpty(t, e.Licence.Name, "%s records no licence", e.Name())
		require.NotEmpty(t, e.Licence.Holder, "%s records no copyright holder", e.Name())
		require.True(t, strings.HasPrefix(e.Licence.URL, "https://"),
			"%s records no licence URL", e.Name())
	}
	// LocalStack is not Amazon's either, and the field exists so that a
	// reader never has to know which projects are vendor projects.
	aws, ok := emulator.Named("aws")
	require.True(t, ok)
	require.False(t, aws.Official, "LocalStack is not shipped by Amazon")
}

// Each licence is the one the project actually ships under, and this file
// first got one of them wrong in the direction that sounds careful. The
// Google Cloud CLI was recorded as proprietary, on the reasoning that a
// vendor tool with a terms of service page is not open source, and the
// image's own /google-cloud-sdk/LICENSE says Apache 2.0 in its first
// sentence. A compliance document is the wrong place to be confidently wrong
// in either direction, so each one below is the value read from the source
// rather than inferred from the vendor.
func TestGCP_RecordsTheLicenceEachProjectActuallyShipsUnder(t *testing.T) {
	t.Parallel()
	// fake-gcs-server, and the copyright line carries the disclosure a reader
	// of the notices file needs.
	gcs := mustGCP(t, emulator.GCSName).Licence
	require.Equal(t, "BSD 2-Clause License", gcs.Name)
	require.Contains(t, gcs.Holder, "Francisco Souza")

	// The Cloud Spanner emulator, which is Google's own repository.
	require.Equal(t, "Apache License 2.0", mustGCP(t, emulator.SpannerName).Licence.Name)

	// The four gcloud emulators share one image and therefore one licence,
	// and the second clause of that licence is recorded with it.
	for _, name := range []string{
		emulator.PubSubName, emulator.FirestoreName,
		emulator.DatastoreName, emulator.BigtableName,
	} {
		lic := mustGCP(t, name).Licence
		require.Equal(t, "Apache License 2.0", lic.Name,
			"%s records a licence the image does not ship under", name)
		require.Contains(t, lic.Holder, "google-cloud-sdk/LICENSE",
			"%s does not say where in the image the grant was read from", name)
	}
}

// Bigtable is this surface's version of the AWS lane's STS problem: creating a
// table is an admin call on a second hostname, so a surface that answered for
// the data plane alone would fail at the first setup step with an error
// naming the wrong thing.
func TestGCP_AnswersForTheBigtableAdminHostAndNotOnlyTheDataHost(t *testing.T) {
	t.Parallel()
	e := mustGCP(t, emulator.BigtableName)
	for _, host := range []string{"bigtable.googleapis.com", "bigtableadmin.googleapis.com"} {
		svc, ok := e.ServiceFor(host)
		require.True(t, ok, "%s is not routed to the emulator", host)
		require.Equal(t, "Cloud Bigtable", svc.Name)
	}
}

// Virtual hosted addressing is the case that decides whether the sidecar may
// rewrite the Host header, and for Cloud Storage the answer is the same as it
// is for S3 and for a different reason worth having in a test rather than in
// a comment: fake-gcs-server routes on the Host header and its public host is
// storage.googleapis.com, so an unmodified client is already addressing it
// correctly and a rewritten Host would match no route in the emulator.
func TestGCP_AnswersForBothCloudStorageAddressingStyles(t *testing.T) {
	t.Parallel()
	e := mustGCP(t, emulator.GCSName)
	for _, host := range []string{
		"storage.googleapis.com",
		"mybucket.storage.googleapis.com",
		"my.dotted.bucket.storage.googleapis.com",
	} {
		svc, ok := e.ServiceFor(host)
		require.True(t, ok, "%s is not routed to the emulator", host)
		require.Equal(t, "Cloud Storage", svc.Name)
	}
	require.Equal(t, "storage.googleapis.com", e.Container().Env["FAKE_GCS_PUBLIC_HOST"],
		"the emulator has to believe it is the host the application addresses")
	require.Equal(t, "http", e.Container().Env["FAKE_GCS_SCHEME"],
		"the sidecar holds the certificate; a second one on the emulator is one more to trust")
}

// The expensive half of the lane. A silent wrong answer from an emulator is
// worse than a refusal because it will be trusted, so a Google host outside
// the surface must not be routed to any of the six.
func TestGCP_RefusesGoogleHostsOutsideTheSurface(t *testing.T) {
	t.Parallel()
	outside := []string{
		// The credential path. No emulator here implements Google's token
		// endpoint, so these are refused rather than answered. This is the
		// gap the guide states plainly.
		"oauth2.googleapis.com",
		"accounts.google.com",
		"iamcredentials.googleapis.com",
		"sts.googleapis.com",
		// Services with no emulator at all.
		"secretmanager.googleapis.com",
		"cloudtasks.googleapis.com",
		"bigquery.googleapis.com",
		"run.googleapis.com",
		"cloudfunctions.googleapis.com",
		"logging.googleapis.com",
		"compute.googleapis.com",
		// The shared Google API host, which also serves the Cloud Storage
		// JSON API and dozens of others.
		"www.googleapis.com",
		"googleapis.com",
		// A regional Cloud Storage endpoint the emulator would 404.
		"storage.us-central1.rep.googleapis.com",
		// A host that merely ends in something that looks right.
		"storage.googleapis.com.evil.example",
		"notgoogleapis.com",
	}
	for _, name := range emulator.GCPNames() {
		e := mustGCP(t, name)
		for _, host := range outside {
			_, ok := e.ServiceFor(host)
			require.False(t, ok,
				"%s is outside the surface and %s answered it", host, name)
		}
	}
}

// No two emulators may claim the same host, because the sidecar resolves one
// address per name and two claims on one name is a race decided by map order.
func TestGCP_NoTwoEmulatorsClaimTheSameHost(t *testing.T) {
	t.Parallel()
	owner := make(map[string]string)
	for _, e := range emulator.Builtin() {
		for _, h := range e.Hosts() {
			prev, dup := owner[h]
			require.False(t, dup,
				"%s is claimed by both %s and %s, and the sidecar forwards to one address",
				h, prev, e.Name())
			owner[h] = e.Name()
		}
	}
}

func TestGCP_EveryCoveredServiceNamesTheCallThatProvesItAndItsTransport(t *testing.T) {
	t.Parallel()
	for _, name := range emulator.GCPNames() {
		e := mustGCP(t, name)
		for _, s := range e.Services {
			require.NotEmpty(t, s.Hosts, "%s claims no host", s.Name)
			require.NotEmpty(t, s.Proves,
				"%s is in the surface with nothing proving it, which is a claim", s.Name)
			_, ok := emulator.GCPTransport(s.Name)
			require.True(t, ok,
				"%s is in the surface with no transport recorded, so nothing can say "+
					"whether an unmodified client library reaches it", s.Name)
		}
		require.NotEmpty(t, e.Outside,
			"%s names no gap, and a gap found in a failure is worse than one read first", name)
		for _, s := range e.Outside {
			require.NotEmpty(t, s.Note, "%s is excluded from %s with no reason given", s.Name, name)
		}
	}
}

// The lane's own unflattering number, asserted rather than described. One of
// the six services is reachable by an unmodified client library through the
// sidecar as it exists today. The other five speak gRPC, which is defined
// over HTTP/2, and the sidecar's inspected path negotiates no ALPN and reads
// HTTP/1.1. If somebody teaches the sidecar HTTP/2, this test is what tells
// them the number moved.
func TestGCP_OnlyCloudStorageIsReachableWithoutHTTP2InTheSidecar(t *testing.T) {
	t.Parallel()
	rest, grpc := 0, 0
	for _, name := range emulator.GCPNames() {
		for _, s := range mustGCP(t, name).Services {
			tr, ok := emulator.GCPTransport(s.Name)
			require.True(t, ok)
			switch tr {
			case emulator.TransportREST:
				rest++
				require.Equal(t, "Cloud Storage", s.Name,
					"a second REST service appeared and the guide's number is stale")
			case emulator.TransportGRPC:
				grpc++
			default:
				t.Fatalf("%s records an unknown transport %q", s.Name, tr)
			}
		}
	}
	require.Equal(t, 1, rest, "the guide says one of six and this says otherwise")
	require.Equal(t, 5, grpc)
}

// Host matching lives in more than one place in this repository and what holds
// those places together is a corpus of vectors, never a shared belief. This is
// that corpus for the GCP patterns: every host the surface claims is compiled
// by the REAL policy engine, and the endpoint an SDK resolves is decided by
// the rule the emulator would have written.
func TestGCP_HostPatternsDecideTheEndpointsAnSDKResolves(t *testing.T) {
	t.Parallel()

	var rules []schema.EgressRule
	byHost := make(map[string]*emulator.Emulator)
	for _, name := range emulator.GCPNames() {
		e := mustGCP(t, name)
		for _, h := range e.Hosts() {
			rules = append(rules, schema.EgressRule{Host: h, Mode: schema.ModeAllow})
			byHost[h] = e
		}
	}
	engine, err := policy.New(&schema.Egress{Default: schema.ModeBlock, Rules: rules})
	require.NoError(t, err, "every host in the surface must compile as a policy rule")

	reached := []string{
		"storage.googleapis.com",
		"mybucket.storage.googleapis.com",
		"my.dotted.bucket.storage.googleapis.com",
		"pubsub.googleapis.com",
		"firestore.googleapis.com",
		"datastore.googleapis.com",
		"bigtable.googleapis.com",
		"bigtableadmin.googleapis.com",
		"spanner.googleapis.com",
	}
	for _, host := range reached {
		req, parseErr := policy.ParseRequest("POST", "https://"+host+"/")
		require.NoError(t, parseErr)
		require.True(t, engine.Evaluate(req).Matched(),
			"%s matched no rule the surface writes", host)
		require.True(t, servedBySomeGCPEmulator(t, host),
			"%s is decided by a rule the emulator writes and yet no emulator claims it, "+
				"so the sidecar and the surface table disagree about the same host", host)
	}

	refused := []string{
		"oauth2.googleapis.com", "accounts.google.com", "www.googleapis.com",
		"secretmanager.googleapis.com", "cloudtasks.googleapis.com",
		"bigquery.googleapis.com", "storage.us-central1.rep.googleapis.com",
		"storage.googleapis.com.evil.example", "googleapis.com",
	}
	for _, host := range refused {
		req, parseErr := policy.ParseRequest("POST", "https://"+host+"/")
		require.NoError(t, parseErr)
		require.False(t, engine.Evaluate(req).Matched(),
			"%s is outside the surface and a rule the emulator writes decided it", host)
		require.False(t, servedBySomeGCPEmulator(t, host),
			"%s is outside the surface and an emulator answered it", host)
	}
}

func servedBySomeGCPEmulator(t *testing.T, host string) bool {
	t.Helper()
	for _, name := range emulator.GCPNames() {
		if _, ok := mustGCP(t, name).ServiceFor(host); ok {
			return true
		}
	}
	return false
}

func mustGCP(t *testing.T, name string) *emulator.Emulator {
	t.Helper()
	e, ok := emulator.Named(name)
	require.True(t, ok, "%s is not built in", name)
	return e
}

// The Spanner emulator is the only image here with no arm64 member, and a
// declaration that lost that fact would send somebody looking for why their
// laptop is slow. The test is about the comment being true rather than about
// the digest: a bump to a future multi architecture Spanner image should make
// this fail so the guide's table gets corrected with it.
func TestGCP_TheThreeImagesAreTheThreeThisSurfacePulls(t *testing.T) {
	t.Parallel()
	byImage := map[string][]string{}
	for _, name := range emulator.GCPNames() {
		img := mustGCP(t, name).Container().Image
		byImage[img] = append(byImage[img], name)
	}
	require.Len(t, byImage, 3,
		"six emulators must come from three images, because the four gcloud "+
			"ones share the CLI image and its layers are pulled once")
	var shared []string
	for _, names := range byImage {
		if len(names) == 4 {
			shared = names
		}
	}
	require.ElementsMatch(t,
		[]string{emulator.PubSubName, emulator.FirestoreName,
			emulator.DatastoreName, emulator.BigtableName}, shared,
		"the image shared by four emulators must be the Google Cloud CLI one")
}

// The command is what decides which emulator a gcloud container is, so a
// missing one is four identical containers that run a shell and answer
// nothing. An image whose entrypoint is already the emulator must NOT carry
// one, because a command there would replace the entrypoint.
func TestGCP_OnlyTheSharedImageNeedsACommandAndAllFourHaveOne(t *testing.T) {
	t.Parallel()
	needsCommand := map[string]string{
		emulator.PubSubName:    "pubsub",
		emulator.FirestoreName: "firestore",
		emulator.DatastoreName: "datastore",
		emulator.BigtableName:  "bigtable",
	}
	for name, want := range needsCommand {
		cmd := mustGCP(t, name).Container().Command
		require.NotEmpty(t, cmd,
			"%s runs the Google Cloud CLI image, whose command is bash, so with no "+
				"command it starts a shell and answers nothing", name)
		require.Equal(t, "gcloud", cmd[0])
		require.Contains(t, cmd, want, "%s does not start its own emulator", name)
		require.Contains(t, strings.Join(cmd, " "), "--host-port=0.0.0.0:",
			"%s binds localhost, and the sidecar forwards to the container's "+
				"address on the inner network", name)
	}
	for _, name := range []string{emulator.GCSName, emulator.SpannerName} {
		require.Empty(t, mustGCP(t, name).Container().Command,
			"%s has the emulator as its entrypoint, and a command would replace it", name)
	}
}
