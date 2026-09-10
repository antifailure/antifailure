package emulator_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/policy"
	"github.com/antifailure/antifailure/engine/pkg/emulator"
	"github.com/antifailure/antifailure/engine/pkg/extension"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The AWS emulator is a declaration, and every property below is a property
// somebody downstream depends on: the registry refuses an unpinned image, the
// sidecar routes what Hosts returns, and a service in the surface table with
// nothing proving it is a claim rather than a capability.

func TestAWS_IsBuiltInUnderTheNameAnEgressRuleWouldUse(t *testing.T) {
	t.Parallel()
	e, ok := emulator.Named(emulator.AWSName)
	require.True(t, ok, "an egress rule naming aws must find an emulator")
	require.Equal(t, "aws", e.Name())
	require.Contains(t, emulator.Names(), "aws")
}

func TestAWS_ImageIsPinnedByDigestRatherThanATag(t *testing.T) {
	t.Parallel()
	e, _ := emulator.Named(emulator.AWSName)
	require.Contains(t, e.Container().Image, "@sha256:",
		"a tag that moves changes what an environment was tested against")
	require.Equal(t, 4566, e.Container().Port)
}

// The registry validates registrations before an environment is created, and
// it already refuses a hostless emulator and an unpinned image. A built in
// emulator that could not pass the check outside registrations are held to
// would be a double standard the first outside emulator would discover.
func TestAWS_PassesTheRegistryValidationOutsideEmulatorsAreHeldTo(t *testing.T) {
	t.Parallel()
	r := extension.NewRegistry()
	r.AddEmulator(mustAWS(t))
	require.NoError(t, r.Validate(map[string][]string{}))

	named, ok := r.EmulatorNamed("aws")
	require.True(t, ok)
	require.NotEmpty(t, named.Hosts())
}

func TestAWS_CoversEveryServiceTheLaneOwes(t *testing.T) {
	t.Parallel()
	e := mustAWS(t)
	owed := []string{
		"Amazon S3", "Amazon SQS", "Amazon SNS", "Amazon DynamoDB",
		"Amazon Kinesis", "Amazon EventBridge", "AWS Secrets Manager",
		"AWS Systems Manager Parameter Store", "AWS STS",
	}
	have := make(map[string]bool)
	for _, s := range e.Services {
		have[s.Name] = true
	}
	for _, name := range owed {
		require.True(t, have[name], "%s is missing from the emulated surface", name)
	}
	require.Len(t, e.Services, len(owed),
		"a service in the surface that nothing above owes is a claim nobody checks")
}

// STS is the one that fails at startup rather than at the call site, so it
// gets its own test rather than sitting inside the list above. Several
// credential chains call it before the first real request, and an emulated
// surface without it fails with an error naming the wrong service.
func TestAWS_AnswersForSTSBothGloballyAndRegionally(t *testing.T) {
	t.Parallel()
	e := mustAWS(t)
	for _, host := range []string{"sts.amazonaws.com", "sts.us-east-1.amazonaws.com"} {
		svc, ok := e.ServiceFor(host)
		require.True(t, ok, "%s is not routed to the emulator", host)
		require.Equal(t, "AWS STS", svc.Name)
	}
}

// Virtual hosted addressing is the case this lane is loudest about: the bucket
// travels in the Host header, which is why the sidecar preserves it.
func TestAWS_AnswersForBothS3AddressingStyles(t *testing.T) {
	t.Parallel()
	e := mustAWS(t)
	for _, host := range []string{
		"s3.amazonaws.com",
		"s3.us-east-1.amazonaws.com",
		"mybucket.s3.amazonaws.com",
		"mybucket.s3.us-east-1.amazonaws.com",
	} {
		svc, ok := e.ServiceFor(host)
		require.True(t, ok, "%s is not routed to the emulator", host)
		require.Equal(t, "Amazon S3", svc.Name)
	}
}

// The expensive half of the lane. A silent wrong answer from an emulator is
// worse than a refusal because it will be trusted, so a service outside the
// surface must not be routed here at all.
func TestAWS_RefusesAWSHostsOutsideTheSurface(t *testing.T) {
	t.Parallel()
	e := mustAWS(t)
	for _, host := range []string{
		"lambda.us-east-1.amazonaws.com",
		"email.us-east-1.amazonaws.com",
		"rds.us-east-1.amazonaws.com",
		"apigateway.us-east-1.amazonaws.com",
		"cloudformation.us-east-1.amazonaws.com",
		"iam.amazonaws.com",
		"monitoring.us-east-1.amazonaws.com",
		"ecs.us-east-1.amazonaws.com",
	} {
		_, ok := e.ServiceFor(host)
		require.False(t, ok, "%s is outside the surface and must not be answered", host)
	}
}

func TestAWS_EveryCoveredServiceNamesTheCallThatProvesIt(t *testing.T) {
	t.Parallel()
	e := mustAWS(t)
	for _, s := range e.Services {
		require.NotEmpty(t, s.Hosts, "%s claims no host", s.Name)
		require.NotEmpty(t, s.Proves,
			"%s is in the surface with nothing proving it, which is a claim", s.Name)
	}
	require.NotEmpty(t, e.Outside, "a gap found in a failure is worse than one read first")
	for _, s := range e.Outside {
		require.NotEmpty(t, s.Note, "%s is excluded with no reason given", s.Name)
	}
}

func TestAWS_RecordsTheLicenceTheNoticesFileOwes(t *testing.T) {
	t.Parallel()
	e := mustAWS(t)
	require.Equal(t, "Apache License 2.0", e.Licence.Name)
	require.Contains(t, e.Licence.Holder, "LocalStack")
	require.True(t, strings.HasPrefix(e.Licence.URL, "https://"))
	require.Equal(t, "LocalStack", e.Project)
}

// Host matching lives in more than one place in this repository and the thing
// that holds those places together is a corpus, not a shared function. This is
// that corpus for the emulator's own patterns: every host the surface claims
// is compiled by the REAL policy engine, and the endpoint an SDK resolves is
// decided by the rule the emulator would have written.
func TestAWS_HostPatternsDecideTheEndpointsAnSDKResolves(t *testing.T) {
	t.Parallel()
	e := mustAWS(t)

	rules := make([]schema.EgressRule, 0, len(e.Hosts()))
	for _, h := range e.Hosts() {
		rules = append(rules, schema.EgressRule{Host: h, Mode: schema.ModeAllow})
	}
	engine, err := policy.New(&schema.Egress{Default: schema.ModeBlock, Rules: rules})
	require.NoError(t, err, "every host in the surface must compile as a policy rule")

	reached := []string{
		"s3.amazonaws.com", "s3.eu-west-1.amazonaws.com",
		"mybucket.s3.amazonaws.com", "my.bucket.s3.us-east-1.amazonaws.com",
		"sqs.us-east-1.amazonaws.com", "sns.ap-south-1.amazonaws.com",
		"dynamodb.us-east-2.amazonaws.com", "streams.dynamodb.us-east-2.amazonaws.com",
		"kinesis.us-west-2.amazonaws.com", "events.eu-central-1.amazonaws.com",
		"secretsmanager.us-east-1.amazonaws.com", "ssm.us-east-1.amazonaws.com",
		"sts.amazonaws.com", "sts.us-east-1.amazonaws.com",
	}
	for _, host := range reached {
		req, parseErr := policy.ParseRequest("POST", "https://"+host+"/")
		require.NoError(t, parseErr)
		d := engine.Evaluate(req)
		require.True(t, d.Matched(), "%s matched no rule the surface writes", host)

		_, covered := e.ServiceFor(host)
		require.True(t, covered,
			"%s is decided by a rule the emulator writes and yet ServiceFor refuses it, "+
				"so the sidecar and the surface table disagree about the same host", host)
	}

	refused := []string{
		"lambda.us-east-1.amazonaws.com", "email.us-east-1.amazonaws.com",
		"iam.amazonaws.com", "rds.us-east-1.amazonaws.com",
		"s3.amazonaws.com.evil.example", "notamazonaws.com",
	}
	for _, host := range refused {
		req, parseErr := policy.ParseRequest("POST", "https://"+host+"/")
		require.NoError(t, parseErr)
		d := engine.Evaluate(req)
		require.False(t, d.Matched(),
			"%s is outside the surface and a rule the emulator writes decided it", host)

		_, covered := e.ServiceFor(host)
		require.False(t, covered, "%s is outside the surface and ServiceFor answered it", host)
	}
}

func mustAWS(t *testing.T) *emulator.Emulator {
	t.Helper()
	e, ok := emulator.Named(emulator.AWSName)
	require.True(t, ok)
	return e
}

// The guide is where a person reads what the emulator answers for before they
// depend on it, and a table that drifts from the code is worse than no table:
// it is a wrong answer somebody trusts. So the guide's surface table is
// checked against the declaration rather than maintained beside it.
func TestAWS_TheGuideRecordsTheSurfaceTheCodeAnswersFor(t *testing.T) {
	t.Parallel()
	guide := readGuide(t)
	e := mustAWS(t)

	for _, s := range e.Services {
		require.Contains(t, guide, s.Name,
			"%s is answered by the emulator and the guide does not name it", s.Name)
		for _, h := range s.Hosts {
			require.Contains(t, guide, "`"+h+"`",
				"%s is routed to the emulator and the guide does not name it", h)
		}
		require.Contains(t, guide, s.Proves,
			"the guide does not carry what proves %s", s.Name)
	}
	for _, s := range e.Outside {
		require.Contains(t, guide, strings.SplitN(s.Name, ",", 2)[0],
			"%s is refused and the guide does not say so", s.Name)
	}
	require.Contains(t, guide, e.Container().Image,
		"the guide names an image other than the one the engine starts")
}

func readGuide(t *testing.T) string {
	t.Helper()
	// From engine/pkg/emulator to the repository root.
	path := filepath.Join("..", "..", "..",
		"docs", "src", "content", "docs", "guides", "aws.md")
	body, err := os.ReadFile(path)
	require.NoError(t, err, "the AWS guide is where the surface is published")
	return string(body)
}

// The engine resolves an emulate rule through the registry and through nothing
// else, so an emulator this repository ships has to arrive the same way one
// written outside it does. A declaration nobody registers is a provider named
// and not built.
func TestRegisterBuiltin_PutsTheAWSEmulatorWhereTheEngineLooksForIt(t *testing.T) {
	t.Parallel()
	r := extension.NewRegistry()
	emulator.RegisterBuiltin(r)

	found, ok := r.EmulatorNamed("aws")
	require.True(t, ok, "an egress rule naming aws would be refused by this build")
	require.Contains(t, found.Container().Image, "@sha256:")
	require.NoError(t, r.Validate(map[string][]string{}))
}

// An organization that registered its own licensed image under the name aws
// made a deliberate choice, and a built in registration must not undo it or
// sit beside it. Two emulators under one name is what the registry refuses.
func TestRegisterBuiltin_LeavesAnOutsideRegistrationOfTheSameNameAlone(t *testing.T) {
	t.Parallel()
	r := extension.NewRegistry()
	theirs := &outsideEmulator{}
	r.AddEmulator(theirs)
	emulator.RegisterBuiltin(r)

	// Exactly one aws, rather than the whole list of names. This build also
	// ships the Azure emulators, and an assertion that spelled out every
	// builtin would fail the next time one is added, for a reason that has
	// nothing to do with what this test is about.
	names := r.EmulatorNames()
	aws := 0
	for _, n := range names {
		if n == "aws" {
			aws++
		}
	}
	require.Equal(t, 1, aws,
		"the built in registration was added beside theirs, and the registry refuses that")

	// The premise, so that this cannot pass by RegisterBuiltin having
	// registered nothing at all: every other builtin did arrive.
	for _, e := range emulator.Builtin() {
		if e.Name() == "aws" {
			continue
		}
		require.Contains(t, names, e.Name(),
			"RegisterBuiltin skipped %s as well, so the aws skip proves nothing", e.Name())
	}
	found, ok := r.EmulatorNamed("aws")
	require.True(t, ok)
	require.Equal(t, "example.invalid/localstack@sha256:"+strings.Repeat("a", 64),
		found.Container().Image, "the built in image displaced the one they chose")
	require.NoError(t, r.Validate(map[string][]string{}))
}

// outsideEmulator is what an organization with a LocalStack licence registers:
// the same name, their own image.
type outsideEmulator struct{}

func (outsideEmulator) Name() string    { return "aws" }
func (outsideEmulator) Hosts() []string { return []string{"s3.amazonaws.com"} }
func (outsideEmulator) Container() extension.EmulatorContainer {
	return extension.EmulatorContainer{
		Image:      "example.invalid/localstack@sha256:" + strings.Repeat("a", 64),
		Port:       4566,
		Maintainer: extension.MaintainerCommercial,
	}
}
