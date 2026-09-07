package detect_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/policy"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The catalog used to register *.amazonaws.com under "Amazon SES" in capture
// mode. Every AWS host an application touches matched a mail rule, so an S3
// PUT, an SQS SendMessage and a Secrets Manager read all fell through to the
// generic capture handler and were answered 200 with an empty body by code
// that believed it was holding an email.
//
// These tests are about what the catalog now says, and the af-proxy suite is
// about what the sidecar now does with it. Both halves are needed: a rule
// nothing enforces is a comment, and an enforcement with no rule behind it
// refuses the wrong things.

// awsSDKRepo is a repository that reaches every AWS service the catalog names.
func awsSDKRepo() map[string]string {
	return map[string]string{
		"package.json": `{"name":"shopfront",
			"scripts":{"start":"next start"},
			"dependencies":{"next":"15.0.0",
			"@aws-sdk/client-s3":"3.0.0",
			"@aws-sdk/client-sqs":"3.0.0",
			"@aws-sdk/client-sns":"3.0.0",
			"@aws-sdk/client-dynamodb":"3.0.0",
			"@aws-sdk/client-kinesis":"3.0.0",
			"@aws-sdk/client-eventbridge":"3.0.0",
			"@aws-sdk/client-secrets-manager":"3.0.0",
			"@aws-sdk/client-ssm":"3.0.0",
			"@aws-sdk/client-sts":"3.0.0",
			"@aws-sdk/client-sesv2":"3.0.0"}}`,
	}
}

func TestCatalog_NoRuleCoversTheWholeOfAWS(t *testing.T) {
	t.Parallel()
	// The finding this lane exists for. One wildcard under a mail rule decided
	// every AWS host in the account, and it decided capture.
	res := run(t, "shopfront", awsSDKRepo())
	for _, r := range res.Draft.Egress.Rules {
		require.NotEqual(t, "*.amazonaws.com", r.Host,
			"a single wildcard cannot be allowed to decide S3, SQS, STS and SES together")
	}
}

func TestCatalog_SESIsScopedToTheMailEndpoint(t *testing.T) {
	t.Parallel()
	res := run(t, "shopfront", awsSDKRepo())
	ses := ruleFor(t, res.Draft, "email.*.amazonaws.com")
	require.Equal(t, schema.ModeCapture, ses.Mode,
		"mail is still captured, and only mail is")
	require.NotEmpty(t, ses.Note, "a rule nobody can explain must not appear in a manifest")
}

func TestCatalog_EveryCloudServiceIsNamedAndBlocked(t *testing.T) {
	t.Parallel()
	// This is the lane's number, asserted rather than counted by hand. Each of
	// these was previously reachable only through the wildcard, or not named
	// at all in the case of GCP and Azure, of which the catalog held nothing.
	res := run(t, "shopfront", map[string]string{
		"package.json": `{"name":"shopfront",
			"scripts":{"start":"next start"},
			"dependencies":{"next":"15.0.0",
			"@aws-sdk/client-s3":"3.0.0","@aws-sdk/client-sqs":"3.0.0",
			"@aws-sdk/client-sns":"3.0.0","@aws-sdk/client-dynamodb":"3.0.0",
			"@aws-sdk/client-kinesis":"3.0.0","@aws-sdk/client-eventbridge":"3.0.0",
			"@aws-sdk/client-secrets-manager":"3.0.0","@aws-sdk/client-ssm":"3.0.0",
			"@aws-sdk/client-sts":"3.0.0",
			"@google-cloud/storage":"7.0.0","@google-cloud/pubsub":"4.0.0",
			"@google-cloud/firestore":"7.0.0","@google-cloud/secret-manager":"5.0.0",
			"@google-cloud/tasks":"5.0.0",
			"@azure/storage-blob":"12.0.0","@azure/storage-queue":"12.0.0",
			"@azure/service-bus":"7.0.0","@azure/cosmos":"4.0.0",
			"@azure/data-tables":"13.0.0","@azure/storage-file-share":"12.0.0",
			"@azure/keyvault-secrets":"4.0.0"}}`,
	})

	for _, host := range []string{
		// AWS.
		"s3.amazonaws.com", "s3.*.amazonaws.com", "*.s3.amazonaws.com", "*.s3.*.amazonaws.com",
		"sqs.*.amazonaws.com", "sns.*.amazonaws.com",
		"dynamodb.*.amazonaws.com", "streams.dynamodb.*.amazonaws.com",
		"kinesis.*.amazonaws.com", "events.*.amazonaws.com",
		"secretsmanager.*.amazonaws.com", "ssm.*.amazonaws.com",
		"sts.amazonaws.com", "sts.*.amazonaws.com",
		// GCP.
		"storage.googleapis.com", "pubsub.googleapis.com", "firestore.googleapis.com",
		"secretmanager.googleapis.com", "cloudtasks.googleapis.com",
		// Azure.
		"*.blob.core.windows.net", "*.queue.core.windows.net", "*.servicebus.windows.net",
		"*.table.core.windows.net", "*.file.core.windows.net",
		"*.documents.azure.com", "*.vault.azure.net",
	} {
		r := ruleFor(t, res.Draft, host)
		require.Equal(t, schema.ModeBlock, r.Mode, "%s must be refused rather than answered", host)
		require.NotEmpty(t, r.Note, "%s is blocked and the manifest must say why", host)
	}
}

func TestCatalog_TheDraftStillCompilesAndValidates(t *testing.T) {
	t.Parallel()
	// A pattern the catalog writes and the policy engine refuses is worse than
	// no rule: af up fails on a manifest af init produced. The star in the
	// middle is new, so this is the check that the two agree about it.
	files := awsSDKRepo()
	res := run(t, "shopfront", files)
	requireDraftValidates(t, res.Draft, files)
}

func TestCatalog_ASESDependencyDoesNotBlockTheRestOfAWS(t *testing.T) {
	t.Parallel()
	// The other direction of the same defect. Attributing aws-sdk to SES meant
	// every AWS user got a mail rule; scoping SES to its own client means a
	// repository that only sends mail does not get nine block rules it has no
	// use for.
	res := run(t, "mailer", map[string]string{
		"package.json": `{"name":"mailer","scripts":{"start":"next start"},
			"dependencies":{"next":"15.0.0","@aws-sdk/client-sesv2":"3.0.0"}}`,
	})
	var hosts []string
	for _, r := range res.Draft.Egress.Rules {
		hosts = append(hosts, r.Host)
	}
	require.Contains(t, hosts, "email.*.amazonaws.com")
	require.NotContains(t, hosts, "s3.amazonaws.com",
		"a mail client is not evidence that the application touches S3")
}

func TestCatalog_TheReasonsAreWrittenForAPersonAndNotShared(t *testing.T) {
	t.Parallel()
	// A note repeated across nine services says nothing about any of them, and
	// the whole argument for naming each service is that the refusal can be
	// specific. The mail sentence in particular must not spread back over the
	// cloud, which is exactly the shape the old wildcard had.
	res := run(t, "shopfront", awsSDKRepo())
	notes := map[string]bool{}
	for _, r := range res.Draft.Egress.Rules {
		if r.Mode != schema.ModeBlock || !strings.Contains(r.Host, "amazonaws.com") {
			continue
		}
		require.NotContains(t, r.Note, "captured into the inbox",
			"%s is not mail and its refusal must not be explained as though it were", r.Host)
		notes[r.Note] = true
	}
	// Nine AWS services are blocked here and each one costs something
	// different when reached, so nine distinct sentences is the floor. S3
	// carries four host spellings that correctly share one.
	require.GreaterOrEqual(t, len(notes), 9,
		"blocked services are sharing a reason, which means the reason explains none of them")
}

// cloudEndpoints are concrete hosts, as an application resolves them, rather
// than the patterns a manifest holds. The measurement below is only worth
// anything against these: a pattern matching itself proves nothing, and the
// question is what happens to the request the SDK actually makes.
var cloudEndpoints = []string{
	// AWS, in two regions, in both S3 addressing styles.
	"s3.amazonaws.com",
	"s3.us-east-1.amazonaws.com",
	"shopfront-uploads.s3.amazonaws.com",
	"shopfront-uploads.s3.eu-west-1.amazonaws.com",
	"sqs.us-east-1.amazonaws.com",
	"sqs.eu-west-1.amazonaws.com",
	"sns.us-east-1.amazonaws.com",
	"dynamodb.us-east-1.amazonaws.com",
	"streams.dynamodb.us-east-1.amazonaws.com",
	"kinesis.us-east-1.amazonaws.com",
	"events.eu-west-1.amazonaws.com",
	"secretsmanager.us-east-1.amazonaws.com",
	"ssm.eu-west-1.amazonaws.com",
	"sts.amazonaws.com",
	"sts.us-east-1.amazonaws.com",
	"email.us-east-1.amazonaws.com",
	"email.eu-west-2.amazonaws.com",
	"email-smtp.us-east-1.amazonaws.com",
	// Google.
	"storage.googleapis.com",
	"pubsub.googleapis.com",
	"firestore.googleapis.com",
	"secretmanager.googleapis.com",
	"cloudtasks.googleapis.com",
	// Azure.
	"shopfront.blob.core.windows.net",
	"shopfront.queue.core.windows.net",
	"shopfront.servicebus.windows.net",
	"shopfront.table.core.windows.net",
	"shopfront.file.core.windows.net",
	"shopfront.documents.azure.com",
	"shopfront.vault.azure.net",
}

// everyCloudSDK is a repository that reaches all three clouds.
func everyCloudSDK() map[string]string {
	return map[string]string{
		"package.json": `{"name":"shopfront",
			"scripts":{"start":"next start"},
			"dependencies":{"next":"15.0.0",
			"@aws-sdk/client-s3":"3.0.0","@aws-sdk/client-sqs":"3.0.0",
			"@aws-sdk/client-sns":"3.0.0","@aws-sdk/client-dynamodb":"3.0.0",
			"@aws-sdk/client-kinesis":"3.0.0","@aws-sdk/client-eventbridge":"3.0.0",
			"@aws-sdk/client-secrets-manager":"3.0.0","@aws-sdk/client-ssm":"3.0.0",
			"@aws-sdk/client-sts":"3.0.0","@aws-sdk/client-sesv2":"3.0.0",
			"@google-cloud/storage":"7.0.0","@google-cloud/pubsub":"4.0.0",
			"@google-cloud/firestore":"7.0.0","@google-cloud/secret-manager":"5.0.0",
			"@google-cloud/tasks":"5.0.0",
			"@azure/storage-blob":"12.0.0","@azure/storage-queue":"12.0.0",
			"@azure/service-bus":"7.0.0","@azure/cosmos":"4.0.0",
			"@azure/data-tables":"13.0.0","@azure/storage-file-share":"12.0.0",
			"@azure/keyvault-secrets":"4.0.0"}}`,
		".env.example": "SES_SMTP_USERNAME=\nSES_SMTP_PASSWORD=\n",
	}
}

// TestCatalog_TheNumber measures what the catalog decides about every cloud
// host an application touches, before and after, and prints both.
//
// It is a measurement rather than an assertion about a count, so it prints
// what it found and fails only on the property that matters: nothing is
// answered as though it had succeeded, and nothing falls through unnamed.
// A number nobody can reproduce is marketing, so the command is the test.
func TestCatalog_TheNumber(t *testing.T) {
	t.Parallel()

	// Before: the catalog's SES entry carried *.amazonaws.com in capture mode
	// and held no Google or Azure host at all.
	before, err := policy.New(&schema.Egress{
		Default: schema.ModeBlock,
		Rules: []schema.EgressRule{
			{Host: "email.us-east-1.amazonaws.com", Mode: schema.ModeCapture},
			{Host: "*.amazonaws.com", Mode: schema.ModeCapture},
		},
	})
	require.NoError(t, err)

	res := run(t, "shopfront", everyCloudSDK())
	after, err := policy.New(res.Draft.Egress)
	require.NoError(t, err, "the catalog must produce a policy the engine compiles")

	count := func(e *policy.Engine) (named, invented, unnamed int) {
		for _, host := range cloudEndpoints {
			d := e.Evaluate(policy.Request{Host: host, TLS: true, Path: "/", Method: "PUT"})
			if !d.Matched() {
				unnamed++
				continue
			}
			named++
			// An invented success is a capture with no handler behind it and
			// no rule naming the host. It is what the sidecar refuses now.
			if d.Mode == schema.ModeCapture && !d.NamesHost() {
				invented++
			}
		}
		return named, invented, unnamed
	}

	bNamed, bInvented, bUnnamed := count(before)
	aNamed, aInvented, aUnnamed := count(after)

	t.Logf("cloud endpoints probed: %d", len(cloudEndpoints))
	t.Logf("before: %d named by a rule, %d unnamed, %d answered 200 by one wildcard",
		bNamed, bUnnamed, bInvented)
	t.Logf("after:  %d named by a rule, %d unnamed, %d answered 200 by one wildcard",
		aNamed, aUnnamed, aInvented)

	require.Equal(t, len(cloudEndpoints), aNamed,
		"every cloud host the application touches must be named by a rule")
	require.Zero(t, aInvented,
		"nothing may be answered as a success by a rule that did not name it")
	require.Positive(t, bInvented,
		"the before figure has to be non zero or this measures nothing")
}
