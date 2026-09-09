package emulator

import "github.com/antifailure/antifailure/engine/pkg/extension"

// AWSName is the value an egress rule names the AWS emulator by.
const AWSName = "aws"

// awsImage is LocalStack, pinned by digest.
//
// It is the COMMUNITY ARCHIVE, and that is a decision rather than an accident.
// LocalStack archived its community edition in March 2026 and moved to a single
// "LocalStack for AWS" image which refuses to start without an auth token: the
// current image exits with code 55 and "License activation failed" before it
// binds a port, with no environment variables set at all. That was measured on
// 2026-09-07 against the image tagged stable, latest and 2026.08.1. A build
// whose emulator needs somebody's account contradicts the rule that no cloud
// account may be required to run the community suite, so this pins the final
// community build, which starts offline and answers for the nine services
// below.
//
// The image says so itself, which is worth more than that measurement because
// it needs no daemon and no gigabyte of pull. Read each digest's config blob
// out of the registry: the description label on the current image is
// "LocalStack Pro Docker image" and on this one it is "LocalStack Docker
// image", and LOCALSTACK_BUILD_VERSION is 2026.8.1 against 4.14.1.dev75. The
// community-archive tag resolves to the digest below. Checked 2026-09-08.
//
// An organization with a LocalStack licence points an environment at the
// supported image by registering an emulator named aws of their own through
// extension.AddEmulator. The registry refuses two emulators under one name, so
// that is a replacement and never a shadow.
//
// The digest is the multi architecture index, so an arm64 laptop and an amd64
// runner resolve the same declaration to their own image and neither is pinned
// to the other's architecture. A tag is refused by the registry's validation
// and that refusal is the point: an emulator answering for production's API
// behind a tag that moves changes what an environment was tested against
// without anything in this repository changing.
const awsImage = "localstack/localstack@sha256:" +
	"6b6172cfceb04b4fbc35097a55f717c365a35fafa572be49f7341771cf9023ed"

// AWSPort is LocalStack's single gateway port. Every service is answered on
// it, which is why one container answers for nine services and the sidecar
// needs one address rather than nine.
const AWSPort = 4566

func init() { register(aws) }

// aws is the AWS surface, answered by LocalStack.
//
// NINE SERVICES, AND THE LIST IS THE SURFACE. An AWS host outside it is not
// routed here: it falls through to the environment's egress policy, whose
// default is block, so it is refused rather than answered. That is deliberate
// and it is the expensive half of this lane. LocalStack answers for more of
// AWS than this, and a service left in the surface that nothing in the
// conformance suite proves is a claim rather than a capability. Every entry
// below names the call that proves it.
var aws = &Emulator{
	EmulatorName: AWSName,
	Vendor:       "AWS",
	Project:      "LocalStack",
	ProjectURL:   "https://github.com/localstack/localstack",
	// LocalStack is a community and commercial project and is not shipped by
	// Amazon. Recorded rather than left to a reader's inference, for the
	// reason the field's own comment gives.
	Official: false,
	// The same fact with the grain the registry validation reads: a company
	// that is not the cloud whose API this answers for.
	Maintainer: extension.MaintainerCommercial,
	Image:      awsImage,
	Port:       AWSPort,
	Env: map[string]string{
		// The allowlist, and what it is and is not worth. MEASURED on
		// 2026-09-08 against this digest: with these two set, 23 of the 35
		// services LocalStack knows about report disabled, and twelve report
		// available. Nine of those twelve are the surface, the tenth is
		// dynamodbstreams which the surface routes, and the other two are kms
		// and lambda, which load because services in the list depend on them.
		//
		// So this is a reduction and NOT the refusal. A GET to
		// /2015-03-31/functions with a Lambda Host header is answered 200
		// with {"Functions": []} by this container, which is exactly the
		// silent wrong answer a surface is supposed to prevent. What prevents
		// it is the ROUTING: lambda.*.amazonaws.com is not a host any service
		// below claims, so nothing sends the request here at all and the
		// environment's egress policy refuses it. Do not read this pair as a
		// second wall. It is a smaller attack surface and a shorter start.
		"SERVICES": "s3,sqs,sns,dynamodb,kinesis,events,secretsmanager,ssm,sts",

		"STRICT_SERVICE_LOADING": "1",

		// The emulator sits on the environment's inner network, which Docker
		// creates internal, so it has no route out whatever it tries. These
		// two mean it does not try: LocalStack otherwise reports usage events
		// and fetches a CA bundle on first use, and a container spending its
		// startup on connections that cannot complete is a container that
		// takes longer to answer and fills the log with failures that are not
		// the user's problem.
		"DISABLE_EVENTS":         "1",
		"SKIP_SSL_CERT_DOWNLOAD": "1",

		// Nothing an environment does to the emulator survives the
		// environment. A twin that inherited the last twin's buckets would be
		// reproducible only by accident.
		"PERSISTENCE": "0",

		// The emulator must not name itself in an answer, and by default it
		// does. LocalStack's standard SQS endpoint strategy returns a queue
		// URL on ITS OWN domain, sqs.<region>.localhost.localstack.cloud,
		// whatever host the CreateQueue arrived on. An application then sends
		// its next message to that name, which is the whole claim of this
		// work failing from the other end: the code named no endpoint and the
		// emulator handed it one anyway.
		//
		// Measured in CI on 2026-09-09. The Node application on the inner
		// network died with `getaddrinfo EAI_AGAIN
		// sqs.us-east-1.localhost.localstack.cloud`, because that name is not
		// one the environment resolves and the network has no route out to
		// look it up. On a machine where it DID resolve the failure would be
		// worse: it is a public name pointing at 127.0.0.1, so the message
		// would leave the surface and land somewhere nobody declared.
		//
		// `dynamic` is the strategy that derives the URL from the request's
		// own Host header, which for an application reaching
		// sqs.us-east-1.amazonaws.com returns a URL on
		// sqs.us-east-1.amazonaws.com, exactly as real SQS does.
		//
		// `off` was tried first and measured, and it is not enough: it drops
		// the `sqs.<region>.` prefix and still answers on LocalStack's own
		// base domain, so the same run failed one name shorter, with
		// `getaddrinfo EAI_AGAIN localhost.localstack.cloud`. Both of the
		// other strategies name that domain too. Only `dynamic` answers with
		// the name the caller used.
		"SQS_ENDPOINT_STRATEGY": "dynamic",

		// Listen on every interface inside the container, because the address
		// the sidecar forwards to is the container's address on the inner
		// network and not localhost.
		"GATEWAY_LISTEN": "0.0.0.0:4566",

		// The log is read by a person diagnosing a failed twin, so it carries
		// warnings and errors and not a line per request.
		"DEBUG":  "0",
		"LS_LOG": "warning",
	},
	Services: []Service{
		{
			Name: "Amazon S3",
			// Four spellings, which is path style and virtual hosted style,
			// each global and regional. Virtual hosted addressing is the case
			// this whole lane is loudest about: the bucket travels in the Host
			// header, LocalStack reads it from there, and that is why the
			// sidecar preserves the Host header and rewrites only the
			// destination. Rewriting Host would destroy the bucket name.
			Hosts: []string{
				"s3.amazonaws.com", "s3.*.amazonaws.com",
				"*.s3.amazonaws.com", "*.s3.*.amazonaws.com",
			},
			Proves: "CreateBucket, PutObject and GetObject, in both addressing styles",
			Note: "The dualstack and transfer acceleration endpoints are outside this, " +
				"and so is S3 Express One Zone, which resolves under a different suffix.",
		},
		{
			Name:   "Amazon SQS",
			Hosts:  []string{"sqs.*.amazonaws.com"},
			Proves: "CreateQueue, SendMessage and ReceiveMessage",
		},
		{
			Name:   "Amazon SNS",
			Hosts:  []string{"sns.*.amazonaws.com"},
			Proves: "CreateTopic and Publish",
			Note: "A subscription to a real email address or phone number cannot be " +
				"delivered from here, which is the point of it being here.",
		},
		{
			Name:   "Amazon DynamoDB",
			Hosts:  []string{"dynamodb.*.amazonaws.com", "streams.dynamodb.*.amazonaws.com"},
			Proves: "CreateTable, PutItem and GetItem",
		},
		{
			Name:   "Amazon Kinesis",
			Hosts:  []string{"kinesis.*.amazonaws.com"},
			Proves: "CreateStream and PutRecord",
		},
		{
			Name:   "Amazon EventBridge",
			Hosts:  []string{"events.*.amazonaws.com"},
			Proves: "PutRule and PutEvents",
		},
		{
			Name:   "AWS Secrets Manager",
			Hosts:  []string{"secretsmanager.*.amazonaws.com"},
			Proves: "CreateSecret and GetSecretValue",
		},
		{
			Name:   "AWS Systems Manager Parameter Store",
			Hosts:  []string{"ssm.*.amazonaws.com"},
			Proves: "PutParameter and GetParameter",
		},
		{
			Name:  "AWS STS",
			Hosts: []string{"sts.amazonaws.com", "sts.*.amazonaws.com"},
			// STS is not here because anybody asked for it. Most AWS SDKs
			// resolve credentials before the first real call, and several
			// credential chains call GetCallerIdentity or AssumeRole to do it.
			// An emulated surface without STS fails at startup with an error
			// naming the wrong service, and the person reading it goes looking
			// at S3.
			Proves: "GetCallerIdentity and AssumeRole",
		},
	},
	Outside: []Service{
		{
			Name: "AWS Lambda, ECS, EKS, Batch and Step Functions",
			Note: "LocalStack runs these by starting further containers through the " +
				"Docker socket. The environment does not hand a container the Docker " +
				"socket, so this is refused rather than half answered.",
		},
		{
			Name: "Amazon RDS, Aurora, ElastiCache and OpenSearch",
			Note: "A datastore is not emulated here. Postgres is branched from a golden " +
				"and a second store is declared in the manifest with a stance, which is a " +
				"better answer than an emulator with an empty schema in it.",
		},
		{
			Name: "Amazon SES and SESv2",
			Note: "Mail is captured into the environment's inbox, where an agent can read " +
				"it and no real address receives anything. An emulator would swallow it.",
		},
		{
			Name: "Amazon API Gateway, CloudFormation, IAM, CloudWatch and everything else AWS runs",
			Note: "Outside the surface. The request is refused by the egress policy rather " +
				"than answered, because a wrong answer from an emulator is worse than a " +
				"refusal: it will be trusted.",
		},
	},
	Licence: Licence{
		Name:   "Apache License 2.0",
		Holder: "Copyright (c) 2017+ LocalStack contributors, Copyright (c) 2016 Atlassian Pty Ltd",
		URL:    "https://github.com/localstack/localstack/blob/main/LICENSE.txt",
	},
}
