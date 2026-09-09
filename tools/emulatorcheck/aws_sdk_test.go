package emulatorcheck_test

// The application under test. Everything in this file is written the way an
// application is written: the SDK is constructed from the default
// configuration chain and nothing tells it where to send a request. If any
// line here named an endpoint, the claim this lane exists to make would be
// false, and the count the lane publishes is the count of such lines, which is
// zero.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	eventbridgetypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
	"github.com/aws/aws-sdk-go-v2/service/kinesis"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/tools/emulatorcheck"
)

// AWS's own published example credentials, which authenticate nothing and are
// what a documentation page hands a reader who wants an SDK to construct a
// request without holding an account.
//
// Assembled rather than written out. `tools/scanrepo` runs `livekey.Scan`,
// which recognises the SHAPE of an access key ID and cannot know that this
// particular one is a documentation value, so a written-out literal turns the
// required context `no credentials in the tree` red for a tree that carries no
// credential. `engine/pkg/livekey`'s package doc says this in as many words,
// and `ee/engine/cloudauth/aws_test.go` already writes the same value the same
// way.
const (
	exampleAccessKeyID     = "AKIA" + "IOSFODNN7EXAMPLE"
	exampleSecretAccessKey = "wJalrXUtnFEMI/K7MDENG/bPxRfiCY" + "EXAMPLEKEY"
)

// runSDK gates the suite. It is an environment variable rather than a build
// tag so that this file is compiled by the ordinary tools test run and a
// change that breaks it fails somewhere, rather than rotting behind a tag
// nobody sets.
const runSDK = "AF_EMULATOR_SDK"

// harness is the emulator, the sidecar stand in, and the environment an
// application would be given. It is built once for the whole file: starting
// LocalStack costs the better part of a minute on an idle machine and a great
// deal more on a busy one, and nine services do not each need their own.
type harness struct {
	container *emulatorcheck.Container
	sidecar   *emulatorcheck.Sidecar
	cfg       aws.Config
}

var live *harness

func TestMain(m *testing.M) {
	if os.Getenv(runSDK) == "" {
		fmt.Fprintf(os.Stderr,
			"emulatorcheck: %s is not set, so the SDK suite did not run. "+
				"It is a separate step because it starts a container.\n", runSDK)
		os.Exit(0)
	}
	ok, err := emulatorcheck.DockerAvailable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "emulatorcheck: %v\n", err)
		os.Exit(1)
	}
	if !ok {
		fmt.Fprintln(os.Stderr, "emulatorcheck: no docker daemon, so the SDK suite did not run")
		os.Exit(0)
	}

	code, err := run(m)
	if err != nil {
		fmt.Fprintf(os.Stderr, "emulatorcheck: %v\n", err)
		os.Exit(1)
	}
	os.Exit(code)
}

func run(m *testing.M) (int, error) {
	port := 45699
	if p := os.Getenv("AF_EMULATOR_PORT"); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil {
			return 1, fmt.Errorf("AF_EMULATOR_PORT is not a number: %w", err)
		}
		port = n
	}

	container, err := emulatorcheck.StartAWS(port)
	if err != nil {
		return 1, err
	}
	defer container.Stop()

	sidecar, err := emulatorcheck.NewSidecar(container.Address)
	if err != nil {
		return 1, err
	}
	defer sidecar.Close()

	bundle, err := sidecar.CABundlePath()
	if err != nil {
		return 1, err
	}

	// THE ENVIRONMENT, not the application. Every one of these is something an
	// environment sets around a process. In a real environment the authority
	// is in the image's trust store and even AWS_CA_BUNDLE is unnecessary, and
	// the credentials are whatever the application already had, because the
	// emulator verifies no signature and the sidecar refuses a live key.
	for k, v := range map[string]string{
		"HTTPS_PROXY":           sidecar.ProxyURL(),
		"HTTP_PROXY":            sidecar.ProxyURL(),
		"AWS_CA_BUNDLE":         bundle,
		"AWS_REGION":            "us-east-1",
		"AWS_ACCESS_KEY_ID":     exampleAccessKeyID,
		"AWS_SECRET_ACCESS_KEY": exampleSecretAccessKey,
	} {
		if err := os.Setenv(k, v); err != nil {
			return 1, err
		}
	}

	// The application's whole configuration. No endpoint, no resolver, no
	// custom transport.
	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		return 1, err
	}

	live = &harness{container: container, sidecar: sidecar, cfg: cfg}
	fmt.Printf("emulatorcheck: emulator ready in %s, %d bytes of memory\n",
		container.Started.Round(time.Second), container.PeakMemory)
	return m.Run(), nil
}

func ctx(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	t.Cleanup(cancel)
	return c
}

// STS first, and not for tidiness. Several credential chains call it before
// the first real request, so an emulated surface without it fails at startup
// with an error naming the credential chain rather than the service.
func TestSTS_AnswersTheCallSDKsMakeBeforeTheirFirstRealRequest(t *testing.T) {
	c := sts.NewFromConfig(live.cfg)
	out, err := c.GetCallerIdentity(ctx(t), &sts.GetCallerIdentityInput{})
	require.NoError(t, err)
	require.NotEmpty(t, aws.ToString(out.Account))
	require.Contains(t, aws.ToString(out.Arn), "arn:aws:")
	requireReached(t, "sts.amazonaws.com", "sts.us-east-1.amazonaws.com")
}

func TestS3_WritesAndReadsAnObjectThroughVirtualHostedAddressing(t *testing.T) {
	c := s3.NewFromConfig(live.cfg)
	bucket := "af-emulatorcheck-bucket"

	_, err := c.CreateBucket(ctx(t), &s3.CreateBucketInput{Bucket: aws.String(bucket)})
	require.NoError(t, err)

	_, err = c.PutObject(ctx(t), &s3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String("receipt.txt"),
		Body:   bytes.NewReader([]byte("the twin wrote this")),
	})
	require.NoError(t, err)

	got, err := c.GetObject(ctx(t), &s3.GetObjectInput{
		Bucket: aws.String(bucket), Key: aws.String("receipt.txt"),
	})
	require.NoError(t, err)
	defer func() { _ = got.Body.Close() }()
	body, err := io.ReadAll(got.Body)
	require.NoError(t, err)
	require.Equal(t, "the twin wrote this", string(body))

	// The headline. The SDK addressed the bucket by name in the HOST header,
	// which is what virtual hosted addressing is, and the sidecar carried that
	// header through untouched. Rewriting it would have destroyed the bucket
	// name, and the request would have arrived asking for an object in no
	// bucket at all.
	var virtual bool
	for _, o := range live.sidecar.Observed() {
		if strings.HasPrefix(o.Host, bucket+".s3.") && o.Emulated {
			virtual = true
			require.Equal(t, "Amazon S3", o.Service)
		}
	}
	require.True(t, virtual,
		"no request carried the bucket in the Host header, so virtual hosted "+
			"addressing was not exercised and the case this lane is loudest about is unproved")
}

func TestSQS_SendsAndReceivesAMessage(t *testing.T) {
	c := sqs.NewFromConfig(live.cfg)
	created, err := c.CreateQueue(ctx(t), &sqs.CreateQueueInput{QueueName: aws.String("orders")})
	require.NoError(t, err)

	_, err = c.SendMessage(ctx(t), &sqs.SendMessageInput{
		QueueUrl: created.QueueUrl, MessageBody: aws.String("order 41"),
	})
	require.NoError(t, err)

	got, err := c.ReceiveMessage(ctx(t), &sqs.ReceiveMessageInput{
		QueueUrl: created.QueueUrl, MaxNumberOfMessages: 1, WaitTimeSeconds: 2,
	})
	require.NoError(t, err)
	require.Len(t, got.Messages, 1)
	require.Equal(t, "order 41", aws.ToString(got.Messages[0].Body))
}

func TestSNS_PublishesToATopic(t *testing.T) {
	c := sns.NewFromConfig(live.cfg)
	topic, err := c.CreateTopic(ctx(t), &sns.CreateTopicInput{Name: aws.String("alerts")})
	require.NoError(t, err)

	out, err := c.Publish(ctx(t), &sns.PublishInput{
		TopicArn: topic.TopicArn, Message: aws.String("the twin published this"),
	})
	require.NoError(t, err)
	require.NotEmpty(t, aws.ToString(out.MessageId))
}

func TestDynamoDB_WritesAndReadsAnItem(t *testing.T) {
	c := dynamodb.NewFromConfig(live.cfg)
	_, err := c.CreateTable(ctx(t), &dynamodb.CreateTableInput{
		TableName: aws.String("carts"),
		AttributeDefinitions: []dynamodbtypes.AttributeDefinition{{
			AttributeName: aws.String("id"), AttributeType: dynamodbtypes.ScalarAttributeTypeS,
		}},
		KeySchema: []dynamodbtypes.KeySchemaElement{{
			AttributeName: aws.String("id"), KeyType: dynamodbtypes.KeyTypeHash,
		}},
		BillingMode: dynamodbtypes.BillingModePayPerRequest,
	})
	require.NoError(t, err)
	require.NoError(t, dynamodb.NewTableExistsWaiter(c).Wait(ctx(t),
		&dynamodb.DescribeTableInput{TableName: aws.String("carts")}, 2*time.Minute))

	_, err = c.PutItem(ctx(t), &dynamodb.PutItemInput{
		TableName: aws.String("carts"),
		Item: map[string]dynamodbtypes.AttributeValue{
			"id":    &dynamodbtypes.AttributeValueMemberS{Value: "cart-1"},
			"total": &dynamodbtypes.AttributeValueMemberN{Value: "4100"},
		},
	})
	require.NoError(t, err)

	got, err := c.GetItem(ctx(t), &dynamodb.GetItemInput{
		TableName: aws.String("carts"),
		Key: map[string]dynamodbtypes.AttributeValue{
			"id": &dynamodbtypes.AttributeValueMemberS{Value: "cart-1"},
		},
	})
	require.NoError(t, err)
	total, ok := got.Item["total"].(*dynamodbtypes.AttributeValueMemberN)
	require.True(t, ok, "the item came back without the attribute it was written with")
	require.Equal(t, "4100", total.Value)
}

func TestKinesis_PutsARecordOnAStream(t *testing.T) {
	c := kinesis.NewFromConfig(live.cfg)
	_, err := c.CreateStream(ctx(t), &kinesis.CreateStreamInput{
		StreamName: aws.String("clicks"), ShardCount: aws.Int32(1),
	})
	require.NoError(t, err)
	require.NoError(t, kinesis.NewStreamExistsWaiter(c).Wait(ctx(t),
		&kinesis.DescribeStreamInput{StreamName: aws.String("clicks")}, 2*time.Minute))

	out, err := c.PutRecord(ctx(t), &kinesis.PutRecordInput{
		StreamName:   aws.String("clicks"),
		PartitionKey: aws.String("one"),
		Data:         []byte("a click"),
	})
	require.NoError(t, err)
	require.NotEmpty(t, aws.ToString(out.SequenceNumber))
}

func TestEventBridge_PutsAnEventOnTheBus(t *testing.T) {
	c := eventbridge.NewFromConfig(live.cfg)
	_, err := c.PutRule(ctx(t), &eventbridge.PutRuleInput{
		Name:         aws.String("on-order"),
		EventPattern: aws.String(`{"source":["shop"]}`),
	})
	require.NoError(t, err)

	out, err := c.PutEvents(ctx(t), &eventbridge.PutEventsInput{
		Entries: []eventbridgetypes.PutEventsRequestEntry{{
			Source:     aws.String("shop"),
			DetailType: aws.String("order.placed"),
			Detail:     aws.String(`{"id":"order-41"}`),
		}},
	})
	require.NoError(t, err)
	require.Equal(t, int32(0), out.FailedEntryCount)
}

func TestSecretsManager_ReadsBackASecretItWrote(t *testing.T) {
	c := secretsmanager.NewFromConfig(live.cfg)
	_, err := c.CreateSecret(ctx(t), &secretsmanager.CreateSecretInput{
		Name: aws.String("checkout/session"), SecretString: aws.String("not-a-real-secret"),
	})
	require.NoError(t, err)

	got, err := c.GetSecretValue(ctx(t), &secretsmanager.GetSecretValueInput{
		SecretId: aws.String("checkout/session"),
	})
	require.NoError(t, err)
	require.Equal(t, "not-a-real-secret", aws.ToString(got.SecretString))
}

func TestSSM_ReadsBackAParameterItWrote(t *testing.T) {
	c := ssm.NewFromConfig(live.cfg)
	_, err := c.PutParameter(ctx(t), &ssm.PutParameterInput{
		Name: aws.String("/shop/feature-flag"), Value: aws.String("on"), Type: "String",
	})
	require.NoError(t, err)

	got, err := c.GetParameter(ctx(t), &ssm.GetParameterInput{
		Name: aws.String("/shop/feature-flag"),
	})
	require.NoError(t, err)
	require.Equal(t, "on", aws.ToString(got.Parameter.Value))
}

// The half that decides whether any of the above is worth anything. An
// emulator that answers everything is as useless as one that refuses
// everything, and a service outside the declared surface has to be refused in
// a shape the SDK's own error handling understands.
func TestLambda_IsOutsideTheSurfaceAndIsRefusedInAWSsOwnErrorShape(t *testing.T) {
	c := lambda.NewFromConfig(live.cfg)
	_, err := c.ListFunctions(ctx(t), &lambda.ListFunctionsInput{})
	require.Error(t, err, "Lambda is outside the surface and must not be answered")

	var api smithy.APIError
	require.True(t, errors.As(err, &api),
		"the refusal did not arrive as an API error the SDK could parse: %v", err)
	require.Equal(t, "AccessDenied", api.ErrorCode())

	for _, o := range live.sidecar.Observed() {
		if strings.HasPrefix(o.Host, "lambda.") {
			require.False(t, o.Emulated, "a Lambda call reached the emulator")
			return
		}
	}
	t.Fatal("no Lambda request was seen, so the refusal was not exercised")
}

// The claim itself, asserted rather than described.
func TestTheApplicationNamedNoEndpointAndStillReachedTheEmulator(t *testing.T) {
	observed := live.sidecar.Observed()
	require.NotEmpty(t, observed)

	hosts := map[string]bool{}
	for _, o := range observed {
		if o.Emulated {
			hosts[o.Host] = true
			require.True(t, o.Authorized,
				"%s arrived with no Authorization header, so the SDK's signature "+
					"was lost on the way and this proves nothing about a signed request", o.Host)
			require.True(t, strings.HasPrefix(o.Authorization, "AWS4-HMAC-SHA256"),
				"the Authorization header was rewritten on the way to the emulator")
		}
	}
	require.NotEmpty(t, hosts)
	for host := range hosts {
		require.True(t, strings.HasSuffix(host, "amazonaws.com"),
			"%s is not a real AWS endpoint, so something told the SDK where to go", host)
	}
}

func requireReached(t *testing.T, allowed ...string) {
	t.Helper()
	for _, o := range live.sidecar.Observed() {
		for _, want := range allowed {
			if o.Host == want && o.Emulated {
				return
			}
		}
	}
	t.Fatalf("no request reached the emulator at any of %s", strings.Join(allowed, ", "))
}
