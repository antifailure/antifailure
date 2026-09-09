package detect

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
)

// ThirdPartyAnalyzer maps the SDKs a repository depends on to the hosts they
// talk to, and to the egress mode that host should get.
//
// This is where a manifest gets its network policy without the user having to
// know that Resend is api.resend.com or that Segment is api.segment.io. The
// default mode per host is a judgement, and the judgement is deliberately
// conservative: a mail provider is captured rather than allowed, because
// "allowed" means a preview environment emails a real customer.
type ThirdPartyAnalyzer struct{}

// Name identifies the analyzer.
func (*ThirdPartyAnalyzer) Name() string { return "thirdparty" }

// ThirdParty describes one external service.
type ThirdParty struct {
	// Name is what a person calls it.
	Name string
	// Hosts are the hostnames its SDK talks to.
	Hosts []string
	// Mode is the egress mode this host gets by default.
	Mode string
	// Why explains the mode, and is written into the manifest as a note so
	// that a rule nobody can explain never appears.
	Why string
	// Packages are the dependency names that identify it, per ecosystem.
	Packages []string
	// EnvHints are variable names its SDK reads, used to corroborate.
	EnvHints []string
	// WebhookPath is where it posts callbacks, when there is a convention.
	WebhookPath string
	// Credential is the variable holding its key, when there is a convention.
	Credential string
	// Cloud names the cloud this entry belongs to: aws, gcp or azure, and
	// empty for everything else. It is what lets an emulator running in the
	// repository's own compose file select the entries for the cloud it
	// answers for.
	Cloud string
	// Tokens are the names this service goes by inside its cloud: the client
	// suffix in an SDK package, and the word an emulator's own configuration
	// uses. @aws-sdk/client-secrets-manager, LocalStack's
	// SERVICES=secretsmanager and the endpoint prefix secretsmanager are the
	// same service under three spellings, and a table is the only thing that
	// knows that. Deriving the endpoint from the package name mechanically
	// gets sfn, sesv2 and cloudwatch-logs wrong, and a silently wrong host is
	// worse than a refusal, because the refusal is read.
	Tokens []string
}

// thirdParties is the catalog. It is a data table rather than code so that
// adding a provider is a pull request anyone can review, and so that the
// documentation page can be generated from it.
var thirdParties = []ThirdParty{
	{
		Name: "Stripe", Hosts: []string{"api.stripe.com", "checkout.stripe.com", "files.stripe.com"},
		Mode: "sandbox",
		Why:  "Stripe has a real sandbox, so billing flows run end to end against it.",
		Packages: []string{"stripe", "@stripe/stripe-js", "@stripe/react-stripe-js",
			"stripe-node", "stripe-go", "stripe-ruby", "stripe-python"},
		EnvHints:    []string{"STRIPE_SECRET_KEY", "STRIPE_API_KEY", "STRIPE_PUBLISHABLE_KEY"},
		WebhookPath: "/api/webhooks/stripe",
		Credential:  "STRIPE_SECRET_KEY",
	},
	{
		Name: "SendGrid", Hosts: []string{"api.sendgrid.com"}, Mode: "capture",
		Why:      "Mail is captured into the inbox so that agents can read it and no real address receives anything.",
		Packages: []string{"@sendgrid/mail", "@sendgrid/client", "sendgrid", "sendgrid-ruby"},
		EnvHints: []string{"SENDGRID_API_KEY"},
	},
	{
		Name: "Resend", Hosts: []string{"api.resend.com"}, Mode: "capture",
		Why:      "Mail is captured into the inbox so that agents can read it and no real address receives anything.",
		Packages: []string{"resend"},
		EnvHints: []string{"RESEND_API_KEY"},
	},
	{
		Name: "Postmark", Hosts: []string{"api.postmarkapp.com"}, Mode: "capture",
		Why:      "Mail is captured into the inbox so that agents can read it and no real address receives anything.",
		Packages: []string{"postmark"},
		EnvHints: []string{"POSTMARK_SERVER_TOKEN", "POSTMARK_API_TOKEN"},
	},
	{
		Name: "Mailgun", Hosts: []string{"api.mailgun.net", "api.eu.mailgun.net"}, Mode: "capture",
		Why:      "Mail is captured into the inbox so that agents can read it and no real address receives anything.",
		Packages: []string{"mailgun.js", "mailgun-js", "mailgun"},
		EnvHints: []string{"MAILGUN_API_KEY"},
	},
	{
		Name: "Amazon SES", Hosts: []string{"email.*.amazonaws.com"}, Mode: "capture",
		Why:      "Mail is captured into the inbox so that agents can read it and no real address receives anything.",
		Packages: []string{"@aws-sdk/client-ses", "@aws-sdk/client-sesv2"},
		EnvHints: []string{"AWS_SES_REGION"},
		Cloud:    "aws", Tokens: []string{"ses", "sesv2", "email"},
	},
	{
		Name: "Amazon SES over SMTP", Hosts: []string{"email-smtp.*.amazonaws.com"}, Mode: "block",
		Why: "The sidecar speaks HTTP, so the SMTP submission port is refused rather than captured. " +
			"Mail sent this way would not reach the inbox, and an environment able to open it could " +
			"send to a real address.",
		EnvHints: []string{"SES_SMTP_USERNAME", "SES_SMTP_PASSWORD"},
		Cloud:    "aws", Tokens: []string{"ses-smtp"},
	},
	{
		Name: "Twilio", Hosts: []string{"api.twilio.com", "verify.twilio.com"}, Mode: "capture",
		Why:      "Messages are captured into the inbox so that a one time code can be read without sending an SMS.",
		Packages: []string{"twilio", "twilio-ruby", "twilio-python"},
		EnvHints: []string{"TWILIO_ACCOUNT_SID", "TWILIO_AUTH_TOKEN"},
	},
	{
		Name: "OpenAI", Hosts: []string{"api.openai.com"}, Mode: "mock",
		Why:      "Model calls are mocked so that a preview run costs nothing and returns the same answer twice.",
		Packages: []string{"openai", "@ai-sdk/openai", "langchain"},
		EnvHints: []string{"OPENAI_API_KEY"},
	},
	{
		Name: "Anthropic", Hosts: []string{"api.anthropic.com"}, Mode: "mock",
		Why:      "Model calls are mocked so that a preview run costs nothing and returns the same answer twice.",
		Packages: []string{"@anthropic-ai/sdk", "anthropic", "@ai-sdk/anthropic"},
		EnvHints: []string{"ANTHROPIC_API_KEY"},
	},
	{
		Name: "Segment", Hosts: []string{"api.segment.io", "cdn.segment.com"}, Mode: "block",
		Why:      "Analytics from a preview environment would pollute production reporting.",
		Packages: []string{"@segment/analytics-node", "analytics-node", "@segment/analytics-next"},
		EnvHints: []string{"SEGMENT_WRITE_KEY"},
	},
	{
		Name: "PostHog", Hosts: []string{"app.posthog.com", "us.i.posthog.com", "eu.i.posthog.com"}, Mode: "block",
		Why:      "Analytics from a preview environment would pollute production reporting.",
		Packages: []string{"posthog-node", "posthog-js", "posthog"},
		EnvHints: []string{"POSTHOG_API_KEY", "NEXT_PUBLIC_POSTHOG_KEY"},
	},
	{
		Name: "Mixpanel", Hosts: []string{"api.mixpanel.com"}, Mode: "block",
		Why:      "Analytics from a preview environment would pollute production reporting.",
		Packages: []string{"mixpanel", "mixpanel-browser"},
		EnvHints: []string{"MIXPANEL_TOKEN"},
	},
	{
		Name: "Sentry", Hosts: []string{"*.ingest.sentry.io", "sentry.io"}, Mode: "block",
		Why:      "Errors from a preview environment would drown the production error feed.",
		Packages: []string{"@sentry/node", "@sentry/nextjs", "@sentry/browser", "sentry-sdk", "sentry-ruby"},
		EnvHints: []string{"SENTRY_DSN"},
	},
	{
		Name: "Datadog", Hosts: []string{"api.datadoghq.com", "*.datadoghq.com"}, Mode: "block",
		Why:      "Metrics from a preview environment would distort production dashboards.",
		Packages: []string{"dd-trace", "datadog-metrics", "ddtrace"},
		EnvHints: []string{"DD_API_KEY", "DATADOG_API_KEY"},
	},
	{
		Name: "LaunchDarkly", Hosts: []string{"app.launchdarkly.com", "*.launchdarkly.com"}, Mode: "mock",
		Why:      "Flags are mocked so that a preview run is deterministic rather than depending on the live flag state.",
		Packages: []string{"launchdarkly-node-server-sdk", "@launchdarkly/node-server-sdk", "launchdarkly-server-sdk"},
		EnvHints: []string{"LAUNCHDARKLY_SDK_KEY", "LD_SDK_KEY"},
	},
	{
		Name: "Clerk", Hosts: []string{"api.clerk.com", "api.clerk.dev", "*.clerk.accounts.dev"}, Mode: "sandbox",
		Why:      "Clerk has development instances, so personas sign in through the real flow.",
		Packages: []string{"@clerk/nextjs", "@clerk/clerk-sdk-node", "@clerk/backend", "@clerk/clerk-js"},
		EnvHints: []string{"CLERK_SECRET_KEY", "NEXT_PUBLIC_CLERK_PUBLISHABLE_KEY"},
	},
	{
		Name: "WorkOS", Hosts: []string{"api.workos.com"}, Mode: "sandbox",
		Why:      "WorkOS has a staging environment, so personas sign in through the real flow.",
		Packages: []string{"@workos-inc/node", "@workos-inc/authkit-nextjs", "workos"},
		EnvHints: []string{"WORKOS_API_KEY", "WORKOS_CLIENT_ID"},
	},
	{
		Name: "Auth0", Hosts: []string{"*.auth0.com"}, Mode: "sandbox",
		Why:      "Auth0 tenants are free to create, so personas sign in through the real flow.",
		Packages: []string{"auth0", "@auth0/nextjs-auth0", "express-openid-connect"},
		EnvHints: []string{"AUTH0_CLIENT_SECRET", "AUTH0_DOMAIN"},
	},
	{
		Name: "Supabase", Hosts: []string{"*.supabase.co", "*.supabase.in"}, Mode: "allow",
		Why:      "The environment's own Supabase project is the database, so its API is reached directly.",
		Packages: []string{"@supabase/supabase-js", "@supabase/ssr", "@supabase/auth-helpers-nextjs", "supabase"},
		EnvHints: []string{"SUPABASE_URL", "NEXT_PUBLIC_SUPABASE_URL", "SUPABASE_SERVICE_ROLE_KEY"},
	},
	{
		Name: "Slack", Hosts: []string{"slack.com", "hooks.slack.com"}, Mode: "capture",
		Why:      "Slack messages are captured so that a preview run does not post into a real channel.",
		Packages: []string{"@slack/web-api", "@slack/bolt", "slack-sdk"},
		EnvHints: []string{"SLACK_BOT_TOKEN", "SLACK_WEBHOOK_URL"},
	},
	{
		Name: "GitHub", Hosts: []string{"api.github.com"}, Mode: "mock",
		Why:      "GitHub calls are mocked so that a preview run cannot write to a real repository.",
		Packages: []string{"@octokit/rest", "@octokit/core", "octokit", "go-github"},
		EnvHints: []string{"GITHUB_TOKEN", "GH_TOKEN"},
	},
	{
		Name: "Cloudinary", Hosts: []string{"api.cloudinary.com", "res.cloudinary.com"}, Mode: "allow",
		Why:      "Media is read only in a preview environment, so reads pass through.",
		Packages: []string{"cloudinary", "next-cloudinary"},
		EnvHints: []string{"CLOUDINARY_URL", "CLOUDINARY_API_SECRET"},
	},
	{
		Name: "Algolia", Hosts: []string{"*.algolia.net", "*.algolianet.com"}, Mode: "mock",
		Why:      "Search results are mocked so that a preview run does not depend on an index it did not build.",
		Packages: []string{"algoliasearch", "@algolia/client-search"},
		EnvHints: []string{"ALGOLIA_API_KEY", "ALGOLIA_APP_ID"},
	},
	{
		Name: "Upstash", Hosts: []string{"*.upstash.io"}, Mode: "allow",
		Why:      "The environment's own Upstash database is reached directly.",
		Packages: []string{"@upstash/redis", "@upstash/ratelimit", "@upstash/qstash"},
		EnvHints: []string{"UPSTASH_REDIS_REST_URL", "UPSTASH_REDIS_REST_TOKEN"},
	},

	// The clouds.
	//
	// Every entry below is block, and every one of them says why in its own
	// words rather than sharing a sentence, because the cost of reaching each
	// of these from a preview environment is a different cost. A queue is
	// consumed by production workers, a secret store hands a production
	// credential to unreviewed code, and an object write is indistinguishable
	// from production data once it lands.
	//
	// Block is the honest answer until an emulator answers for the service,
	// and it is a better answer than the one this catalog used to give. The
	// SES entry above carried *.amazonaws.com, so every AWS host in this
	// section matched a mail rule and was answered 200 with an empty body by a
	// handler that believed it was holding an email.
	//
	// The hosts are written out per service rather than covered by one
	// wildcard, because naming the service is the whole point: a refusal that
	// says "this is S3, and here is why an environment may not write to it"
	// is a sentence somebody can act on, and one that says "no rule matches"
	// is not.
	{
		Name: "Amazon S3",
		Hosts: []string{
			"s3.amazonaws.com", "s3.*.amazonaws.com",
			"*.s3.amazonaws.com", "*.s3.*.amazonaws.com",
		},
		Mode: "block",
		// Four spellings, which is path style and virtual hosted style, each
		// global and regional. The dualstack and transfer acceleration
		// endpoints are NOT here and that is a stated gap rather than an
		// oversight: they are two further spellings of these same four rules
		// and eight S3 entries in a generated manifest is a manifest nobody
		// reads. They still reach nothing, because the default is block; the
		// refusal says no rule matches rather than naming S3.
		Why: "An object written from a preview environment lands in the real bucket, where nothing " +
			"tells it apart from production data afterwards.",
		Packages: []string{"@aws-sdk/client-s3", "@aws-sdk/lib-storage", "aws-sdk", "boto3",
			"aws-sdk-go", "aws-sdk-go-v2", "aws-sdk-s3", "fog-aws"},
		EnvHints: []string{"AWS_S3_BUCKET", "S3_BUCKET", "AWS_BUCKET_NAME"},
		Cloud:    "aws", Tokens: []string{"s3"},
	},
	{
		Name: "Amazon SQS", Hosts: []string{"sqs.*.amazonaws.com"}, Mode: "block",
		Why: "A message sent to a real queue is picked up by production workers, which is a preview " +
			"environment reaching into production through the back door.",
		Packages: []string{"@aws-sdk/client-sqs", "aws-sdk", "boto3", "aws-sdk-go", "aws-sdk-go-v2"},
		EnvHints: []string{"SQS_QUEUE_URL", "AWS_SQS_QUEUE_URL"},
		Cloud:    "aws", Tokens: []string{"sqs"},
	},
	{
		Name: "Amazon SNS", Hosts: []string{"sns.*.amazonaws.com"}, Mode: "block",
		Why: "A publish reaches every real subscriber, and some of those subscribers are a phone " +
			"number and an email address.",
		Packages: []string{"@aws-sdk/client-sns", "aws-sdk", "boto3", "aws-sdk-go", "aws-sdk-go-v2"},
		EnvHints: []string{"SNS_TOPIC_ARN", "AWS_SNS_TOPIC_ARN"},
		Cloud:    "aws", Tokens: []string{"sns"},
	},
	{
		Name:  "Amazon DynamoDB",
		Hosts: []string{"dynamodb.*.amazonaws.com", "streams.dynamodb.*.amazonaws.com"},
		Mode:  "block",
		Why: "This is a production datastore, and an environment writing to it is writing to " +
			"production. Antifailure branches Postgres and does not yet branch this.",
		Packages: []string{"@aws-sdk/client-dynamodb", "@aws-sdk/lib-dynamodb", "dynamoose",
			"aws-sdk", "boto3", "aws-sdk-go", "aws-sdk-go-v2"},
		EnvHints: []string{"DYNAMODB_TABLE", "AWS_DYNAMODB_TABLE"},
		Cloud:    "aws", Tokens: []string{"dynamodb", "dynamodbstreams"},
	},
	{
		Name: "Amazon Kinesis", Hosts: []string{"kinesis.*.amazonaws.com"}, Mode: "block",
		Why: "Records put onto a real stream are read by production consumers and cannot be taken " +
			"back off it.",
		Packages: []string{"@aws-sdk/client-kinesis", "aws-sdk", "boto3", "aws-sdk-go", "aws-sdk-go-v2"},
		EnvHints: []string{"KINESIS_STREAM_NAME"},
		Cloud:    "aws", Tokens: []string{"kinesis"},
	},
	{
		Name: "Amazon EventBridge", Hosts: []string{"events.*.amazonaws.com"}, Mode: "block",
		Why: "An event on the real bus fans out to every production rule that matches it, and the " +
			"targets are whatever those rules point at.",
		Packages: []string{"@aws-sdk/client-eventbridge", "aws-sdk", "boto3", "aws-sdk-go", "aws-sdk-go-v2"},
		EnvHints: []string{"EVENT_BUS_NAME", "EVENTBRIDGE_BUS_NAME"},
		Cloud:    "aws", Tokens: []string{"eventbridge", "events"},
	},
	{
		Name: "AWS Secrets Manager", Hosts: []string{"secretsmanager.*.amazonaws.com"}, Mode: "block",
		Why: "Reading it hands a production credential to an environment running unreviewed code " +
			"against a copy of production data, which is the one thing this product exists to stop.",
		Packages: []string{"@aws-sdk/client-secrets-manager", "aws-sdk", "boto3", "aws-sdk-go", "aws-sdk-go-v2"},
		EnvHints: []string{"AWS_SECRET_NAME", "SECRETS_MANAGER_SECRET_ID"},
		Cloud:    "aws", Tokens: []string{"secrets-manager", "secretsmanager"},
	},
	{
		Name: "AWS Systems Manager Parameter Store", Hosts: []string{"ssm.*.amazonaws.com"}, Mode: "block",
		Why: "Parameter Store holds production configuration and, through SecureString, production " +
			"credentials, so it is refused for the same reason Secrets Manager is.",
		Packages: []string{"@aws-sdk/client-ssm", "aws-sdk", "boto3", "aws-sdk-go", "aws-sdk-go-v2"},
		EnvHints: []string{"SSM_PARAMETER_PATH", "AWS_SSM_PATH"},
		Cloud:    "aws", Tokens: []string{"ssm"},
	},
	{
		Name: "AWS STS", Hosts: []string{"sts.amazonaws.com", "sts.*.amazonaws.com"}, Mode: "block",
		Why: "STS mints credentials. An environment that can call it can hold a production role for " +
			"an hour, and no rule about any other host applies to what it does with one.",
		Packages: []string{"@aws-sdk/client-sts", "aws-sdk", "boto3", "aws-sdk-go", "aws-sdk-go-v2"},
		EnvHints: []string{"AWS_ROLE_ARN", "AWS_WEB_IDENTITY_TOKEN_FILE"},
		Cloud:    "aws", Tokens: []string{"sts"},
	},
	{
		Name: "Google Cloud Storage", Hosts: []string{"storage.googleapis.com"}, Mode: "block",
		Why: "An object written from a preview environment lands in the real bucket. Google ships no " +
			"official Cloud Storage emulator, which is why this is a refusal rather than a redirect.",
		Packages: []string{"@google-cloud/storage", "google-cloud-storage", "gcs-resumable-upload"},
		EnvHints: []string{"GCS_BUCKET", "GOOGLE_CLOUD_STORAGE_BUCKET"},
		Cloud:    "gcp", Tokens: []string{"storage"},
	},
	{
		Name: "Google Cloud Pub/Sub", Hosts: []string{"pubsub.googleapis.com"}, Mode: "block",
		Why:      "A message published to a real topic is delivered to production subscribers.",
		Packages: []string{"@google-cloud/pubsub", "google-cloud-pubsub"},
		EnvHints: []string{"PUBSUB_TOPIC", "GOOGLE_PUBSUB_TOPIC"},
		Cloud:    "gcp", Tokens: []string{"pubsub"},
	},
	{
		Name: "Google Cloud Firestore", Hosts: []string{"firestore.googleapis.com"}, Mode: "block",
		Why:      "This is a production datastore, and an environment writing to it is writing to production.",
		Packages: []string{"@google-cloud/firestore", "google-cloud-firestore", "firebase-admin"},
		EnvHints: []string{"FIRESTORE_PROJECT_ID", "FIRESTORE_EMULATOR_HOST"},
		Cloud:    "gcp", Tokens: []string{"firestore"},
	},
	{
		Name: "Google Secret Manager", Hosts: []string{"secretmanager.googleapis.com"}, Mode: "block",
		Why: "Reading it hands a production credential to an environment running unreviewed code " +
			"against a copy of production data.",
		Packages: []string{"@google-cloud/secret-manager", "google-cloud-secret-manager"},
		EnvHints: []string{"GOOGLE_SECRET_NAME", "SECRET_MANAGER_PROJECT"},
		Cloud:    "gcp", Tokens: []string{"secret-manager", "secretmanager"},
	},
	{
		Name: "Google Cloud Tasks", Hosts: []string{"cloudtasks.googleapis.com"}, Mode: "block",
		Why: "A task enqueued on a real queue is dispatched to a production handler, at a time nobody " +
			"is watching for it.",
		Packages: []string{"@google-cloud/tasks", "google-cloud-tasks"},
		EnvHints: []string{"CLOUD_TASKS_QUEUE", "GOOGLE_CLOUD_TASKS_QUEUE"},
		Cloud:    "gcp", Tokens: []string{"tasks", "cloudtasks"},
	},
	{
		Name: "Azure Blob Storage", Hosts: []string{"*.blob.core.windows.net"}, Mode: "block",
		Why: "An object written from a preview environment lands in the real container, where nothing " +
			"tells it apart from production data afterwards.",
		Packages: []string{"@azure/storage-blob", "azure-storage-blob", "azure-storage"},
		EnvHints: []string{"AZURE_STORAGE_ACCOUNT", "AZURE_STORAGE_CONNECTION_STRING"},
		Cloud:    "azure", Tokens: []string{"storage-blob", "blob"},
	},
	{
		Name: "Azure Queue Storage", Hosts: []string{"*.queue.core.windows.net"}, Mode: "block",
		Why:      "A message sent to a real queue is picked up by production workers.",
		Packages: []string{"@azure/storage-queue", "azure-storage-queue"},
		EnvHints: []string{"AZURE_QUEUE_NAME"},
		Cloud:    "azure", Tokens: []string{"storage-queue", "queue"},
	},
	{
		Name: "Azure Service Bus", Hosts: []string{"*.servicebus.windows.net"}, Mode: "block",
		Why: "A message on a real topic or queue is delivered to production subscribers. The Microsoft " +
			"emulator that answers for this needs an MSSQL container beside it, which is weight an " +
			"environment pays for on purpose rather than by default.",
		Packages: []string{"@azure/service-bus", "azure-servicebus"},
		EnvHints: []string{"SERVICEBUS_CONNECTION_STRING", "AZURE_SERVICEBUS_NAMESPACE"},
		Cloud:    "azure", Tokens: []string{"service-bus", "servicebus", "eventhubs", "event-hubs"},
	},
	{
		Name: "Azure Table Storage", Hosts: []string{"*.table.core.windows.net"}, Mode: "block",
		Why:      "This is a production datastore, and an environment writing to it is writing to production.",
		Packages: []string{"@azure/data-tables", "azure-data-tables"},
		EnvHints: []string{"AZURE_TABLE_NAME"},
		Cloud:    "azure", Tokens: []string{"data-tables", "table"},
	},
	{
		Name: "Azure Files", Hosts: []string{"*.file.core.windows.net"}, Mode: "block",
		Why: "A file written from a preview environment lands in the real share, where nothing tells " +
			"it apart from production data afterwards.",
		Packages: []string{"@azure/storage-file-share", "azure-storage-file-share"},
		EnvHints: []string{"AZURE_FILE_SHARE_NAME"},
		Cloud:    "azure", Tokens: []string{"storage-file-share", "file"},
	},
	{
		Name: "Azure Cosmos DB", Hosts: []string{"*.documents.azure.com"}, Mode: "block",
		Why:      "This is a production datastore, and an environment writing to it is writing to production.",
		Packages: []string{"@azure/cosmos", "azure-cosmos"},
		EnvHints: []string{"COSMOS_ENDPOINT", "AZURE_COSMOS_CONNECTION_STRING"},
		Cloud:    "azure", Tokens: []string{"cosmos"},
	},
	{
		Name: "Azure Key Vault", Hosts: []string{"*.vault.azure.net"}, Mode: "block",
		Why: "Reading it hands a production credential to an environment running unreviewed code " +
			"against a copy of production data.",
		Packages: []string{"@azure/keyvault-secrets", "@azure/keyvault-keys", "azure-keyvault-secrets"},
		EnvHints: []string{"AZURE_KEY_VAULT_URL", "KEY_VAULT_NAME"},
		Cloud:    "azure", Tokens: []string{"keyvault-secrets", "keyvault-keys", "keyvault"},
	},

	// The rest of each cloud, added because the nine, five and seven above
	// were the services one lane could name in one pass and an application
	// touches more than that.
	//
	// A repository depending on @aws-sdk/client-lambda got no rule at all, so
	// the invoke was refused with "no rule matches" rather than "this is
	// Lambda". That refusal is correct and it is unreadable, and the whole
	// argument for a per service catalog is that the sentence can be acted on.
	// Every entry here is still block, for the same reason as the ones above:
	// block is the honest answer until an emulator answers for the service,
	// and this build has none.
	//
	// The endpoint is written out rather than derived from the package name.
	// AWS spells three of these differently in the two places, sfn against
	// states and cloudwatch-logs against logs among them, and a host guessed
	// wrong is worse than a host not named: the wrong pattern matches nothing,
	// the request falls through, and the catalog looks like it covered a
	// service it never did.
	{
		Name: "AWS Lambda", Hosts: []string{"lambda.*.amazonaws.com"}, Mode: "block",
		Why: "Invoking a real function runs production code, with production's own permissions, " +
			"at a time nobody is watching for it.",
		Packages: []string{"@aws-sdk/client-lambda", "aws-lambda"},
		EnvHints: []string{"LAMBDA_FUNCTION_NAME", "AWS_LAMBDA_FUNCTION_NAME"},
		Cloud:    "aws", Tokens: []string{"lambda"},
	},
	{
		Name: "Amazon CloudWatch Logs", Hosts: []string{"logs.*.amazonaws.com"}, Mode: "block",
		Why: "A preview environment's log lines land in the production log group, where an on " +
			"call engineer reads them as production and an alarm counts them as production.",
		Packages: []string{"@aws-sdk/client-cloudwatch-logs", "watchtower"},
		EnvHints: []string{"CLOUDWATCH_LOG_GROUP", "AWS_LOG_GROUP"},
		Cloud:    "aws", Tokens: []string{"cloudwatch-logs", "logs"},
	},
	{
		Name: "Amazon CloudWatch", Hosts: []string{"monitoring.*.amazonaws.com"}, Mode: "block",
		Why: "Metrics from a preview environment distort the production dashboards and the " +
			"alarms wired to them.",
		Packages: []string{"@aws-sdk/client-cloudwatch"},
		EnvHints: []string{"CLOUDWATCH_NAMESPACE"},
		Cloud:    "aws", Tokens: []string{"cloudwatch", "monitoring"},
	},
	{
		Name: "AWS KMS", Hosts: []string{"kms.*.amazonaws.com"}, Mode: "block",
		Why: "A decrypt turns production ciphertext into plaintext inside an environment running " +
			"unreviewed code, which is the same exposure a secret store is refused for.",
		Packages: []string{"@aws-sdk/client-kms"},
		EnvHints: []string{"KMS_KEY_ID", "AWS_KMS_KEY_ID"},
		Cloud:    "aws", Tokens: []string{"kms"},
	},
	{
		Name: "AWS Step Functions", Hosts: []string{"states.*.amazonaws.com"}, Mode: "block",
		Why: "Starting a real execution runs every step of a production workflow, and the steps " +
			"are whatever that state machine points at.",
		Packages: []string{"@aws-sdk/client-sfn"},
		EnvHints: []string{"STATE_MACHINE_ARN", "SFN_STATE_MACHINE_ARN"},
		Cloud:    "aws", Tokens: []string{"sfn", "stepfunctions", "states"},
	},
	{
		Name: "Amazon Athena", Hosts: []string{"athena.*.amazonaws.com"}, Mode: "block",
		Why: "A query reads the production data lake unmasked, and it is billed by the volume it " +
			"scans, so a loop in a preview environment is a bill as well as a leak.",
		Packages: []string{"@aws-sdk/client-athena", "pyathena"},
		EnvHints: []string{"ATHENA_WORKGROUP", "ATHENA_DATABASE"},
		Cloud:    "aws", Tokens: []string{"athena"},
	},
	{
		Name: "Amazon Bedrock",
		Hosts: []string{
			"bedrock.*.amazonaws.com", "bedrock-runtime.*.amazonaws.com",
		},
		Mode: "block",
		Why: "Model calls are billed per token and answer differently every run, so reaching " +
			"them costs money and makes the run non repeatable.",
		Packages: []string{"@aws-sdk/client-bedrock", "@aws-sdk/client-bedrock-runtime"},
		EnvHints: []string{"BEDROCK_MODEL_ID", "AWS_BEDROCK_MODEL_ID"},
		Cloud:    "aws", Tokens: []string{"bedrock", "bedrock-runtime"},
	},
	{
		Name: "Amazon Cognito",
		Hosts: []string{
			"cognito-idp.*.amazonaws.com", "cognito-identity.*.amazonaws.com",
		},
		Mode: "block",
		Why: "A sign up writes a real user into the production pool, and that user can then sign " +
			"in to production. Personas exist so this does not have to happen.",
		Packages: []string{"@aws-sdk/client-cognito-identity-provider",
			"@aws-sdk/client-cognito-identity", "amazon-cognito-identity-js"},
		EnvHints: []string{"COGNITO_USER_POOL_ID", "AWS_COGNITO_USER_POOL_ID"},
		Cloud:    "aws", Tokens: []string{"cognito-identity-provider", "cognito-idp", "cognito"},
	},
	{
		Name: "Amazon API Gateway",
		Hosts: []string{
			"apigateway.*.amazonaws.com", "*.execute-api.*.amazonaws.com",
		},
		Mode: "block",
		Why: "A call to a deployed API reaches the production service behind it, and the " +
			"management endpoint can change what that API does for everyone.",
		Packages: []string{"@aws-sdk/client-api-gateway", "@aws-sdk/client-apigatewayv2"},
		EnvHints: []string{"API_GATEWAY_ID", "APIGATEWAY_ENDPOINT"},
		Cloud:    "aws", Tokens: []string{"api-gateway", "apigateway", "apigatewayv2", "execute-api"},
	},
	{
		Name: "Amazon Data Firehose", Hosts: []string{"firehose.*.amazonaws.com"}, Mode: "block",
		Why: "Records put onto a real delivery stream are written to whatever it delivers to, " +
			"usually a production bucket or warehouse, and cannot be taken back out.",
		Packages: []string{"@aws-sdk/client-firehose"},
		EnvHints: []string{"FIREHOSE_STREAM_NAME"},
		Cloud:    "aws", Tokens: []string{"firehose"},
	},
	{
		Name: "Google BigQuery",
		Hosts: []string{
			"bigquery.googleapis.com", "bigquerystorage.googleapis.com",
		},
		Mode: "block",
		Why: "A query reads production's warehouse unmasked and is billed by the bytes it scans, " +
			"and an insert lands in a table the business reports from.",
		Packages: []string{"@google-cloud/bigquery", "google-cloud-bigquery"},
		EnvHints: []string{"BIGQUERY_DATASET", "GOOGLE_BIGQUERY_DATASET"},
		Cloud:    "gcp", Tokens: []string{"bigquery", "bigquerystorage"},
	},
	{
		Name: "Google Cloud Logging", Hosts: []string{"logging.googleapis.com"}, Mode: "block",
		Why: "A preview environment's log lines land in the production project, where they are " +
			"read and alerted on as production.",
		Packages: []string{"@google-cloud/logging", "@google-cloud/logging-winston", "google-cloud-logging"},
		EnvHints: []string{"GOOGLE_CLOUD_LOG_NAME"},
		Cloud:    "gcp", Tokens: []string{"logging", "logging-winston"},
	},
	{
		Name: "Google Cloud Monitoring", Hosts: []string{"monitoring.googleapis.com"}, Mode: "block",
		Why:      "Metrics from a preview environment distort the production dashboards and alerting policies.",
		Packages: []string{"@google-cloud/monitoring", "google-cloud-monitoring"},
		EnvHints: []string{"GOOGLE_CLOUD_METRIC_PREFIX"},
		Cloud:    "gcp", Tokens: []string{"monitoring"},
	},
	{
		Name: "Google Cloud Bigtable",
		Hosts: []string{
			"bigtable.googleapis.com", "bigtableadmin.googleapis.com",
		},
		Mode:     "block",
		Why:      "This is a production datastore, and an environment writing to it is writing to production.",
		Packages: []string{"@google-cloud/bigtable", "google-cloud-bigtable"},
		EnvHints: []string{"BIGTABLE_INSTANCE_ID", "BIGTABLE_EMULATOR_HOST"},
		Cloud:    "gcp", Tokens: []string{"bigtable", "bigtableadmin"},
	},
	{
		Name: "Google Cloud Spanner", Hosts: []string{"spanner.googleapis.com"}, Mode: "block",
		Why:      "This is a production datastore, and an environment writing to it is writing to production.",
		Packages: []string{"@google-cloud/spanner", "google-cloud-spanner"},
		EnvHints: []string{"SPANNER_INSTANCE_ID", "SPANNER_EMULATOR_HOST"},
		Cloud:    "gcp", Tokens: []string{"spanner"},
	},
	{
		Name: "Google Vertex AI", Hosts: []string{"aiplatform.googleapis.com"}, Mode: "block",
		// The regional spelling is us-central1-aiplatform.googleapis.com and
		// it is NOT here. A star in a rule stands for one whole label, and
		// us-central1-aiplatform is not one, so the only pattern that would
		// cover it is *.googleapis.com, which is the wildcard this catalog was
		// rewritten to remove. A regional endpoint therefore still reaches
		// nothing, because the default is block, and its refusal says no rule
		// matches rather than naming Vertex. A stated gap, not an oversight.
		Why: "Model calls are billed per token and answer differently every run, so reaching " +
			"them costs money and makes the run non repeatable.",
		Packages: []string{"@google-cloud/aiplatform", "@google-cloud/vertexai", "google-cloud-aiplatform"},
		EnvHints: []string{"VERTEX_AI_LOCATION", "GOOGLE_VERTEX_PROJECT"},
		Cloud:    "gcp", Tokens: []string{"aiplatform", "vertexai"},
	},
	{
		Name: "Google Cloud Run", Hosts: []string{"run.googleapis.com"}, Mode: "block",
		Why: "The admin API deploys and scales real services, so an environment that can reach " +
			"it can change what production runs.",
		Packages: []string{"@google-cloud/run", "google-cloud-run"},
		EnvHints: []string{"CLOUD_RUN_SERVICE", "K_SERVICE"},
		Cloud:    "gcp", Tokens: []string{"run"},
	},
	{
		Name: "Google OAuth token endpoint",
		Hosts: []string{
			"oauth2.googleapis.com", "accounts.google.com", "iamcredentials.googleapis.com",
		},
		Mode: "block",
		// The one every other Google entry depends on. A client library
		// exchanges a service account key here before it calls anything at
		// all, so an unnamed refusal at this host is the FIRST thing a Google
		// application sees and it says nothing about Google.
		Why: "This is where a service account key is exchanged for an access token. An " +
			"environment holding one holds production's identity for an hour, and no rule about " +
			"any other host applies to what it does with it.",
		Packages: []string{"google-auth-library", "google-auth", "@google-cloud/local-auth",
			"googleapis", "google-api-python-client"},
		EnvHints: []string{"GOOGLE_APPLICATION_CREDENTIALS", "GOOGLE_SERVICE_ACCOUNT_KEY"},
		Cloud:    "gcp", Tokens: []string{"auth", "local-auth", "iamcredentials"},
	},
	{
		Name: "Microsoft Entra ID",
		Hosts: []string{
			"login.microsoftonline.com", "login.windows.net",
		},
		Mode: "block",
		// The Azure peer of the entry above, and the same reasoning: every
		// @azure/* client asks DefaultAzureCredential for a token first, and
		// that request goes here.
		Why: "This is where a client credential is exchanged for a token in the production " +
			"tenant. An environment that can reach it holds production's identity, and every " +
			"other Azure rule is downstream of that.",
		Packages: []string{"@azure/identity", "@azure/msal-node", "azure-identity", "msal"},
		EnvHints: []string{"AZURE_TENANT_ID", "AZURE_CLIENT_ID", "AZURE_CLIENT_SECRET"},
		Cloud:    "azure", Tokens: []string{"identity", "msal-node", "msal"},
	},
	{
		Name: "Azure OpenAI", Hosts: []string{"*.openai.azure.com"}, Mode: "mock",
		// Mock rather than block, matching OpenAI and Anthropic above. What
		// this one costs is money and a different answer every run, not a
		// write to production, and a mock keeps the run repeatable where a
		// refusal only makes it fail.
		Why:      "Model calls are mocked so that a preview run costs nothing and returns the same answer twice.",
		Packages: []string{"@azure/openai", "@azure/ai-openai"},
		EnvHints: []string{"AZURE_OPENAI_ENDPOINT", "AZURE_OPENAI_API_KEY"},
		Cloud:    "azure", Tokens: []string{"openai", "ai-openai"},
	},
	{
		Name: "Azure AI Search", Hosts: []string{"*.search.windows.net"}, Mode: "block",
		Why: "An index write changes what production search returns, and a query reads " +
			"production's documents unmasked.",
		Packages: []string{"@azure/search-documents", "azure-search-documents"},
		EnvHints: []string{"AZURE_SEARCH_ENDPOINT", "AZURE_SEARCH_INDEX_NAME"},
		Cloud:    "azure", Tokens: []string{"search-documents", "search"},
	},
	{
		Name: "Azure Monitor and Application Insights",
		Hosts: []string{
			"dc.services.visualstudio.com", "*.in.applicationinsights.azure.com",
			"*.livediagnostics.monitor.azure.com",
		},
		Mode: "block",
		Why: "Telemetry from a preview environment drowns the production error feed and moves " +
			"the availability numbers somebody is judged on.",
		Packages: []string{"@azure/monitor-opentelemetry", "applicationinsights",
			"azure-monitor-opentelemetry"},
		EnvHints: []string{"APPLICATIONINSIGHTS_CONNECTION_STRING", "APPINSIGHTS_INSTRUMENTATIONKEY"},
		Cloud:    "azure", Tokens: []string{"monitor-opentelemetry", "monitor"},
	},
	{
		Name: "Azure App Configuration", Hosts: []string{"*.azconfig.io"}, Mode: "block",
		Why: "It holds production configuration and, through its Key Vault references, " +
			"production credentials, so it is refused for the same reason Key Vault is.",
		Packages: []string{"@azure/app-configuration", "azure-appconfiguration"},
		EnvHints: []string{"AZURE_APPCONFIG_ENDPOINT", "APP_CONFIGURATION_CONNECTION_STRING"},
		Cloud:    "azure", Tokens: []string{"app-configuration", "appconfiguration", "appconfig"},
	},
}

// Analyze reports each third party the repository depends on.
func (a *ThirdPartyAnalyzer) Analyze(_ context.Context, r *Repo) ([]Finding, error) {
	deps := collectDependencies(r)
	envNames := collectEnvNames(r)

	var out []Finding
	for _, tp := range thirdParties {
		var evidence, reason string
		for _, pkg := range tp.Packages {
			if file, ok := deps[pkg]; ok {
				evidence, reason = file, fmt.Sprintf("%s depends on %s.", file, pkg)
				break
			}
		}
		conf := High
		if evidence == "" {
			// No dependency, but a variable its SDK reads. Weaker evidence,
			// and worth reporting: a service reached through raw HTTP still
			// needs a rule.
			for _, name := range tp.EnvHints {
				if file, ok := envNames[name]; ok {
					evidence, reason = file, fmt.Sprintf("%s declares %s.", file, name)
					conf = Low
					break
				}
			}
		}
		if evidence == "" {
			continue
		}
		for _, host := range tp.Hosts {
			out = append(out, Finding{
				Kind: KindThirdParty, Subject: host, Value: tp.Mode,
				Confidence: conf, Evidence: evidence, Detail: reason,
				Extra: map[string]string{
					"provider":     tp.Name,
					"why":          tp.Why,
					"webhook_path": tp.WebhookPath,
					"credential":   tp.Credential,
				},
			})
		}
	}
	// The gap, named. A cloud SDK the catalog does not claim produces no rule
	// at all, and a missing rule is invisible in the manifest by definition.
	out = append(out, unnamedCloudServices(deps)...)
	return out, nil
}

// collectDependencies gathers every declared dependency name across every
// ecosystem, mapped to the file that declared it.
func collectDependencies(r *Repo) map[string]string {
	out := map[string]string{}

	for _, p := range r.Glob("package.json") {
		b, ok := r.Read(p)
		if !ok {
			continue
		}
		var pkg packageJSON
		if jsonUnmarshal(b, &pkg) != nil {
			continue
		}
		for name := range pkg.allDeps() {
			if _, exists := out[name]; !exists {
				out[name] = p
			}
		}
	}

	// Python and Ruby declarations are read as text, because the point is to
	// find a name rather than to resolve a version.
	for _, name := range []string{"requirements.txt", "pyproject.toml", "Pipfile", "setup.py", "Gemfile"} {
		for _, p := range r.Glob(name) {
			body, ok := r.ReadString(p)
			if !ok {
				continue
			}
			for _, dep := range extractLooseDependencyNames(body) {
				if _, exists := out[dep]; !exists {
					out[dep] = p
				}
			}
		}
	}

	// Go modules name their dependencies by import path, so the last segment
	// is what matches a catalog entry.
	for _, p := range r.Glob("go.mod") {
		body, ok := r.ReadString(p)
		if !ok {
			continue
		}
		for _, dep := range goRequires(body) {
			short := dep
			if i := strings.LastIndexByte(short, '/'); i >= 0 {
				short = short[i+1:]
			}
			if _, exists := out[short]; !exists {
				out[short] = p
			}
			if _, exists := out[dep]; !exists {
				out[dep] = p
			}
		}
	}
	return out
}

var looseDepRe = regexp.MustCompile(`(?m)^\s*(?:gem\s+["']|["']?)([a-zA-Z0-9][a-zA-Z0-9._@/-]{1,60})["']?\s*(?:[=<>~!,\[]|$)`)

// extractLooseDependencyNames pulls plausible package names out of a
// declaration file without parsing its format.
func extractLooseDependencyNames(body string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range looseDepRe.FindAllStringSubmatch(body, -1) {
		name := strings.ToLower(m[1])
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

var goRequireRe = regexp.MustCompile(`(?m)^\s*(?:require\s+)?([a-z0-9.-]+\.[a-z]{2,}/[^\s]+)\s+v`)

func goRequires(body string) []string {
	var out []string
	for _, m := range goRequireRe.FindAllStringSubmatch(body, -1) {
		out = append(out, m[1])
	}
	return out
}

// unusedPath keeps the path import present for future analyzers in this file.
var _ = path.Base
