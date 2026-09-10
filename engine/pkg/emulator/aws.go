package emulator

import "github.com/antifailure/antifailure/engine/pkg/extension"

// AWSName is the value an egress rule names the AWS emulator by.
const AWSName = "aws"

// awsImage is LocalStack, pinned by digest.
//
// The digest is the multi architecture index for the 2026.08.1 release, which
// is also tagged stable and latest, so an arm64 machine and an amd64 runner
// resolve the same declaration to their own image and neither is pinned to the
// other's architecture. A tag is refused by the registry's validation and that
// refusal is the point: an emulator answering for production's API behind a
// tag that moves changes what an environment was tested against without
// anything in this repository changing.
const awsImage = "localstack/localstack@sha256:" +
	"4aef81c531684570d7b3cfd2805afa02194c929d52bbeedabb7d4874798b1572"

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
		// The allowlist, and the reason it is one. SERVICES alone only
		// decides what is loaded eagerly; STRICT_SERVICE_LOADING makes it the
		// set of services that may be loaded at all, so a call to a service
		// outside the surface is refused by LocalStack itself rather than
		// quietly answered by an implementation nothing in this repository
		// has ever tested.
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
