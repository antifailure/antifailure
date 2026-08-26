package manifest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
	"gopkg.in/yaml.v3"
	"pgregory.net/rapid"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

// minimal is the smallest manifest that validates. Tests build on it so that
// each one shows only what it is about.
const minimal = `
version: 1
name: shop
services:
  - name: web
    port: 3000
`

func parse(t *testing.T, body string) (*schema.Manifest, error) {
	t.Helper()
	return manifest.Parse([]byte(body), "antifailure.yaml", "")
}

func mustParse(t *testing.T, body string) *schema.Manifest {
	t.Helper()
	m, err := parse(t, body)
	require.NoError(t, err)
	return m
}

func problems(t *testing.T, err error) []manifest.Problem {
	t.Helper()
	require.Error(t, err)
	var e *manifest.Errors
	require.ErrorAs(t, err, &e, "expected validation problems, got %v", err)
	return e.Problems
}

func messages(ps []manifest.Problem) string {
	var b strings.Builder
	for _, p := range ps {
		b.WriteString(p.String())
		b.WriteString("\n")
	}
	return b.String()
}

func TestParse_MinimalManifestValidates(t *testing.T) {
	t.Parallel()
	m := mustParse(t, minimal)
	require.Equal(t, 1, m.Version)
	require.Equal(t, "shop", m.Name)
	require.Len(t, m.Services, 1)
}

// The single most important validation rule. A typo that is silently ignored
// produces an environment that is subtly not what the user asked for, and they
// find out in production.
func TestParse_UnknownKeyIsAnErrorWithALineAndASuggestion(t *testing.T) {
	t.Parallel()
	_, err := parse(t, `
version: 1
services:
  - name: web
    port: 3000
    healthpath: /healthz
`)
	ps := problems(t, err)
	require.Len(t, ps, 1)
	require.Contains(t, ps[0].Message, "healthpath")
	require.Contains(t, ps[0].Hint, "health_path", "a near miss must be suggested")
	require.Equal(t, 6, ps[0].Line, "the problem must name the line to edit")
}

func TestParse_UnknownTopLevelKeyIsRejected(t *testing.T) {
	t.Parallel()
	_, err := parse(t, minimal+"\nsevices: []\n")
	ps := problems(t, err)
	require.Contains(t, messages(ps), "sevices")
	require.Contains(t, messages(ps), "services")
}

func TestParse_CollectsEveryProblemAtOnce(t *testing.T) {
	t.Parallel()
	// Fixing a manifest by rerunning the command once per problem is an
	// experience a validator can trivially avoid.
	_, err := parse(t, `
version: 1
name: shop
services:
  - name: web
    kind: web
  - name: web
    kind: web
    port: 3000
  - name: api
    kind: cron
`)
	ps := problems(t, err)
	require.GreaterOrEqual(t, len(ps), 4, "got:\n%s", messages(ps))
	all := messages(ps)
	require.Contains(t, all, "no port")
	require.Contains(t, all, `Two services are both named "web"`)
	require.Contains(t, all, "no schedule")
	require.Contains(t, all, "no command")
}

func TestParse_ProblemsAreOrderedByLine(t *testing.T) {
	t.Parallel()
	_, err := parse(t, `
version: 1
name: shop
services:
  - name: alpha
    kind: web
  - name: beta
    kind: web
`)
	ps := problems(t, err)
	require.GreaterOrEqual(t, len(ps), 2)
	for i := 1; i < len(ps); i++ {
		require.LessOrEqual(t, ps[i-1].Line, ps[i].Line, "problems must be ordered by line")
	}
}

func TestParse_RejectsANewerSchemaVersion(t *testing.T) {
	t.Parallel()
	_, err := parse(t, "version: 99\nservices:\n  - name: web\n    port: 3000\n")
	require.ErrorIs(t, err, aferrors.Coded(aferrors.AFMAN003))
	require.Contains(t, err.Error(), "99")
}

func TestParse_AssumesVersionOneWhenAbsent(t *testing.T) {
	t.Parallel()
	// The field has only ever had one value. Refusing would be pedantic.
	m := mustParse(t, "name: shop\nservices:\n  - name: web\n    port: 3000\n")
	require.Equal(t, 1, m.Version)
}

func TestParse_RejectsVersionZeroAndNegative(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"0", "-3"} {
		_, err := parse(t, "version: "+v+"\nservices:\n  - name: web\n    port: 3000\n")
		require.Error(t, err, "version %s must be rejected", v)
	}
}

func TestParse_RejectsAnEmptyDocument(t *testing.T) {
	t.Parallel()
	_, err := parse(t, "")
	ps := problems(t, err)
	require.Contains(t, ps[0].Message, "empty")
	require.Contains(t, ps[0].Hint, "af init")
}

// YAML anchors let a small document expand into a very large one. A manifest
// has no legitimate use for them.
func TestParse_RejectsAnchorsAndAliases(t *testing.T) {
	t.Parallel()
	_, err := parse(t, `
version: 1
name: shop
x: &base
  a: b
services:
  - name: web
    port: 3000
    env: *base
`)
	require.Error(t, err)
	require.Contains(t, err.Error(), "anchors and aliases")
}

func TestParse_RejectsDuplicateKeys(t *testing.T) {
	t.Parallel()
	_, err := parse(t, "version: 1\nname: a\nname: b\nservices:\n  - name: web\n    port: 3000\n")
	require.Error(t, err)
}

func TestParse_RejectsAManifestAboveTheSizeLimit(t *testing.T) {
	t.Parallel()
	big := minimal + "\n# " + strings.Repeat("x", manifest.MaxSize)
	_, err := parse(t, big)
	require.ErrorIs(t, err, aferrors.Coded(aferrors.AFMAN005))
}

func TestParse_RejectsExcessiveNesting(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	b.WriteString("version: 1\ndeep:\n")
	for i := 0; i < 60; i++ {
		b.WriteString(strings.Repeat("  ", i+1) + "a:\n")
	}
	b.WriteString(strings.Repeat("  ", 61) + "b: c\n")
	_, err := parse(t, b.String())
	require.Error(t, err)
}

// Path confinement. A manifest is committed and shared, so a path that escapes
// the repository or depends on one machine's layout is refused.
func TestParse_RejectsPathsOutsideTheRepository(t *testing.T) {
	t.Parallel()
	for _, p := range []string{"../secrets", "/etc", "../../../root", "a/../../b"} {
		_, err := parse(t, "version: 1\nname: s\nservices:\n  - name: web\n    port: 3000\n    path: "+p+"\n")
		ps := problems(t, err)
		require.Contains(t, messages(ps), "outside the repository", "path %q must be refused", p)
	}
}

func TestParse_AcceptsPathsInsideTheRepository(t *testing.T) {
	t.Parallel()
	m := mustParse(t, "version: 1\nname: s\nservices:\n  - name: web\n    port: 3000\n    path: apps/web\n")
	require.Equal(t, "apps/web", m.Services[0].Path)
}

func TestParse_ReportsAMissingServiceDirectory(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	_, err := manifest.Parse([]byte(
		"version: 1\nname: s\nservices:\n  - name: web\n    port: 3000\n    path: apps/web\n"),
		"antifailure.yaml", root)
	ps := problems(t, err)
	require.Contains(t, messages(ps), "does not exist")

	require.NoError(t, os.MkdirAll(filepath.Join(root, "apps", "web"), 0o755))
	_, err = manifest.Parse([]byte(
		"version: 1\nname: s\nservices:\n  - name: web\n    port: 3000\n    path: apps/web\n"),
		"antifailure.yaml", root)
	require.NoError(t, err)
}

func TestParse_RejectsAPortCollision(t *testing.T) {
	t.Parallel()
	_, err := parse(t, `
version: 1
name: shop
services:
  - name: web
    port: 3000
  - name: api
    port: 3000
`)
	require.Contains(t, messages(problems(t, err)), "both claim port 3000")
}

func TestParse_RejectsADependencyCycle(t *testing.T) {
	t.Parallel()
	_, err := parse(t, `
version: 1
name: shop
services:
  - name: web
    port: 3000
    depends_on: [api]
  - name: api
    port: 3001
    depends_on: [worker]
  - name: worker
    kind: worker
    depends_on: [web]
`)
	msg := messages(problems(t, err))
	require.Contains(t, msg, "cycle")
	require.Contains(t, msg, "Nothing could start first")
}

func TestParse_RejectsASelfDependency(t *testing.T) {
	t.Parallel()
	_, err := parse(t, "version: 1\nname: s\nservices:\n  - name: web\n    port: 3000\n    depends_on: [web]\n")
	require.Contains(t, messages(problems(t, err)), "depends on itself")
}

func TestParse_RejectsAnUndeclaredDependency(t *testing.T) {
	t.Parallel()
	_, err := parse(t, "version: 1\nname: s\nservices:\n  - name: web\n    port: 3000\n    depends_on: [ghost]\n")
	require.Contains(t, messages(problems(t, err)), `depends on "ghost"`)
}

func TestParse_AcceptsAValidDependencyOrder(t *testing.T) {
	t.Parallel()
	mustParse(t, `
version: 1
name: shop
services:
  - name: web
    port: 3000
    depends_on: [api]
  - name: api
    port: 3001
`)
}

// A build argument is recorded in image metadata and readable by anyone who can
// pull the image.
func TestParse_RejectsASecretShapedBuildArgument(t *testing.T) {
	t.Parallel()
	_, err := parse(t, `
version: 1
name: shop
services:
  - name: web
    port: 3000
    build:
      args:
        STRIPE_SECRET_KEY: something
`)
	msg := messages(problems(t, err))
	require.Contains(t, msg, "named like a secret")
	require.Contains(t, msg, "readable by anyone who can pull the image")
}

func TestParse_RejectsACredentialShapedLiteralValue(t *testing.T) {
	t.Parallel()
	// A manifest is committed, so a literal credential in it is a leak.
	body := "version: 1\nname: s\nservices:\n  - name: web\n    port: 3000\n    env:\n      - name: URL\n        value: " +
		"postgres://user:supersecretpassword@db:5432/app\n"
	require.Contains(t, messages(problems(t, mustFail(t, body))), "shaped like a credential")
}

func TestParse_RejectsBothValueAndFrom(t *testing.T) {
	t.Parallel()
	body := "version: 1\nname: s\nservices:\n  - name: web\n    port: 3000\n    env:\n      - name: A\n        value: x\n        from: doppler\n"
	require.Contains(t, messages(problems(t, mustFail(t, body))), "both value and from")
}

func TestParse_RejectsADuplicateEnvironmentVariable(t *testing.T) {
	t.Parallel()
	body := "version: 1\nname: s\nservices:\n  - name: web\n    port: 3000\n    env:\n      - name: A\n      - name: A\n"
	require.Contains(t, messages(problems(t, mustFail(t, body))), "declared twice")
}

func TestParse_WarnsWhenEgressDefaultIsAllow(t *testing.T) {
	t.Parallel()
	body := minimal + "\negress:\n  default: allow\n"
	msg := messages(problems(t, mustFail(t, body)))
	require.Contains(t, msg, "reach the whole internet")
	require.Contains(t, msg, "emails a real customer")
}

func TestParse_RejectsAWildcardRuleThatIsNotBlock(t *testing.T) {
	t.Parallel()
	body := minimal + "\negress:\n  rules:\n    - host: '*'\n      mode: allow\n"
	require.Contains(t, messages(problems(t, mustFail(t, body))), "Only block may match everything")
}

func TestParse_AcceptsAWildcardBlockRule(t *testing.T) {
	t.Parallel()
	mustParse(t, minimal+"\negress:\n  rules:\n    - host: '*'\n      mode: block\n")
}

func TestParse_RejectsASandboxRuleWithNoCredential(t *testing.T) {
	t.Parallel()
	body := minimal + "\negress:\n  rules:\n    - host: api.stripe.com\n      mode: sandbox\n"
	require.Contains(t, messages(problems(t, mustFail(t, body))), "names no credential")
}

func TestParse_ReportsARuleThatCanNeverApply(t *testing.T) {
	t.Parallel()
	body := minimal + `
egress:
  rules:
    - host: api.example.com
      mode: allow
    - host: api.example.com
      mode: block
`
	require.Contains(t, messages(problems(t, mustFail(t, body))), "never applies")
}

func TestParse_ReportsThatSynthNeverProducesAPass(t *testing.T) {
	t.Parallel()
	body := minimal + "\negress:\n  rules:\n    - host: api.example.com\n      mode: synth\n"
	msg := messages(problems(t, mustFail(t, body)))
	require.Contains(t, msg, "unverified rather than pass")
}

func TestParse_RejectsAnInvalidHostPattern(t *testing.T) {
	t.Parallel()
	for _, h := range []string{"not a host", "-leading.com", "trailing-.com", "a..b.com", ".leading"} {
		body := minimal + "\negress:\n  rules:\n    - host: '" + h + "'\n      mode: block\n"
		require.Contains(t, messages(problems(t, mustFail(t, body))), "not a valid hostname",
			"host %q must be rejected", h)
	}
}

func TestParse_AcceptsValidHostPatterns(t *testing.T) {
	t.Parallel()
	for _, h := range []string{"api.stripe.com", "*.stripe.com", "10.0.0.1", "localhost", "api.example.com:8443"} {
		body := minimal + "\negress:\n  rules:\n    - host: '" + h + "'\n      mode: block\n"
		_, err := parse(t, body)
		require.NoError(t, err, "host %q must be accepted", h)
	}
}

func TestParse_RejectsAnInvalidRateLimit(t *testing.T) {
	t.Parallel()
	body := minimal + "\negress:\n  rules:\n    - host: a.example.com\n      mode: allow\n      rate_limit: fast\n"
	require.Contains(t, messages(problems(t, mustFail(t, body))), "not valid")
}

// Invariants run inside a read only transaction, so Postgres refuses a write
// regardless. Catching it at validation means the author finds out when they
// write the manifest rather than when the first workflow finishes.
func TestParse_RejectsAnInvariantThatCouldWrite(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"update":            "UPDATE users SET email = 'x'",
		"delete":            "DELETE FROM users",
		"insert":            "INSERT INTO users (id) VALUES (1)",
		"drop":              "DROP TABLE users",
		"truncate":          "TRUNCATE users",
		"two statements":    "SELECT 1; DELETE FROM users",
		"comment smuggling": "SELECT 1 -- ok\n; DROP TABLE users",
		"function call":     "SELECT pg_sleep(30)",
		"file read":         "SELECT pg_read_file('/etc/passwd')",
		"set":               "SET session_replication_role = replica",
		"not a query":       "VACUUM FULL",
	}
	for name, sql := range cases {
		body := minimal + "\ninvariants:\n  - name: check\n    sql: \"" + strings.ReplaceAll(sql, "\n", "\\n") + "\"\n"
		require.Contains(t, messages(problems(t, mustFail(t, body))), "not read only",
			"invariant %s must be rejected", name)
	}
}

func TestParse_AcceptsReadOnlyInvariants(t *testing.T) {
	t.Parallel()
	// Including ones whose column names contain forbidden keywords, which a
	// naive substring check would reject.
	cases := []string{
		"SELECT id FROM orders WHERE total < 0",
		"WITH bad AS (SELECT 1) SELECT * FROM bad",
		"SELECT id, created_at, updated_at FROM users WHERE deleted_at IS NULL AND id > 0",
		"SELECT 1 FROM subscriptions s LEFT JOIN customers c ON c.id = s.customer_id WHERE c.id IS NULL",
		"SELECT id FROM t; ",
	}
	for _, sql := range cases {
		body := minimal + "\ninvariants:\n  - name: check\n    sql: \"" + sql + "\"\n"
		_, err := parse(t, body)
		require.NoError(t, err, "invariant %q must be accepted", sql)
	}
}

func TestParse_RejectsAWorkflowNamingAnUnknownPersona(t *testing.T) {
	t.Parallel()
	body := minimal + `
personas:
  - name: owner
workflows:
  - name: sign-up
    description: Create an account and confirm the welcome email arrives.
    persona: ghost
`
	require.Contains(t, messages(problems(t, mustFail(t, body))), "not a declared persona")
}

func TestParse_RejectsAWorkflowWithNoPersonaAtAll(t *testing.T) {
	t.Parallel()
	body := minimal + `
workflows:
  - name: sign-up
    description: Create an account and confirm the welcome email arrives.
`
	require.Contains(t, messages(problems(t, mustFail(t, body))), "names no persona")
}

func TestParse_RejectsATooShortWorkflowDescription(t *testing.T) {
	t.Parallel()
	body := minimal + "\npersonas:\n  - name: owner\nworkflows:\n  - name: w\n    description: do it now\n"
	require.Contains(t, messages(problems(t, mustFail(t, body))), "too short to plan from")
}

func TestParse_RejectsPersonasSharingAnAddress(t *testing.T) {
	t.Parallel()
	body := minimal + "\npersonas:\n  - name: a\n    email: same@example.test\n  - name: b\n    email: same@example.test\n"
	msg := messages(problems(t, mustFail(t, body)))
	require.Contains(t, msg, "share the address")
	require.Contains(t, msg, "routes messages by recipient")
}

func TestParse_RejectsAnInvalidCronExpression(t *testing.T) {
	t.Parallel()
	for _, expr := range []string{"* * *", "99 * * * *", "* 25 * * *", "not a cron", "*/0 * * * *"} {
		body := "version: 1\nname: s\nservices:\n  - name: job\n    kind: cron\n    command: run\n    schedule: \"" + expr + "\"\n"
		require.Contains(t, messages(problems(t, mustFail(t, body))), "not valid",
			"cron %q must be rejected", expr)
	}
}

func TestParse_AcceptsValidCronExpressions(t *testing.T) {
	t.Parallel()
	for _, expr := range []string{"0 3 * * *", "*/15 * * * *", "0 0 1 1 0", "0,30 * * * 1-5", "CRON_TZ=Europe/London 0 3 * * *"} {
		body := "version: 1\nname: s\nservices:\n  - name: job\n    kind: cron\n    command: run\n    schedule: \"" + expr + "\"\n"
		_, err := parse(t, body)
		require.NoError(t, err, "cron %q must be accepted", expr)
	}
}

func TestParse_RejectsBothASourceAndASeed(t *testing.T) {
	t.Parallel()
	body := minimal + "\ndatabase:\n  source_url_env: PROD_URL\n  seed: npm run seed\n"
	require.Contains(t, messages(problems(t, mustFail(t, body))), "not both")
}

func TestParse_RejectsRemoteGoldenStorageWithNoURL(t *testing.T) {
	t.Parallel()
	body := minimal + "\ndatabase:\n  golden:\n    storage: azure_blob\n"
	require.Contains(t, messages(problems(t, mustFail(t, body))), "no URL is given")
}

func TestParse_RejectsSubsettingWithNoSeedTable(t *testing.T) {
	t.Parallel()
	body := minimal + "\ndatabase:\n  subset:\n    enabled: true\n"
	require.Contains(t, messages(problems(t, mustFail(t, body))), "no seed table")
}

func TestParse_RejectsALoadDurationAboveTheCap(t *testing.T) {
	t.Parallel()
	body := minimal + "\nload:\n  enabled: true\n  duration: 60m\n"
	require.Contains(t, messages(problems(t, mustFail(t, body))), "fifteen minute cap")
}

func TestParse_RejectsARouteListedAsBothSafeAndUnsafe(t *testing.T) {
	t.Parallel()
	body := minimal + "\nload:\n  enabled: true\n  safe_routes: [/api/items]\n  unsafe_routes: [/api/items]\n"
	require.Contains(t, messages(problems(t, mustFail(t, body))), "both safe and unsafe")
}

func TestParse_RejectsKubernetesWithTheLocalhostDomain(t *testing.T) {
	t.Parallel()
	body := minimal + "\nruntime:\n  provider: kubernetes\n"
	require.Contains(t, messages(problems(t, mustFail(t, body))), "still localhost")
}

func mustFail(t *testing.T, body string) error {
	t.Helper()
	_, err := parse(t, body)
	require.Error(t, err, "expected this manifest to be rejected:\n%s", body)
	return err
}

func TestNormalize_AppliesEveryDefault(t *testing.T) {
	t.Parallel()
	m := mustParse(t, minimal)

	require.Equal(t, schema.ServiceWeb, m.Services[0].Kind)
	require.Equal(t, "/", m.Services[0].HealthPath)
	require.Equal(t, "180s", m.Services[0].HealthTimeout)
	require.Equal(t, 1, m.Services[0].Replicas)
	require.Equal(t, "1", m.Services[0].Resources.CPU)
	require.Equal(t, "1Gi", m.Services[0].Resources.Memory)
	require.Equal(t, schema.BuildAuto, m.Services[0].Build.Strategy)

	require.Equal(t, schema.DBDocker, m.Database.Provider)
	require.Equal(t, 17, m.Database.Version)
	require.Equal(t, "DATABASE_URL", m.Database.URLEnv)
	require.Equal(t, "masking.yaml", m.Database.MaskingRules)
	require.Equal(t, 5, m.Database.Golden.Retain)

	// The one default that is a promise rather than a convenience.
	require.Equal(t, schema.ModeBlock, m.Egress.Default)
	require.False(t, m.Egress.AllowIPv6)

	require.Equal(t, schema.RuntimeLocal, m.Runtime.Provider)
	require.Equal(t, "localhost", m.Runtime.Domain)
	require.Equal(t, "168h", m.Runtime.TTL)
	require.Equal(t, schema.GitHubActions, m.GitHub.Mode)
	require.Equal(t, schema.ForkLabel, m.GitHub.ForkPolicy,
		"a fork must not run with this environment's credentials by default")
	require.True(t, *m.Insights.Enabled)
}

func TestNormalize_DefaultsPersonaEmailToAReservedDomain(t *testing.T) {
	t.Parallel()
	m := mustParse(t, minimal+"\npersonas:\n  - name: owner\n")
	// example.test is reserved by RFC 6761 and can never receive mail, so a
	// persona address cannot become a real one by accident.
	require.Equal(t, "owner@example.test", m.Personas[0].Email)
}

func TestNormalize_DefaultsWorkflowPersonaToTheFirst(t *testing.T) {
	t.Parallel()
	m := mustParse(t, minimal+`
personas:
  - name: owner
  - name: member
workflows:
  - name: sign-up
    description: Create an account and confirm the welcome email arrives.
`)
	require.Equal(t, "owner", m.Workflows[0].Persona)
	require.Equal(t, "/", m.Workflows[0].StartPath)
	require.Equal(t, 60, m.Workflows[0].Budget.Steps)
}

func TestNormalize_IsIdempotent(t *testing.T) {
	t.Parallel()
	// Running normalization twice must change nothing, or a manifest would
	// mean something different depending on how many times it was loaded.
	rapid.Check(t, func(rt *rapid.T) {
		body := genManifestYAML(rt)
		first, err := manifest.Parse([]byte(body), "antifailure.yaml", "")
		if err != nil {
			return // an invalid manifest is not what this property is about
		}
		out, err := yaml.Marshal(first)
		if err != nil {
			rt.Fatalf("marshal: %v", err)
		}
		second, err := manifest.Parse(out, "antifailure.yaml", "")
		if err != nil {
			rt.Fatalf("a normalized manifest failed to reparse: %v\n%s", err, out)
		}
		again, err := yaml.Marshal(second)
		if err != nil {
			rt.Fatalf("marshal: %v", err)
		}
		if string(out) != string(again) {
			rt.Fatalf("normalization is not idempotent:\n--- first ---\n%s\n--- second ---\n%s", out, again)
		}
	})
}

func genManifestYAML(rt *rapid.T) string {
	var b strings.Builder
	b.WriteString("version: 1\nname: app\nservices:\n")
	n := rapid.IntRange(1, 3).Draw(rt, "services")
	for i := 0; i < n; i++ {
		kind := rapid.SampledFrom([]string{"web", "worker"}).Draw(rt, "kind")
		b.WriteString("  - name: svc" + string(rune('a'+i)) + "\n")
		b.WriteString("    kind: " + kind + "\n")
		if kind == "web" {
			b.WriteString("    port: " + itoa(3000+i) + "\n")
		}
		if rapid.Bool().Draw(rt, "haspath") {
			b.WriteString("    path: apps/svc" + string(rune('a'+i)) + "\n")
		}
	}
	if rapid.Bool().Draw(rt, "egress") {
		b.WriteString("egress:\n  rules:\n    - host: api.example.com\n      mode: block\n")
	}
	if rapid.Bool().Draw(rt, "persona") {
		b.WriteString("personas:\n  - name: owner\n")
	}
	return b.String()
}

func itoa(n int) string {
	return string(rune('0'+n/1000)) + string(rune('0'+(n/100)%10)) +
		string(rune('0'+(n/10)%10)) + string(rune('0'+n%10))
}

func TestParseDuration(t *testing.T) {
	t.Parallel()
	cases := map[string]time.Duration{
		"30s":   30 * time.Second,
		"5m":    5 * time.Minute,
		"168h":  168 * time.Hour,
		"7d":    7 * 24 * time.Hour,
		"500ms": 500 * time.Millisecond,
	}
	for in, want := range cases {
		got, err := manifest.ParseDuration(in)
		require.NoError(t, err, in)
		require.Equal(t, want, got, in)
	}
	for _, bad := range []string{"", "7 days", "d", "abc", "1y"} {
		_, err := manifest.ParseDuration(bad)
		require.Error(t, err, "%q must be rejected", bad)
	}
}

func TestParseRate(t *testing.T) {
	t.Parallel()
	n, unit, err := manifest.ParseRate("600/m")
	require.NoError(t, err)
	require.Equal(t, 600, n)
	require.Equal(t, "m", unit)
	for _, bad := range []string{"", "/s", "10/", "0/s", "-1/s", "10/d", "ten/s"} {
		_, _, err := manifest.ParseRate(bad)
		require.Error(t, err, "%q must be rejected", bad)
	}
}

func TestFind_WalksUpFromASubdirectory(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, manifest.FileName), []byte(minimal), 0o600))
	deep := filepath.Join(root, "apps", "web", "src")
	require.NoError(t, os.MkdirAll(deep, 0o755))

	// Commands are run from wherever the developer happens to be.
	got, err := manifest.Find(deep)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(root, manifest.FileName), got)
}

func TestFind_ReportsAF_MAN_001WhenThereIsNone(t *testing.T) {
	t.Parallel()
	_, err := manifest.Find(t.TempDir())
	require.ErrorIs(t, err, aferrors.Coded(aferrors.AFMAN001))

	// The message names what is missing; the next step names the fix. The
	// command boundary prints both, so both have to carry their half.
	var coded *aferrors.Error
	require.ErrorAs(t, err, &coded)
	require.Contains(t, coded.Message(), "No antifailure.yaml was found")
	require.Contains(t, coded.NextStep(), "af init")
}

func TestLoad_ReadsFromDisk(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, manifest.FileName)
	require.NoError(t, os.WriteFile(path, []byte(minimal), 0o600))

	m, err := manifest.Load(path)
	require.NoError(t, err)
	require.Equal(t, "shop", m.Name)
}

func TestLoad_ReportsAMissingFile(t *testing.T) {
	t.Parallel()
	_, err := manifest.Load(filepath.Join(t.TempDir(), manifest.FileName))
	require.ErrorIs(t, err, aferrors.Coded(aferrors.AFMAN001))
}

func TestLoad_ReportsAFileAboveTheSizeLimit(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), manifest.FileName)
	require.NoError(t, os.WriteFile(path, make([]byte, manifest.MaxSize+1), 0o600))
	_, err := manifest.Load(path)
	require.ErrorIs(t, err, aferrors.Coded(aferrors.AFMAN005))
}

func TestExplain_RendersEverySection(t *testing.T) {
	t.Parallel()
	m := mustParse(t, `
version: 1
name: shop
services:
  - name: web
    port: 3000
    migrate: npx prisma migrate deploy
    env:
      - name: STRIPE_KEY
        sandbox: true
  - name: worker
    kind: worker
  - name: nightly
    kind: cron
    command: node scripts/nightly.js
    schedule: 0 3 * * *
database:
  provider: docker
  source_url_env: PROD_DATABASE_URL
egress:
  rules:
    - host: api.stripe.com
      mode: sandbox
      credential: STRIPE_KEY
      note: Billing runs against the Stripe sandbox.
personas:
  - name: owner
    role: admin
workflows:
  - name: subscribe
    description: Sign in, choose the pro plan, complete checkout, and confirm the plan shows as pro.
invariants:
  - name: no-orphan-subscriptions
    sql: SELECT s.id FROM subscriptions s LEFT JOIN customers c ON c.id = s.customer_id WHERE c.id IS NULL
    description: Every subscription belongs to a customer.
load:
  enabled: true
  source: access_log
`)
	out := manifest.Explain(m)
	for _, want := range []string{
		"Application  shop", "web", "worker", "nightly", "0 3 * * *",
		"npx prisma migrate deploy", "STRIPE_KEY (sandbox)",
		"PROD_DATABASE_URL", "never stored",
		"api.stripe.com", "sandbox", "Billing runs against the Stripe sandbox.",
		"owner@example.test", "subscribe", "no-orphan-subscriptions",
		"Insights", "Runtime", "GitHub", "antifailure:allow", "Load",
	} {
		require.Contains(t, out, want, "Explain must mention %q", want)
	}
	require.NotContains(t, out, "—", "prose must not use an em dash")
}

func TestExplain_SaysWhenThereAreNoRules(t *testing.T) {
	t.Parallel()
	out := manifest.Explain(mustParse(t, minimal))
	require.Contains(t, out, "no rules, so every outbound request is refused")
	require.Contains(t, out, "subset       off")
}

func TestSummary(t *testing.T) {
	t.Parallel()
	m := mustParse(t, `
version: 1
name: shop
services:
  - name: web
    port: 3000
  - name: api
    port: 3001
  - name: worker
    kind: worker
personas:
  - name: owner
workflows:
  - name: subscribe
    description: Sign in and complete checkout, then confirm the plan shows as pro.
`)
	require.Equal(t, "shop: 2 webs, 1 worker, docker database, 1 workflow", manifest.Summary(m))
}

func TestHosts_ListsEveryHostSorted(t *testing.T) {
	t.Parallel()
	m := mustParse(t, `
version: 1
name: shop
services:
  - name: web
    port: 3000
    build:
      allow_hosts: [registry.npmjs.org]
egress:
  rules:
    - host: api.stripe.com
      mode: block
    - host: '*.sendgrid.com'
      mode: capture
`)
	require.Equal(t, []string{"*.sendgrid.com", "api.stripe.com", "registry.npmjs.org"}, manifest.Hosts(m))
}

// The alias scan is bounded so that a hostile document cannot make validation
// itself the denial of service. But an unfinished scan cannot claim there are
// no aliases, and the decoder that runs next expands them. Refusing an
// unscannable document is what keeps the alias check meaningful.
func TestParse_RefusesADocumentTooLargeToScanForAliases(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	b.WriteString("version: 1\nname: app\nbig:\n")
	for i := 0; i < 60000; i++ {
		b.WriteString("  - a\n")
	}
	_, err := parse(t, b.String())
	require.Error(t, err)
	require.Contains(t, err.Error(), "more than")
	require.Contains(t, err.Error(), "nodes")
}

func TestParse_ReportsAtMostAFixedNumberOfProblems(t *testing.T) {
	t.Parallel()
	// Nobody reads the two hundredth message, and locating each one walks the
	// document, so an unbounded report is quadratic as well as unhelpful.
	var b strings.Builder
	b.WriteString("version: 1\nname: app\nservices:\n")
	for i := 0; i < 200; i++ {
		b.WriteString("  - name: svc\n    kind: web\n")
	}
	_, err := parse(t, b.String())
	ps := problems(t, err)
	require.LessOrEqual(t, len(ps), 41)
	require.Contains(t, messages(ps), "more problems, not listed")
}
