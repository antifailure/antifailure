package detect_test

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// The lane's number: the manifest fields af init gets right on a real
// repository, out of the total.
//
// The denominator is a manifest a person would write for the fixture below,
// written by hand from the schema rather than from anything detection
// produces, and it is checked in beside the fixture so the figure can be
// reproduced and argued with. The numerator is the fields the draft agrees
// with, compared leaf by leaf.
//
// It is a measurement rather than a threshold. It logs every field it got
// wrong, by name, because a fraction with no list behind it is the kind of
// round unsourced number this repository bans, and because the misses are the
// backlog: each one names a thing detection could learn next.

// insightRepo is a cloud analytics application, of the shape this lane exists
// for: a Next.js front end, Postgres for records, ClickHouse for events, Redis
// for cache, Kafka for the bus, LocalStack standing in for AWS, and Stripe.
func insightRepo() map[string]string {
	return map[string]string{
		"package.json": `{
  "name": "insight",
  "scripts": {"dev": "next dev", "build": "next build", "start": "next start"},
  "dependencies": {
    "next": "15.0.0",
    "react": "19.0.0",
    "@prisma/client": "6.0.0",
    "@clickhouse/client": "1.0.0",
    "stripe": "17.0.0",
    "@aws-sdk/client-s3": "3.600.0",
    "@aws-sdk/client-sqs": "3.600.0"
  }
}`,
		"prisma/schema.prisma": `datasource db {
  provider = "postgresql"
  url      = env("DATABASE_URL")
}

model Account {
  id    String @id
  email String @unique
}
`,
		"Dockerfile": `FROM node:20-alpine
WORKDIR /app
COPY . .
RUN npm ci && npm run build
EXPOSE 3000
CMD ["npm", "start"]
`,
		"docker-compose.yml": `services:
  web:
    build: .
    ports:
      - "3000:3000"
    environment:
      DATABASE_URL: postgres://postgres@db:5432/insight
      CLICKHOUSE_URL: http://events:8123
    depends_on:
      - db
      - events
  db:
    image: postgres:16
  events:
    image: clickhouse/clickhouse-server:24.3
  cache:
    image: redis:7
  broker:
    image: confluentinc/cp-kafka:7.6.0
  localstack:
    image: localstack/localstack:3.4
    environment:
      SERVICES: s3,sqs
`,
		".env.example": "DATABASE_URL=\nCLICKHOUSE_URL=\nSTRIPE_SECRET_KEY=\nAWS_S3_BUCKET=\n",
	}
}

// insightManifest is the manifest a person would write for that repository.
//
// Written from pkg/schema and from what the fixture says, NOT from detection's
// output, which is the only thing that makes the fraction mean anything. Two
// blocks are in it that detection cannot possibly derive from a repository,
// personas and workflows, and they are counted like everything else: af init
// discloses them as guesses, and a guess disclosed is still not a field it got
// right. The figure without them is logged too, so both readings are on the
// record.
const insightManifest = `
version: 1
name: insight
services:
  - name: web
    kind: web
    path: .
    port: 3000
    command: npm start
    build:
      strategy: dockerfile
      dockerfile: Dockerfile
      context: .
database:
  engine: postgres
  present: true
  source_url_env: DATABASE_URL
  migrations:
    tool: prisma
datastores:
  - name: events
    engine: clickhouse
    stance: golden
    source_url_env: CLICKHOUSE_URL
  - name: cache
    engine: redis
    stance: empty
  - name: broker
    engine: kafka
    stance: topics_only
egress:
  default: block
  rules:
    - host: api.stripe.com
      mode: sandbox
      credential: STRIPE_SECRET_KEY
    - host: files.stripe.com
      mode: sandbox
      credential: STRIPE_SECRET_KEY
    - host: checkout.stripe.com
      mode: sandbox
      credential: STRIPE_SECRET_KEY
    - host: s3.amazonaws.com
      mode: block
    - host: s3.*.amazonaws.com
      mode: block
    - host: "*.s3.amazonaws.com"
      mode: block
    - host: "*.s3.*.amazonaws.com"
      mode: block
    - host: sqs.*.amazonaws.com
      mode: block
personas:
  - name: analyst
    email: analyst@example.test
    role: member
    login: password
  - name: admin
    email: admin@example.test
    role: admin
    login: password
workflows:
  - name: view-dashboard
    persona: analyst
`

// flatten turns a manifest into leaf paths. A list whose elements carry a name
// or a host is keyed by it rather than by position, because a draft missing
// one rule would otherwise shift every later index and read as eight misses
// instead of one.
func flatten(prefix string, v any, out map[string]string) {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			flatten(join(prefix, k), val, out)
		}
	case []any:
		for i, el := range t {
			key := fmt.Sprintf("[%d]", i)
			if m, ok := el.(map[string]any); ok {
				for _, id := range []string{"name", "host"} {
					if s, ok := m[id].(string); ok && s != "" {
						key = "[" + s + "]"
						break
					}
				}
			}
			flatten(prefix+key, el, out)
		}
	case nil:
	default:
		out[prefix] = fmt.Sprintf("%v", t)
	}
}

func join(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}

func flattenYAML(t *testing.T, body []byte) map[string]string {
	t.Helper()
	var generic map[string]any
	require.NoError(t, yaml.Unmarshal(body, &generic))
	out := map[string]string{}
	flatten("", generic, out)
	return out
}

// TestTheNumber_ManifestFieldsAfInitGetsRight is the lane's measurement.
func TestTheNumber_ManifestFieldsAfInitGetsRight(t *testing.T) {
	t.Parallel()

	want := flattenYAML(t, []byte(insightManifest))
	res := run(t, "insight", insightRepo())
	draftBody, err := yaml.Marshal(res.Draft)
	require.NoError(t, err)
	got := flattenYAML(t, draftBody)

	var right, wrong []string
	for path, value := range want {
		if got[path] == value {
			right = append(right, path)
		} else {
			wrong = append(wrong, fmt.Sprintf("%s: want %q, got %q", path, value, got[path]))
		}
	}
	sort.Strings(right)
	sort.Strings(wrong)

	guessed := func(path string) bool {
		return strings.HasPrefix(path, "personas") || strings.HasPrefix(path, "workflows")
	}
	derivable, derivableRight := 0, 0
	for path, value := range want {
		if guessed(path) {
			continue
		}
		derivable++
		if got[path] == value {
			derivableRight++
		}
	}

	t.Logf("manifest fields af init gets right: %d of %d", len(right), len(want))
	t.Logf("  excluding personas and workflows, which no repository states: %d of %d",
		derivableRight, derivable)
	t.Logf("  fields it got wrong:")
	for _, w := range wrong {
		t.Logf("    %s", w)
	}

	// Written where a person can read it beside the number, because a
	// fraction quoted with no list behind it is exactly the unsourced figure
	// this repository bans.
	if path := os.Getenv("AF_FIELDS_REPORT"); path != "" {
		var b strings.Builder
		fmt.Fprintf(&b, "%d of %d\n%d of %d without personas and workflows\n\nwrong:\n",
			len(right), len(want), derivableRight, derivable)
		for _, w := range wrong {
			fmt.Fprintf(&b, "  %s\n", w)
		}
		fmt.Fprintf(&b, "\nright:\n")
		for _, r := range right {
			fmt.Fprintf(&b, "  %s\n", r)
		}
		require.NoError(t, os.WriteFile(path, []byte(b.String()), 0o600))
	}

	require.NotEmpty(t, want, "the expected manifest is empty, so this measured nothing")
	require.Positive(t, len(right), "no field matched at all, which means the comparison is broken")
}
