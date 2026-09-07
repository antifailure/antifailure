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
	},
	{
		Name: "Amazon SES over SMTP", Hosts: []string{"email-smtp.*.amazonaws.com"}, Mode: "block",
		Why: "The sidecar speaks HTTP, so the SMTP submission port is refused rather than captured. " +
			"Mail sent this way would not reach the inbox, and an environment able to open it could " +
			"send to a real address.",
		EnvHints: []string{"SES_SMTP_USERNAME", "SES_SMTP_PASSWORD"},
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
	},
	{
		Name: "Amazon SQS", Hosts: []string{"sqs.*.amazonaws.com"}, Mode: "block",
		Why: "A message sent to a real queue is picked up by production workers, which is a preview " +
			"environment reaching into production through the back door.",
		Packages: []string{"@aws-sdk/client-sqs", "aws-sdk", "boto3", "aws-sdk-go", "aws-sdk-go-v2"},
		EnvHints: []string{"SQS_QUEUE_URL", "AWS_SQS_QUEUE_URL"},
	},
	{
		Name: "Amazon SNS", Hosts: []string{"sns.*.amazonaws.com"}, Mode: "block",
		Why: "A publish reaches every real subscriber, and some of those subscribers are a phone " +
			"number and an email address.",
		Packages: []string{"@aws-sdk/client-sns", "aws-sdk", "boto3", "aws-sdk-go", "aws-sdk-go-v2"},
		EnvHints: []string{"SNS_TOPIC_ARN", "AWS_SNS_TOPIC_ARN"},
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
	},
	{
		Name: "Amazon Kinesis", Hosts: []string{"kinesis.*.amazonaws.com"}, Mode: "block",
		Why: "Records put onto a real stream are read by production consumers and cannot be taken " +
			"back off it.",
		Packages: []string{"@aws-sdk/client-kinesis", "aws-sdk", "boto3", "aws-sdk-go", "aws-sdk-go-v2"},
		EnvHints: []string{"KINESIS_STREAM_NAME"},
	},
	{
		Name: "Amazon EventBridge", Hosts: []string{"events.*.amazonaws.com"}, Mode: "block",
		Why: "An event on the real bus fans out to every production rule that matches it, and the " +
			"targets are whatever those rules point at.",
		Packages: []string{"@aws-sdk/client-eventbridge", "aws-sdk", "boto3", "aws-sdk-go", "aws-sdk-go-v2"},
		EnvHints: []string{"EVENT_BUS_NAME", "EVENTBRIDGE_BUS_NAME"},
	},
	{
		Name: "AWS Secrets Manager", Hosts: []string{"secretsmanager.*.amazonaws.com"}, Mode: "block",
		Why: "Reading it hands a production credential to an environment running unreviewed code " +
			"against a copy of production data, which is the one thing this product exists to stop.",
		Packages: []string{"@aws-sdk/client-secrets-manager", "aws-sdk", "boto3", "aws-sdk-go", "aws-sdk-go-v2"},
		EnvHints: []string{"AWS_SECRET_NAME", "SECRETS_MANAGER_SECRET_ID"},
	},
	{
		Name: "AWS Systems Manager Parameter Store", Hosts: []string{"ssm.*.amazonaws.com"}, Mode: "block",
		Why: "Parameter Store holds production configuration and, through SecureString, production " +
			"credentials, so it is refused for the same reason Secrets Manager is.",
		Packages: []string{"@aws-sdk/client-ssm", "aws-sdk", "boto3", "aws-sdk-go", "aws-sdk-go-v2"},
		EnvHints: []string{"SSM_PARAMETER_PATH", "AWS_SSM_PATH"},
	},
	{
		Name: "AWS STS", Hosts: []string{"sts.amazonaws.com", "sts.*.amazonaws.com"}, Mode: "block",
		Why: "STS mints credentials. An environment that can call it can hold a production role for " +
			"an hour, and no rule about any other host applies to what it does with one.",
		Packages: []string{"@aws-sdk/client-sts", "aws-sdk", "boto3", "aws-sdk-go", "aws-sdk-go-v2"},
		EnvHints: []string{"AWS_ROLE_ARN", "AWS_WEB_IDENTITY_TOKEN_FILE"},
	},
	{
		Name: "Google Cloud Storage", Hosts: []string{"storage.googleapis.com"}, Mode: "block",
		Why: "An object written from a preview environment lands in the real bucket. Google ships no " +
			"official Cloud Storage emulator, which is why this is a refusal rather than a redirect.",
		Packages: []string{"@google-cloud/storage", "google-cloud-storage", "gcs-resumable-upload"},
		EnvHints: []string{"GCS_BUCKET", "GOOGLE_CLOUD_STORAGE_BUCKET"},
	},
	{
		Name: "Google Cloud Pub/Sub", Hosts: []string{"pubsub.googleapis.com"}, Mode: "block",
		Why:      "A message published to a real topic is delivered to production subscribers.",
		Packages: []string{"@google-cloud/pubsub", "google-cloud-pubsub"},
		EnvHints: []string{"PUBSUB_TOPIC", "GOOGLE_PUBSUB_TOPIC"},
	},
	{
		Name: "Google Cloud Firestore", Hosts: []string{"firestore.googleapis.com"}, Mode: "block",
		Why:      "This is a production datastore, and an environment writing to it is writing to production.",
		Packages: []string{"@google-cloud/firestore", "google-cloud-firestore", "firebase-admin"},
		EnvHints: []string{"FIRESTORE_PROJECT_ID", "FIRESTORE_EMULATOR_HOST"},
	},
	{
		Name: "Google Secret Manager", Hosts: []string{"secretmanager.googleapis.com"}, Mode: "block",
		Why: "Reading it hands a production credential to an environment running unreviewed code " +
			"against a copy of production data.",
		Packages: []string{"@google-cloud/secret-manager", "google-cloud-secret-manager"},
		EnvHints: []string{"GOOGLE_SECRET_NAME", "SECRET_MANAGER_PROJECT"},
	},
	{
		Name: "Google Cloud Tasks", Hosts: []string{"cloudtasks.googleapis.com"}, Mode: "block",
		Why: "A task enqueued on a real queue is dispatched to a production handler, at a time nobody " +
			"is watching for it.",
		Packages: []string{"@google-cloud/tasks", "google-cloud-tasks"},
		EnvHints: []string{"CLOUD_TASKS_QUEUE", "GOOGLE_CLOUD_TASKS_QUEUE"},
	},
	{
		Name: "Azure Blob Storage", Hosts: []string{"*.blob.core.windows.net"}, Mode: "block",
		Why: "An object written from a preview environment lands in the real container, where nothing " +
			"tells it apart from production data afterwards.",
		Packages: []string{"@azure/storage-blob", "azure-storage-blob", "azure-storage"},
		EnvHints: []string{"AZURE_STORAGE_ACCOUNT", "AZURE_STORAGE_CONNECTION_STRING"},
	},
	{
		Name: "Azure Queue Storage", Hosts: []string{"*.queue.core.windows.net"}, Mode: "block",
		Why:      "A message sent to a real queue is picked up by production workers.",
		Packages: []string{"@azure/storage-queue", "azure-storage-queue"},
		EnvHints: []string{"AZURE_QUEUE_NAME"},
	},
	{
		Name: "Azure Service Bus", Hosts: []string{"*.servicebus.windows.net"}, Mode: "block",
		Why: "A message on a real topic or queue is delivered to production subscribers. The Microsoft " +
			"emulator that answers for this needs an MSSQL container beside it, which is weight an " +
			"environment pays for on purpose rather than by default.",
		Packages: []string{"@azure/service-bus", "azure-servicebus"},
		EnvHints: []string{"SERVICEBUS_CONNECTION_STRING", "AZURE_SERVICEBUS_NAMESPACE"},
	},
	{
		Name: "Azure Table Storage", Hosts: []string{"*.table.core.windows.net"}, Mode: "block",
		Why:      "This is a production datastore, and an environment writing to it is writing to production.",
		Packages: []string{"@azure/data-tables", "azure-data-tables"},
		EnvHints: []string{"AZURE_TABLE_NAME"},
	},
	{
		Name: "Azure Files", Hosts: []string{"*.file.core.windows.net"}, Mode: "block",
		Why: "A file written from a preview environment lands in the real share, where nothing tells " +
			"it apart from production data afterwards.",
		Packages: []string{"@azure/storage-file-share", "azure-storage-file-share"},
		EnvHints: []string{"AZURE_FILE_SHARE_NAME"},
	},
	{
		Name: "Azure Cosmos DB", Hosts: []string{"*.documents.azure.com"}, Mode: "block",
		Why:      "This is a production datastore, and an environment writing to it is writing to production.",
		Packages: []string{"@azure/cosmos", "azure-cosmos"},
		EnvHints: []string{"COSMOS_ENDPOINT", "AZURE_COSMOS_CONNECTION_STRING"},
	},
	{
		Name: "Azure Key Vault", Hosts: []string{"*.vault.azure.net"}, Mode: "block",
		Why: "Reading it hands a production credential to an environment running unreviewed code " +
			"against a copy of production data.",
		Packages: []string{"@azure/keyvault-secrets", "@azure/keyvault-keys", "azure-keyvault-secrets"},
		EnvHints: []string{"AZURE_KEY_VAULT_URL", "KEY_VAULT_NAME"},
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
