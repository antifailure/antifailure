package local_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/emulator"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The number this lane owes, measured the only way it can be.
//
// The claim is not "a bucket exists in LocalStack". It is "an application that
// reads its own bucket on startup finds it". So the subject here is an
// UNMODIFIED application, identical between two runs, addressing
// s3.amazonaws.com with no endpoint override, and the only difference between
// the two environments is whether the bucket production declares was declared
// to `af up`. One run reads the bucket and one reads NoSuchBucket, and the
// application is byte for byte the same in both.
//
// That control arm is what makes the measurement mean anything. Without it a
// bucket that arrived from somewhere else, or a body that came from a cached
// answer, would read exactly like a bucket this created, and every one of the
// instruments this repository has caught measuring nothing had that shape.
//
// It runs against the REAL LocalStack this build pins, not a stand in. What is
// being proved is that the requests in pkg/emulator are the requests that
// container answers, and a fake that answered what those requests expect would
// prove only that the code agrees with itself.

// seededBucket is what production declares and the twin should therefore hold.
const seededBucket = "af-seed-orders"

// theBucketReader is the unmodified application, built once and used in both
// environments.
//
// It asks for the provider's own hostname. There is no AWS_ENDPOINT_URL, no
// base URL variable, and no client built one way for tests. GET on a bucket is
// a listing, so the answer says which of the two outcomes happened in the body
// rather than only in a status code.
func theBucketReader() provider.ServiceSpec {
	return provider.ServiceSpec{
		Name: "app", Image: proberImage, Kind: "worker",
		Command: "wget -T 20 -q -O - http://s3.amazonaws.com/" + seededBucket +
			" 2>&1 | tr -d '\\n'; echo; echo AF-APP-DONE; sleep 240\n",
	}
}

// awsEmulatorSpec is the AWS emulator this build declares, as the runtime
// takes it.
//
// Read out of the registration rather than written here, so this test runs
// against the image, the digest and the variables an `af up` actually starts.
// A spec written out in the test would keep passing the day the registration
// changed.
func awsEmulatorSpec(t *testing.T) provider.EmulatorSpec {
	t.Helper()
	em, ok := emulator.Named(emulator.AWSName)
	require.True(t, ok, "this build does not register the aws emulator")
	c := em.Container()
	return provider.EmulatorSpec{
		Name: em.Name(), Image: c.Image, Port: c.Port, Env: c.Env, Command: c.Command,
	}
}

// s3OnlyEgress routes the apex and nothing else, which is also the narrowest
// policy that can reproduce a bucket at all.
func s3OnlyEgress() *schema.Egress {
	return &schema.Egress{
		Default: schema.ModeBlock,
		Rules: []schema.EgressRule{
			{Host: "s3.amazonaws.com", Mode: schema.ModeEmulate, Emulator: emulator.AWSName},
		},
	}
}

func TestSeed_TheApplicationFindsTheBucketProductionDeclaresAndNotOneItDoesNot(t *testing.T) {
	r := requireRuntime(t)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()

	// THE CONTROL, first and torn down before the second, so that two
	// LocalStack containers are never resident at once on a machine whose
	// daemon has been killed for memory before.
	withoutApp := theBucketReader()
	withoutID := envID(t, r, "seedwithout")
	_, err := r.Up(ctx, provider.EnvSpec{
		EnvID:     withoutID,
		Egress:    s3OnlyEgress(),
		Emulators: []provider.EmulatorSpec{awsEmulatorSpec(t)},
		Services:  []provider.ServiceSpec{withoutApp},
		// No CloudResources. This is the state of every environment this
		// product brought up before this lane: the emulator is running, the
		// route works, and the bucket is not there.
	})
	require.NoError(t, err)
	withoutOut := waitForAppOutput(t, ctx, r, withoutID)
	// The status rather than the emulator's own words, because busybox wget
	// DISCARDS the body on a non-200: the sidecar's decision log records the
	// 231 byte NoSuchBucket document the emulator returned, and the
	// application never sees it. Asserting on the body here would be
	// asserting on something this application cannot print.
	require.Contains(t, withoutOut, "404",
		"an environment that declared no bucket had one anyway, so the second run proves "+
			"nothing. The application said:\n%s\nsidecar:\n%s",
		withoutOut, sidecarLog(t, ctx, r, withoutID))
	require.NotContains(t, withoutOut, "<Name>"+seededBucket+"</Name>")
	_, err = r.Down(ctx, withoutID)
	require.NoError(t, err)

	// THE SAME APPLICATION, one declaration different.
	var progress []string
	withApp := theBucketReader()
	withID := envID(t, r, "seedwith")
	_, err = r.Up(ctx, provider.EnvSpec{
		EnvID:     withID,
		Egress:    s3OnlyEgress(),
		Emulators: []provider.EmulatorSpec{awsEmulatorSpec(t)},
		Services:  []provider.ServiceSpec{withApp},
		CloudResources: []provider.CloudResource{{
			Type: "aws_s3_bucket", Name: seededBucket,
			Attributes: map[string]string{
				"versioning": "Enabled",
				// Declared so that the run has something it cannot reproduce,
				// which is the other half of the property: the report has to
				// name it rather than pass over it.
				"lifecycle_rule": "expire-after-30-days",
			},
		}},
		Progress: func(line string) { progress = append(progress, line) },
	})
	require.NoError(t, err)

	withOut := waitForAppOutput(t, ctx, r, withID)
	require.Contains(t, withOut, "<Name>"+seededBucket+"</Name>",
		"the application did not find the bucket its own declaration asked for. It is "+
			"identical in both runs, so the difference is the declaration.\napplication:\n%s\n"+
			"seeding said:\n%s\nsidecar:\n%s",
		withOut, strings.Join(progress, "\n"), sidecarLog(t, ctx, r, withID))
	require.NotContains(t, withOut, "404")

	// THE MEASUREMENT. Everything the application is, compared between the run
	// that found its bucket and the run that did not.
	require.Equal(t, withoutApp, withApp,
		"the application differs between the two runs, so what was measured is not the "+
			"declaration")
	require.Empty(t, withApp.Env,
		"the application is given %d variables; it needs none to find its own bucket",
		len(withApp.Env))

	// THE REPORT, which is the other deliverable. The bucket reproduced, the
	// attribute nobody reproduces named with its reason, and neither of them
	// silent.
	report := strings.Join(progress, "\n")
	require.Contains(t, report, seededBucket)
	require.Contains(t, report, "lifecycle_rule is unmeasured",
		"an attribute the twin does not carry was not reported. The seeding said:\n%s", report)
	require.Contains(t, report, "never expires")
	require.NotContains(t, report, "versioning is unmeasured")
	require.NotContains(t, report, "versioning is absent")

	_, err = r.Down(ctx, withID)
	require.NoError(t, err)
}

func TestSeed_AResourceTheEnvironmentDoesNotRouteIsReportedRatherThanCreatedElsewhere(t *testing.T) {
	r := requireRuntime(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	// An environment that emulates S3 and nothing else, handed a queue. The
	// SQS hostname is not routed, so the seeding must say so rather than
	// sending a CreateQueue somewhere. A build that sent it anyway would be
	// creating a queue inside whatever answers for that host, and reporting
	// success for a queue the application will be refused at.
	var progress []string
	id := envID(t, r, "seedunrouted")
	_, err := r.Up(ctx, provider.EnvSpec{
		EnvID:     id,
		Egress:    s3OnlyEgress(),
		Emulators: []provider.EmulatorSpec{awsEmulatorSpec(t)},
		CloudResources: []provider.CloudResource{
			{Type: "aws_s3_bucket", Name: seededBucket},
			{Type: "aws_sqs_queue", Name: "af-seed-jobs"},
		},
		Progress: func(line string) { progress = append(progress, line) },
	})
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = r.Down(context.WithoutCancel(ctx), id) })

	report := strings.Join(progress, "\n")
	require.Contains(t, report, "af-seed-jobs is refused",
		"the unrouted queue was not reported. The seeding said:\n%s", report)
	require.Contains(t, report, "sqs.us-east-1.amazonaws.com")
	require.Contains(t, report, `bucket "`+seededBucket+`" is reproduced`,
		"the bucket that IS routed was not reproduced, so the refusal above may be "+
			"reporting a broken environment rather than a policy. The seeding said:\n%s", report)
	require.Contains(t, report, "1 of 2 declared cloud resources are reproduced")
}
