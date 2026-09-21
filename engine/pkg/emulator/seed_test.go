package emulator_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/emulator"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// The seeding tests, and what each one is aimed at.
//
// The planner half is a pure function, so it is tested as one: given a
// declaration, what does the plan SAY it will do and what does it say it will
// not. The runner half is tested against a server that answers exactly what
// the real emulators were measured answering, and the cases that matter are
// the dishonest ones: a create that is answered and never lands, an emulator
// that cannot be asked, and a host nothing routes. Each of those has a shape
// that a weaker implementation reports as success, which is why they are here
// rather than only the happy path.

// recorded is one request the fake emulator received.
type recorded struct {
	Method string
	Host   string
	Path   string
	Query  string
	Header http.Header
	Body   string
}

// fakeEmulator answers the requests a plan sends, from a table keyed by
// method and path, and records everything it was asked.
//
// It answers the shapes MEASURED off the real containers rather than shapes
// invented here, because a fake that answers what the code expects proves the
// code agrees with itself.
type fakeEmulator struct {
	server *httptest.Server
	got    []recorded
	answer func(r recorded) (int, string)
}

func newFakeEmulator(t *testing.T, answer func(r recorded) (int, string)) *fakeEmulator {
	t.Helper()
	f := &fakeEmulator{answer: answer}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		got := recorded{
			Method: req.Method,
			Host:   req.Host,
			Path:   req.URL.Path,
			Query:  req.URL.RawQuery,
			Header: req.Header.Clone(),
			Body:   string(body),
		}
		f.got = append(f.got, got)
		status, answer := f.answer(got)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, answer)
	}))
	t.Cleanup(f.server.Close)
	return f
}

// send is the transport the seeder uses in these tests: a real HTTP request
// carrying the provider's own hostname in the Host header, to the fake.
func (f *fakeEmulator) send() emulator.Send {
	return func(ctx context.Context, host string, r emulator.Request) (emulator.Response, error) {
		url := f.server.URL + r.Path
		if r.Query != "" {
			url += "?" + r.Query
		}
		req, err := http.NewRequestWithContext(ctx, r.Method, url, strings.NewReader(r.Body))
		if err != nil {
			return emulator.Response{}, err
		}
		req.Host = host
		for k, v := range r.Header {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return emulator.Response{}, err
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return emulator.Response{}, err
		}
		return emulator.Response{Status: resp.StatusCode, Body: string(body)}, nil
	}
}

// outcomeFor finds one named outcome in a result.
func outcomeFor(t *testing.T, r emulator.Result, name string) emulator.Outcome {
	t.Helper()
	for _, o := range r.Outcomes {
		if o.Name == name {
			return o
		}
	}
	t.Fatalf("no outcome named %q in %+v", name, r.Outcomes)
	return emulator.Outcome{}
}

// s3Answers is the fake S3, answering what LocalStack was measured answering.
func s3Answers(buckets map[string]bool, versioned map[string]bool) func(recorded) (int, string) {
	return func(r recorded) (int, string) {
		name := strings.TrimPrefix(r.Path, "/")
		switch {
		case r.Method == "PUT" && r.Query == "versioning":
			if r.Header.Get("Content-Type") != "application/xml" {
				// Measured: the container answers 400 without this header, and
				// the bucket is left unversioned.
				return 400, "<Error><Code>MalformedXML</Code></Error>"
			}
			versioned[name] = true
			return 200, ""
		case r.Method == "GET" && r.Query == "versioning":
			if versioned[name] {
				return 200, `<VersioningConfiguration><Status>Enabled</Status>` +
					`</VersioningConfiguration>`
			}
			return 200, `<VersioningConfiguration />`
		case r.Method == "PUT":
			buckets[name] = true
			return 200, ""
		case r.Method == "HEAD":
			if buckets[name] {
				return 200, ""
			}
			return 404, ""
		}
		return 400, "unexpected request"
	}
}

func TestSeeding_ABucketIsCreatedAndReadBackOutOfTheEmulator(t *testing.T) {
	buckets, versioned := map[string]bool{}, map[string]bool{}
	fake := newFakeEmulator(t, s3Answers(buckets, versioned))

	steps := emulator.PlanSeeding([]provider.CloudResource{{
		Type: "aws_s3_bucket", Name: "orders", Attributes: map[string]string{"region": "eu-west-2"},
	}})
	results := emulator.Seeder{Send: fake.send()}.Run(context.Background(), steps)

	require.Len(t, results, 1)
	require.Equal(t, emulator.Reproduced, results[0].Worst())
	require.Equal(t, emulator.Reproduced, outcomeFor(t, results[0], "the bucket").State)
	require.True(t, buckets["orders"], "the bucket was never created in the emulator")

	// The region decides the hostname, and the hostname is what the egress
	// rule has to route. A request that went to the apex would reach an
	// environment that routes only the regional spelling as a refusal.
	require.Equal(t, "s3.eu-west-2.amazonaws.com", results[0].Host)
	for _, got := range fake.got {
		require.Equal(t, "s3.eu-west-2.amazonaws.com", got.Host)
	}
}

func TestSeeding_ACreateThatWasAnsweredIsNotAPassWhenTheReadCannotFindIt(t *testing.T) {
	// The emulator accepts the create and holds nothing, which is the shape of
	// every instrument this repository has caught measuring nothing: a 200 read
	// as evidence that something exists.
	fake := newFakeEmulator(t, func(r recorded) (int, string) {
		if r.Method == "HEAD" {
			return 404, ""
		}
		return 200, ""
	})
	steps := emulator.PlanSeeding([]provider.CloudResource{{Type: "aws_s3_bucket", Name: "orders"}})
	results := emulator.Seeder{Send: fake.send()}.Run(context.Background(), steps)

	got := outcomeFor(t, results[0], "the bucket")
	require.Equal(t, emulator.Absent, got.State,
		"a create answered 200 was read as a bucket that exists")
	require.Contains(t, got.Reason, "404")
	require.Equal(t, emulator.Absent, results[0].Worst())
}

func TestSeeding_AResourceAlreadyInTheTwinIsNotCreatedAgain(t *testing.T) {
	// A second `af up` against a standing environment. DynamoDB answers
	// ResourceInUseException to a create for a table that is there, so a plan
	// that created first and read second would report a failure for a table
	// that is present and correct.
	var creates int
	fake := newFakeEmulator(t, func(r recorded) (int, string) {
		switch r.Header.Get("X-Amz-Target") {
		case "DynamoDB_20120810.CreateTable":
			creates++
			return 400, `{"__type": "ResourceInUseException", "message": "Table already exists"}`
		case "DynamoDB_20120810.DescribeTable":
			return 200, `{"Table": {"TableName": "orders", "AttributeDefinitions": ` +
				`[{"AttributeName": "id", "AttributeType": "S"}]}}`
		}
		return 400, "unexpected"
	})
	steps := emulator.PlanSeeding([]provider.CloudResource{{
		Type: "aws_dynamodb_table", Name: "orders",
		Attributes: map[string]string{"hash_key": "id"},
	}})
	results := emulator.Seeder{Send: fake.send()}.Run(context.Background(), steps)

	require.Equal(t, emulator.Reproduced, results[0].Worst())
	require.Zero(t, creates, "the table was created again although the read had already found it")
}

func TestSeeding_AnEmulatorThatCannotBeAskedIsUnmeasuredRatherThanAbsent(t *testing.T) {
	// The distinction the whole report rests on. "I looked and it is not
	// there" and "I could not look" are different facts, and an implementation
	// that folded the second into the first would report a twin as broken
	// whenever the transport was.
	failing := func(ctx context.Context, host string, r emulator.Request) (emulator.Response, error) {
		return emulator.Response{}, fmt.Errorf("dial tcp: connection refused")
	}
	steps := emulator.PlanSeeding([]provider.CloudResource{{Type: "aws_s3_bucket", Name: "orders"}})
	results := emulator.Seeder{Send: failing}.Run(context.Background(), steps)

	got := outcomeFor(t, results[0], "the bucket")
	require.Equal(t, emulator.Unmeasured, got.State)
	require.Contains(t, got.Reason, "connection refused")
	require.False(t, got.State.Measured())
}

func TestSeeding_AHostTheEnvironmentDoesNotRouteIsRefusedRatherThanAttempted(t *testing.T) {
	fake := newFakeEmulator(t, func(recorded) (int, string) { return 200, "" })
	steps := emulator.PlanSeeding([]provider.CloudResource{{Type: "aws_s3_bucket", Name: "orders"}})
	results := emulator.Seeder{
		Send:   fake.send(),
		Routes: func(string, string) bool { return false },
	}.Run(context.Background(), steps)

	got := outcomeFor(t, results[0], "orders")
	require.Equal(t, emulator.Refused, got.State)
	require.Contains(t, got.Reason, "s3.us-east-1.amazonaws.com")
	require.Empty(t, fake.got, "a request was sent to a host the environment does not route")
}

func TestSeeding_AnAttributeNobodyReproducesIsNamedWithTheReason(t *testing.T) {
	// The property this lane exists for. The accounting is by subtraction, so
	// an attribute nobody thought about is reported rather than dropped.
	steps := emulator.PlanSeeding([]provider.CloudResource{{
		Type: "aws_s3_bucket", Name: "orders",
		Attributes: map[string]string{
			"lifecycle_rule":            "expire-after-30-days",
			"object_lock_configuration": "GOVERNANCE",
		},
	}})
	require.Len(t, steps, 1)

	// Decided at plan time, before anything ran, which is what makes the plan
	// inspectable rather than only the result.
	byName := map[string]emulator.Outcome{}
	for _, o := range steps[0].Outcomes {
		byName[o.Name] = o
	}
	require.Contains(t, byName, "lifecycle_rule")
	require.Equal(t, emulator.Unmeasured, byName["lifecycle_rule"].State)
	require.Contains(t, byName["lifecycle_rule"].Reason, "never expires")
	require.Contains(t, byName["lifecycle_rule"].Reason, "expire-after-30-days",
		"the reason does not say what production declares")

	// The one nothing has a specific reason for is still named, with the
	// general sentence, rather than being silently absent.
	require.Contains(t, byName, "object_lock_configuration")
	require.Equal(t, emulator.Unmeasured, byName["object_lock_configuration"].State)
	require.NotEmpty(t, byName["object_lock_configuration"].Reason)
}

func TestSeeding_AResourceTypeNothingCreatesIsReportedRatherThanSkipped(t *testing.T) {
	steps := emulator.PlanSeeding([]provider.CloudResource{{
		Type: "aws_elasticache_cluster", Name: "sessions",
	}})
	require.Len(t, steps, 1)
	require.Empty(t, steps[0].Actions)

	results := emulator.Seeder{Send: nil}.Run(context.Background(), steps)
	got := outcomeFor(t, results[0], "sessions")
	require.Equal(t, emulator.Unmeasured, got.State)
	require.Contains(t, got.Reason, "aws_elasticache_cluster")
}

func TestSeeding_NothingIsReportedWhenEverythingWasReproduced(t *testing.T) {
	// The other half of the honesty, and the one that is easy to forget: a
	// report that always prints a caveat is a report nobody reads. A resource
	// with nothing unreproduced costs exactly one line and names no reason.
	buckets, versioned := map[string]bool{}, map[string]bool{}
	fake := newFakeEmulator(t, s3Answers(buckets, versioned))
	steps := emulator.PlanSeeding([]provider.CloudResource{{
		Type: "aws_s3_bucket", Name: "orders",
		Attributes: map[string]string{"versioning": "Enabled"},
	}})
	results := emulator.Seeder{Send: fake.send()}.Run(context.Background(), steps)

	require.Equal(t, emulator.Reproduced, results[0].Worst())
	require.True(t, versioned["orders"], "versioning was reported without being set")
	lines := results[0].Lines()
	require.Len(t, lines, 1, "a fully reproduced resource printed a caveat: %q", lines)
	require.Contains(t, lines[0], "reproduced")
}

func TestSeeding_AVersionedBucketWhoseVersioningDidNotLandIsNotReportedReproduced(t *testing.T) {
	// The bucket is there and the attribute is not, which is precisely the
	// twin that looks right and is not. The resource's verdict is the weakest
	// part of it.
	buckets := map[string]bool{}
	fake := newFakeEmulator(t, func(r recorded) (int, string) {
		name := strings.TrimPrefix(r.Path, "/")
		switch {
		case r.Query == "versioning" && r.Method == "PUT":
			return 400, "<Error><Code>MalformedXML</Code></Error>"
		case r.Query == "versioning":
			return 200, `<VersioningConfiguration />`
		case r.Method == "PUT":
			buckets[name] = true
			return 200, ""
		case r.Method == "HEAD":
			return 200, ""
		}
		return 400, ""
	})
	steps := emulator.PlanSeeding([]provider.CloudResource{{
		Type: "aws_s3_bucket", Name: "orders",
		Attributes: map[string]string{"versioning": "Enabled"},
	}})
	results := emulator.Seeder{Send: fake.send()}.Run(context.Background(), steps)

	require.Equal(t, emulator.Reproduced, outcomeFor(t, results[0], "the bucket").State)
	require.Equal(t, emulator.Absent, outcomeFor(t, results[0], "versioning").State)
	require.Equal(t, emulator.Absent, results[0].Worst(),
		"a bucket without the versioning it declares was reported reproduced")
	require.Contains(t, outcomeFor(t, results[0], "versioning").Reason, "MalformedXML",
		"the reason does not carry what the emulator said about the create")
}

func TestSeeding_TheQueueURLComesFromTheEmulatorRatherThanBeingComposed(t *testing.T) {
	// Composing it would mean writing LocalStack's own account number into
	// this build, which is wrong the day somebody registers another emulator.
	const queueURL = "http://sqs.eu-west-2.amazonaws.com/123456789012/orders"
	fake := newFakeEmulator(t, func(r recorded) (int, string) {
		switch r.Header.Get("X-Amz-Target") {
		case "AmazonSQS.CreateQueue":
			return 200, fmt.Sprintf(`{"QueueUrl": %q}`, queueURL)
		case "AmazonSQS.GetQueueUrl":
			return 200, fmt.Sprintf(`{"QueueUrl": %q}`, queueURL)
		case "AmazonSQS.GetQueueAttributes":
			if !strings.Contains(r.Body, queueURL) {
				return 400, `{"__type": "InvalidAddress"}`
			}
			return 200, `{"Attributes": {"VisibilityTimeout": "45"}}`
		}
		return 400, "unexpected"
	})
	steps := emulator.PlanSeeding([]provider.CloudResource{{
		Type: "aws_sqs_queue", Name: "orders",
		Attributes: map[string]string{"region": "eu-west-2", "visibility_timeout_seconds": "45"},
	}})
	results := emulator.Seeder{Send: fake.send()}.Run(context.Background(), steps)

	require.Equal(t, emulator.Reproduced, results[0].Worst())
	require.Equal(t, emulator.Reproduced,
		outcomeFor(t, results[0], "visibility_timeout_seconds").State)
}

func TestSeeding_ASecretHoldsAPlaceholderAndSaysSo(t *testing.T) {
	// Production's value must never be copied into a container a third party
	// image runs. So the secret exists, the application finds it, and the
	// result says substituted rather than reproduced: reading "the secret is
	// in the twin" as "the secret says what production says" is the single
	// most dangerous sentence this package could produce.
	var created bool
	fake := newFakeEmulator(t, func(r recorded) (int, string) {
		switch r.Header.Get("X-Amz-Target") {
		case "secretsmanager.CreateSecret":
			created = true
			return 200, `{"Name": "prod/db/password"}`
		case "secretsmanager.DescribeSecret":
			if !created {
				return 400, `{"__type": "ResourceNotFoundException"}`
			}
			return 200, `{"Name": "prod/db/password"}`
		}
		return 400, "unexpected"
	})
	steps := emulator.PlanSeeding([]provider.CloudResource{{
		Type: "aws_secretsmanager_secret", Name: "prod/db/password",
	}})
	results := emulator.Seeder{Send: fake.send()}.Run(context.Background(), steps)

	got := outcomeFor(t, results[0], "the secret")
	require.Equal(t, emulator.Substituted, got.State)
	require.Contains(t, got.Reason, "never production")
	var carriedPlaceholder bool
	for _, sent := range fake.got {
		if sent.Header.Get("X-Amz-Target") == "secretsmanager.CreateSecret" {
			require.Contains(t, sent.Body, "placeholder")
			require.NotContains(t, sent.Body, "prod-password-value")
			carriedPlaceholder = true
		}
	}
	require.True(t, carriedPlaceholder, "the secret was never created, so nothing was measured")
}

func TestSeeding_ASecureStringParameterSaysItIsNotOne(t *testing.T) {
	// The KMS gap, surfaced rather than smoothed over. The parameter is
	// created as a plain String, because nothing in the surface can decrypt a
	// SecureString, and that is a real difference between the twin and
	// production.
	steps := emulator.PlanSeeding([]provider.CloudResource{{
		Type: "aws_ssm_parameter", Name: "/prod/db/url",
		Attributes: map[string]string{"type": "SecureString"},
	}})
	require.Len(t, steps, 1)
	var found bool
	for _, o := range steps[0].Outcomes {
		if o.Name == "type" {
			found = true
			require.Equal(t, emulator.Unmeasured, o.State)
			require.Contains(t, o.Reason, "KMS")
		}
	}
	require.True(t, found, "a SecureString parameter was created as a String and nothing said so")
}

func TestSeeding_ATableWithNoHashKeySendsNothingAndSaysWhy(t *testing.T) {
	// Sending the create anyway would put the emulator's refusal in front of
	// somebody as a broken twin, when what is missing is in the declaration.
	fake := newFakeEmulator(t, func(recorded) (int, string) { return 200, "" })
	steps := emulator.PlanSeeding([]provider.CloudResource{{
		Type: "aws_dynamodb_table", Name: "orders",
	}})
	require.Empty(t, steps[0].Actions)
	results := emulator.Seeder{Send: fake.send()}.Run(context.Background(), steps)

	got := outcomeFor(t, results[0], "orders")
	require.Equal(t, emulator.Unmeasured, got.State)
	require.Contains(t, got.Reason, "hash key")
	require.Empty(t, fake.got, "a request was sent for a table that cannot be created")
}

func TestSeeding_AzureIsReportedUnseededRatherThanUnknown(t *testing.T) {
	// The emulator IS here and the seeding is not, and those are different
	// sentences. A reader told that no emulator answers for Azure Blob Storage
	// would go looking for a missing emulator that is running.
	steps := emulator.PlanSeeding([]provider.CloudResource{{
		Type: "azurerm_storage_container", Name: "uploads",
	}})
	results := emulator.Seeder{Send: nil}.Run(context.Background(), steps)

	require.Equal(t, emulator.AzureBlobName, results[0].Emulator)
	got := outcomeFor(t, results[0], "uploads")
	require.Equal(t, emulator.Unmeasured, got.State)
	require.Contains(t, got.Reason, "403")
	require.NotContains(t, got.Reason, "no emulator in this build")
}

func TestSeeding_EverySeedPlannerNamesAnEmulatorThisBuildRegisters(t *testing.T) {
	// A planner naming an emulator nothing registers would send its requests
	// to a host the environment has nothing to route to, and the result would
	// be a refusal that reads as the user's missing egress rule.
	require.NotEmpty(t, emulator.SeedTypes())
	for _, resourceType := range emulator.SeedTypes() {
		steps := emulator.PlanSeeding([]provider.CloudResource{{
			Type: resourceType, Name: "probe", Attributes: map[string]string{"hash_key": "id"},
		}})
		require.Len(t, steps, 1)
		name := steps[0].Emulator
		require.NotEmpty(t, name, "%s names no emulator", resourceType)
		_, ok := emulator.Named(name)
		require.True(t, ok, "%s names the emulator %q, which this build does not register",
			resourceType, name)
	}
}

func TestSeeding_EveryCandidateHostBelongsToTheEmulatorsOwnSurface(t *testing.T) {
	// The seeding and the routing have to agree about what the emulator
	// answers for. A plan sending a bucket to a host the emulator's surface
	// table does not claim would be a request the sidecar refuses, reported as
	// the environment's policy being wrong when the plan was.
	for _, resourceType := range emulator.SeedTypes() {
		steps := emulator.PlanSeeding([]provider.CloudResource{{
			Type: resourceType, Name: "probe",
			Attributes: map[string]string{"hash_key": "id", "region": "eu-west-2"},
		}})
		em, ok := emulator.Named(steps[0].Emulator)
		require.True(t, ok)
		for _, host := range steps[0].Hosts {
			_, covered := em.ServiceFor(host)
			require.True(t, covered,
				"%s would send a request to %s, which the %s emulator does not answer for",
				resourceType, host, steps[0].Emulator)
		}
	}
}

func TestSeeding_AHostRoutedToADifferentEmulatorIsNotUsed(t *testing.T) {
	// Routing is two questions and not one. A build that asked only "is this
	// host routed" would create a bucket inside whatever other emulator
	// answers for it, and report the bucket reproduced.
	fake := newFakeEmulator(t, func(recorded) (int, string) { return 200, "" })
	steps := emulator.PlanSeeding([]provider.CloudResource{{Type: "aws_s3_bucket", Name: "orders"}})
	results := emulator.Seeder{
		Send: fake.send(),
		Routes: func(emulatorName, host string) bool {
			return emulatorName == "somebody-elses-emulator"
		},
	}.Run(context.Background(), steps)

	require.Equal(t, emulator.Refused, outcomeFor(t, results[0], "orders").State)
	require.Empty(t, fake.got, "a bucket was created in an emulator that answers for something else")
}

func TestSeeding_OnePlanIsTheSamePlanTwice(t *testing.T) {
	// A request assembled from a Go map is assembled in whatever order the map
	// iterated, so two runs of one declaration would send two different
	// bodies. The one that matters is the secret's idempotency token, which
	// this build derives from the name for exactly this reason: a token that
	// moved would make a second `af up` create a second version of a secret
	// nobody asked for.
	decl := []provider.CloudResource{
		{Type: "aws_sqs_queue", Name: "orders", Attributes: map[string]string{
			"visibility_timeout_seconds": "45", "delay_seconds": "5",
			"message_retention_seconds": "600", "fifo_queue": "true",
		}},
		{Type: "aws_secretsmanager_secret", Name: "prod/db/password"},
	}
	// Twenty rather than two, because the defect this is aimed at is a map
	// iterated without being sorted, and Go randomises that per iteration. Two
	// plans agree by chance roughly one time in twenty four with the four
	// attributes above, so a comparison of two would report a build that
	// assembles its requests in a random order as deterministic about once
	// every twenty four runs, and pass.
	first := emulator.PlanSeeding(decl)
	for i := 0; i < 20; i++ {
		require.Equal(t, first, emulator.PlanSeeding(decl),
			"one declaration produced two different plans, on attempt %d", i)
	}
}

func TestSeeding_NoPlannerGivesAReasonForAnAttributeItAlreadyReproduces(t *testing.T) {
	// A reason for a consumed key can never print, because the accounting is
	// by subtraction and a consumed key is subtracted. It would sit in the
	// source looking like coverage of a gap that is not a gap, and the first
	// person to widen the planner would read it as still true.
	for _, resourceType := range emulator.SeedTypes() {
		steps := emulator.PlanSeeding([]provider.CloudResource{{
			Type: resourceType, Name: "probe",
			Attributes: map[string]string{"hash_key": "id", "topic": "t"},
		}})
		consumed := map[string]bool{}
		for _, k := range steps[0].Consumed {
			consumed[k] = true
		}
		for key := range steps[0].Reasons {
			require.False(t, consumed[key],
				"%s carries a reason for %q and also reproduces it, so the reason can never "+
					"be printed", resourceType, key)
		}
	}
}
