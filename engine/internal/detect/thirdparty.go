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
		Name: "Amazon SES", Hosts: []string{"email.us-east-1.amazonaws.com", "*.amazonaws.com"}, Mode: "capture",
		Why:      "Mail is captured into the inbox so that agents can read it and no real address receives anything.",
		Packages: []string{"@aws-sdk/client-ses", "@aws-sdk/client-sesv2", "aws-sdk"},
		EnvHints: []string{"AWS_ACCESS_KEY_ID", "AWS_SES_REGION"},
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
