package detect_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/detect"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// A compose file running LocalStack is the strongest evidence there is that
// an application talks to AWS, stronger than a dependency name: somebody has
// already gone to the trouble of standing one up and pointing the application
// at it. It was previously about to be detected as a service to build, and the
// cloud it answers for produced no rules at all.
//
// It is also the thing this product replaces, because reaching an emulator
// costs an endpoint override, and the override means the code under test is
// not the code that ships.

// localstackRepo runs LocalStack and names three services, with no AWS SDK in
// the dependency list at all. That is the case that matters: the roster comes
// from what the developer configured rather than from what they installed.
func localstackRepo(services string) map[string]string {
	env := ""
	if services != "" {
		env = "    environment:\n      SERVICES: " + services + "\n"
	}
	return map[string]string{
		"package.json": `{"name":"shopfront","scripts":{"start":"next start"},
			"dependencies":{"next":"15.0.0"}}`,
		"docker-compose.yml": `services:
  web:
    build: .
    ports:
      - "3000:3000"
  localstack:
    image: localstack/localstack:3.4
    ports:
      - "4566:4566"
` + env,
		"Dockerfile": "FROM node:20\nCMD [\"node\", \"server.js\"]\n",
	}
}

func emulatorFor(t *testing.T, res *detect.Result, service string) detect.DetectedEmulator {
	t.Helper()
	for _, e := range res.Emulators {
		if e.Service == service {
			return e
		}
	}
	var names []string
	for _, e := range res.Emulators {
		names = append(names, e.Service)
	}
	t.Fatalf("no emulator for service %q; found %v", service, names)
	return detect.DetectedEmulator{}
}

func TestEmulator_AnEmulatorIsNotAServiceToBuild(t *testing.T) {
	t.Parallel()
	// It carries a port and an image, which is everything a service needs to
	// look like one. Building it would put a copy of LocalStack in the
	// manifest as though it were part of the application.
	res := run(t, "shopfront", localstackRepo("s3,sqs,sns"))
	for _, s := range res.Draft.Services {
		require.NotEqual(t, "localstack", s.Name,
			"the emulator is infrastructure the environment replaces, not code to build")
	}
}

func TestEmulator_TheCloudItAnswersForIsNamed(t *testing.T) {
	t.Parallel()
	res := run(t, "shopfront", localstackRepo("s3,sqs,sns"))
	em := emulatorFor(t, res, "localstack")
	require.Equal(t, "aws", em.Cloud)
	require.Equal(t, "LocalStack", em.Product)
	require.Equal(t, "docker-compose.yml", em.Evidence)
	require.Equal(t, []string{"s3", "sns", "sqs"}, em.Services)
}

func TestEmulator_TheServicesItNamesBecomeRules(t *testing.T) {
	t.Parallel()
	// There is no @aws-sdk anything in this repository. Without the emulator
	// being read, S3, SQS and SNS get no rule, and each call is refused with
	// "no rule matches" rather than by name.
	res := run(t, "shopfront", localstackRepo("s3,sqs,sns"))
	for _, host := range []string{
		"s3.amazonaws.com", "s3.*.amazonaws.com", "*.s3.amazonaws.com", "*.s3.*.amazonaws.com",
		"sqs.*.amazonaws.com", "sns.*.amazonaws.com",
	} {
		r := ruleFor(t, res.Draft, host)
		require.Equal(t, schema.ModeBlock, r.Mode, "%s must be refused rather than answered", host)
		require.NotEmpty(t, r.Note, "%s is blocked and the manifest must say why", host)
	}
}

func TestEmulator_AServiceItDoesNotNameGetsNoRule(t *testing.T) {
	t.Parallel()
	// The emulator is the roster, and a roster that adds services nobody
	// listed is a wildcard with extra steps. A repository running LocalStack
	// for S3 alone does not get a Secrets Manager rule it has no use for.
	res := run(t, "shopfront", localstackRepo("s3"))
	var hosts []string
	for _, r := range res.Draft.Egress.Rules {
		hosts = append(hosts, r.Host)
	}
	require.Contains(t, hosts, "s3.amazonaws.com")
	require.NotContains(t, hosts, "secretsmanager.*.amazonaws.com",
		"an emulator naming S3 is not evidence that the application reads secrets")
	require.NotContains(t, hosts, "*.amazonaws.com",
		"a cloud named with no service named is the wildcard this catalog removed")
}

func TestEmulator_AnEmulatorNamingNothingWritesNoRules(t *testing.T) {
	t.Parallel()
	// LocalStack with no SERVICES answers for whatever is asked of it. That is
	// not a list, and inventing one from it would put every AWS host in the
	// manifest on the strength of a container nobody configured.
	res := run(t, "shopfront", localstackRepo(""))
	em := emulatorFor(t, res, "localstack")
	require.Empty(t, em.Services)
	require.Empty(t, em.Hosts)
	for _, r := range res.Draft.Egress.Rules {
		require.NotContains(t, r.Host, "amazonaws.com",
			"an emulator that named no service cannot produce an AWS rule")
	}
	require.Contains(t, em.Note(), "It names no services",
		"the reader has to be told why an emulator they can see produced nothing")
}

func TestEmulator_AServiceTheCatalogDoesNotClaimIsReported(t *testing.T) {
	t.Parallel()
	// The instrument that can say no. A service the developer configured and
	// the catalog has no host for is a refusal already scheduled, and af init
	// is the last cheap moment to fix it.
	res := run(t, "shopfront", localstackRepo("s3,textract,rekognition"))
	em := emulatorFor(t, res, "localstack")
	require.Equal(t, []string{"rekognition", "textract"}, em.Unnamed)

	note := em.Note()
	require.Contains(t, note, "textract")
	require.Contains(t, note, "rekognition")
	require.Contains(t, note, "no rule matches",
		"the note must say what the refusal will actually look like")
}

func TestEmulator_ARuleTheDependencyScanAlreadyWroteIsNotWrittenTwice(t *testing.T) {
	t.Parallel()
	// Two rules for one host is a manifest the validator refuses, and the
	// first one carries the same reason the second would.
	files := localstackRepo("s3,sqs")
	files["package.json"] = `{"name":"shopfront","scripts":{"start":"next start"},
		"dependencies":{"next":"15.0.0","@aws-sdk/client-s3":"3.0.0"}}`
	res := run(t, "shopfront", files)

	seen := map[string]int{}
	for _, r := range res.Draft.Egress.Rules {
		seen[r.Host]++
	}
	for host, n := range seen {
		require.Equal(t, 1, n, "%s has %d rules and the validator accepts one", host, n)
	}
	requireDraftValidates(t, res.Draft, files)
}

func TestEmulator_AzuriteAnswersForAzureAndNotForAWS(t *testing.T) {
	t.Parallel()
	// The cloud is read off the image rather than assumed, and the three
	// emulators do not share a table.
	res := run(t, "shopfront", map[string]string{
		"package.json": `{"name":"shopfront","scripts":{"start":"next start"},
			"dependencies":{"next":"15.0.0"}}`,
		"docker-compose.yml": `services:
  web:
    build: .
    ports:
      - "3000:3000"
  azurite:
    image: mcr.microsoft.com/azure-storage/azurite:3.29.0
    environment:
      SERVICES: blob,queue
`,
		"Dockerfile": "FROM node:20\nCMD [\"node\", \"server.js\"]\n",
	})
	em := emulatorFor(t, res, "azurite")
	require.Equal(t, "azure", em.Cloud)
	require.Equal(t, "Azurite", em.Product)
	r := ruleFor(t, res.Draft, "*.blob.core.windows.net")
	require.Equal(t, schema.ModeBlock, r.Mode)
	for _, rule := range res.Draft.Egress.Rules {
		require.NotContains(t, rule.Host, "amazonaws.com",
			"an Azure emulator is not evidence about AWS")
	}
}

func TestEmulator_MinIOIsAnObjectStoreAndNotAnEmulator(t *testing.T) {
	t.Parallel()
	// The distinction the ordering in ComposeAnalyzer exists for.
	// fake-gcs-server and MinIO are both object stores, and only one of them
	// stands in for a named cloud service. Calling MinIO an AWS emulator would
	// write nine S3 rules for an application that never calls AWS.
	res := run(t, "shopfront", map[string]string{
		"package.json": `{"name":"shopfront","scripts":{"start":"next start"},
			"dependencies":{"next":"15.0.0"}}`,
		"docker-compose.yml": `services:
  web:
    build: .
    ports:
      - "3000:3000"
  minio:
    image: minio/minio:latest
`,
		"Dockerfile": "FROM node:20\nCMD [\"node\", \"server.js\"]\n",
	})
	require.Empty(t, res.Emulators, "MinIO is object storage the environment runs, not a stand in for S3")
}

func TestEmulator_TheCloudSDKGapIsNamedFromTheDependencyList(t *testing.T) {
	t.Parallel()
	// The dependency half of the same instrument. Textract is a service this
	// application calls and the catalog has no host for, so it is reported by
	// name rather than discovered as a refusal in an environment.
	res := run(t, "shopfront", map[string]string{
		"package.json": `{"name":"shopfront","scripts":{"start":"next start"},
			"dependencies":{"next":"15.0.0","@aws-sdk/client-textract":"3.0.0"}}`,
	})
	var details []string
	for _, f := range detect.OfKind(res.Findings, detect.KindNote) {
		if f.Subject == "cloud-service.aws.textract" {
			details = append(details, f.Detail)
		}
	}
	require.Len(t, details, 1, "a cloud SDK the catalog does not claim must be reported exactly once")
	require.Contains(t, details[0], "@aws-sdk/client-textract")
	require.Contains(t, details[0], "no rule matches")
}

func TestEmulator_ASDKForAServiceTheCatalogClaimsIsNotAGap(t *testing.T) {
	t.Parallel()
	// The other direction. A gap report for a service that has a rule is
	// noise, and noise is what makes a report stop being read.
	res := run(t, "shopfront", map[string]string{
		"package.json": `{"name":"shopfront","scripts":{"start":"next start"},
			"dependencies":{"next":"15.0.0","@aws-sdk/client-s3":"3.0.0","boto3":"1.34.0"}}`,
	})
	for _, f := range detect.OfKind(res.Findings, detect.KindNote) {
		require.NotContains(t, f.Subject, "cloud-service.",
			"%s has a rule, so reporting it as a gap is noise", f.Subject)
	}
}
