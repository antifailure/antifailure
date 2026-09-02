package detect_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
	"gopkg.in/yaml.v3"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/detect"
	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

// tree builds an in memory repository. Fixtures are written inline rather than
// committed as directories so that each test shows exactly the files that
// produce its result, which is the whole point of a detection test.
func tree(files map[string]string) fstest.MapFS {
	out := fstest.MapFS{}
	for name, body := range files {
		out[name] = &fstest.MapFile{Data: []byte(body), Mode: 0o644}
	}
	return out
}

func run(t *testing.T, name string, files map[string]string) *detect.Result {
	t.Helper()
	res, err := detect.Run(context.Background(), tree(files), name, detect.Options{
		Clock: clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)),
	})
	require.NoError(t, err)
	return res
}

func serviceNamed(t *testing.T, m *schema.Manifest, name string) schema.Service {
	t.Helper()
	for _, s := range m.Services {
		if s.Name == name {
			return s
		}
	}
	var names []string
	for _, s := range m.Services {
		names = append(names, s.Name)
	}
	t.Fatalf("no service named %q; found %v", name, names)
	return schema.Service{}
}

// requireDraftValidates proves the draft would survive the validator af init
// runs before it writes anything. A draft that fails it is never written, so
// a detection result that cannot pass it is a detection bug, not a manifest
// one. The fixture is materialised on disk because the validator checks that
// the paths a service names actually exist.
func requireDraftValidates(t *testing.T, m *schema.Manifest, files map[string]string) {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		full := filepath.Join(root, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o750))
		require.NoError(t, os.WriteFile(full, []byte(body), 0o600))
	}
	body, err := yaml.Marshal(m)
	require.NoError(t, err)
	_, err = manifest.Parse(body, "antifailure.yaml", root)
	require.NoError(t, err, "detection produced a draft af init would refuse to write")
}

func ruleFor(t *testing.T, m *schema.Manifest, host string) schema.EgressRule {
	t.Helper()
	for _, r := range m.Egress.Rules {
		if r.Host == host {
			return r
		}
	}
	var hosts []string
	for _, r := range m.Egress.Rules {
		hosts = append(hosts, r.Host)
	}
	t.Fatalf("no egress rule for %q; found %v", host, hosts)
	return schema.EgressRule{}
}

func TestRun_NextJsWithPrismaAndStripe(t *testing.T) {
	t.Parallel()
	res := run(t, "shop", map[string]string{
		"package.json": `{
  "name": "shop",
  "scripts": {"dev": "next dev", "build": "next build", "start": "next start"},
  "dependencies": {"next": "15.0.0", "react": "19.0.0", "stripe": "17.0.0",
                   "@prisma/client": "6.0.0", "resend": "4.0.0"}
}`,
		"pnpm-lock.yaml":       "lockfileVersion: '9.0'\n",
		"prisma/schema.prisma": "datasource db {\n  provider = \"postgresql\"\n  url = env(\"DATABASE_URL\")\n}\n",
		".env.example":         "DATABASE_URL=\nSTRIPE_SECRET_KEY=\nRESEND_API_KEY=\nNEXTAUTH_SECRET=\n",
		"app/page.tsx":         "export default function Page() { return null }",
		"lib/stripe.ts":        "const key = process.env.STRIPE_SECRET_KEY;\nconst url = process.env.APP_BASE_URL;",
	})

	m := res.Draft
	web := serviceNamed(t, m, "shop")
	require.Equal(t, schema.ServiceWeb, web.Kind)
	require.Equal(t, 3000, web.Port, "Next.js listens on 3000")
	require.Equal(t, "pnpm start", web.Command, "the lockfile selects the package manager")
	require.Equal(t, "npx prisma migrate deploy", web.Migrate,
		"the migration command is what makes rehearsal possible")

	require.Equal(t, schema.DBDocker, m.Database.Provider)
	require.Equal(t, "DATABASE_URL", m.Database.URLEnv)

	// The network policy is the part a user could not write themselves without
	// knowing every SDK's hostname.
	require.Equal(t, schema.ModeBlock, m.Egress.Default)
	require.Equal(t, schema.ModeSandbox, ruleFor(t, m, "api.stripe.com").Mode,
		"Stripe has a real sandbox, so billing runs end to end")
	require.Equal(t, "STRIPE_SECRET_KEY", ruleFor(t, m, "api.stripe.com").Credential)
	require.Equal(t, "/api/webhooks/stripe", ruleFor(t, m, "api.stripe.com").WebhookPath)
	require.Equal(t, schema.ModeCapture, ruleFor(t, m, "api.resend.com").Mode,
		"mail is captured so a preview never emails a real customer")
	require.NotEmpty(t, ruleFor(t, m, "api.resend.com").Note,
		"a rule nobody can explain is a rule nobody dares remove")

	// A variable read by the code and declared nowhere is the one that will be
	// missing at run time.
	var found bool
	for _, f := range detect.OfKind(res.Findings, detect.KindEnvVar) {
		if f.Subject == "APP_BASE_URL" && f.Value == "referenced" {
			found = true
			require.Contains(t, f.Detail, "purpose is unknown")
		}
	}
	require.True(t, found, "a variable read by the code and declared nowhere must be reported")

	require.NotEmpty(t, m.Workflows)
	require.Equal(t, "sign-up", m.Workflows[0].Name)
	require.Contains(t, m.Workflows[0].Description, "welcome email",
		"a project with a mail provider gets an email assertion")
	var names []string
	for _, w := range m.Workflows {
		names = append(names, w.Name)
	}
	require.Contains(t, names, "subscribe", "a project with Stripe gets a billing workflow")
}

func TestRun_DjangoWithCeleryAndSendGrid(t *testing.T) {
	t.Parallel()
	res := run(t, "saleor", map[string]string{
		"manage.py":          "import os\nos.environ.setdefault('DJANGO_SETTINGS_MODULE', 'config.settings')\n",
		"requirements.txt":   "Django==5.0\ncelery==5.3\ngunicorn==22.0\nsendgrid==6.11\npsycopg2-binary==2.9\n",
		"config/settings.py": "DATABASES = {}\n",
		"config/wsgi.py":     "from django.core.wsgi import get_wsgi_application\napplication = get_wsgi_application()\n",
		"config/celery.py":   "from celery import Celery\napp = Celery('config')\n",
		".env.example":       "DATABASE_URL=postgres://user:pass@localhost:5432/app\nSENDGRID_API_KEY=\n",
	})
	m := res.Draft

	web := serviceNamed(t, m, "saleor")
	require.Equal(t, 8000, web.Port)
	require.Equal(t, "python manage.py migrate", web.Migrate)
	require.Contains(t, web.Command, "gunicorn")
	require.Contains(t, web.Command, "config.wsgi:application",
		"the wsgi module is found from the package holding settings.py")

	worker := serviceNamed(t, m, "saleor-worker")
	require.Equal(t, schema.ServiceWorker, worker.Kind)
	require.Contains(t, worker.Command, "celery")
	require.Zero(t, worker.Port, "a worker receives no traffic")

	require.Equal(t, schema.ModeCapture, ruleFor(t, m, "api.sendgrid.com").Mode)
}

func TestRun_GoServiceWithAListener(t *testing.T) {
	t.Parallel()
	res := run(t, "api", map[string]string{
		"go.mod":            "module github.com/acme/api\n\ngo 1.25\n",
		"cmd/api/main.go":   "package main\n\nimport \"net/http\"\n\nfunc main() {\n\thttp.ListenAndServe(\":8080\", nil)\n}\n",
		"internal/db/db.go": "package db\n",
	})
	svc := serviceNamed(t, res.Draft, "api")
	require.Equal(t, 8080, svc.Port, "the port is read from the listener rather than guessed")
	require.Equal(t, "./api", svc.Command)
}

func TestRun_RailsWithSidekiq(t *testing.T) {
	t.Parallel()
	res := run(t, "store", map[string]string{
		"Gemfile":               "source 'https://rubygems.org'\ngem 'rails', '~> 7.1'\ngem 'sidekiq'\ngem 'pg'\n",
		"config/application.rb": "module Store\nend\n",
	})
	m := res.Draft
	web := serviceNamed(t, m, "store")
	require.Equal(t, 3000, web.Port)
	require.Equal(t, "bin/rails db:migrate", web.Migrate)
	worker := serviceNamed(t, m, "store-worker")
	require.Equal(t, schema.ServiceWorker, worker.Kind)
	require.Contains(t, worker.Command, "sidekiq")
}

// A Dockerfile is a statement about the real runtime, so it outranks a guess
// from a dependency list.
func TestRun_DockerfileOutranksAFrameworkDefault(t *testing.T) {
	t.Parallel()
	res := run(t, "app", map[string]string{
		"package.json": `{"name":"app","scripts":{"start":"next start"},"dependencies":{"next":"15.0.0"}}`,
		"Dockerfile": `FROM node:22-alpine AS deps
WORKDIR /app
COPY package.json ./
RUN npm ci

FROM node:22-alpine AS runner
WORKDIR /app
COPY --from=deps /app .
EXPOSE 8080
CMD ["node", "server.js"]
`,
	})
	svc := serviceNamed(t, res.Draft, "app")
	require.Equal(t, 8080, svc.Port, "EXPOSE is what the image actually publishes")
	require.Equal(t, schema.BuildDockerfile, svc.Build.Strategy)
	require.Equal(t, "Dockerfile", svc.Build.Dockerfile)
	require.Equal(t, "runner", svc.Build.Target, "the final stage is the one that ships")
	require.Equal(t, "node server.js", svc.Command)
}

// The other multi stage shape, and by far the more common one: the builder is
// named and the stage that ships is not. finalStage only ever assigned on a
// named FROM, so it kept the builder's name, and af init wrote 'target: build'
// into the manifest. af up would then have built the stage that compiles the
// application rather than the one that runs it, which fails at the point
// furthest from the cause.
func TestRun_AnUnnamedFinalStageTargetsNothing(t *testing.T) {
	t.Parallel()
	res := run(t, "app", map[string]string{
		"package.json": `{"name":"app","scripts":{"start":"next start"},"dependencies":{"next":"15.0.0"}}`,
		"Dockerfile": `FROM node:22-alpine AS build
WORKDIR /app
RUN npm run build

FROM node:22-alpine
WORKDIR /app
COPY --from=build /app ./
EXPOSE 3000
CMD ["node", "server.js"]
`,
	})
	svc := serviceNamed(t, res.Draft, "app")
	require.Equal(t, schema.BuildDockerfile, svc.Build.Strategy)
	require.Empty(t, svc.Build.Target,
		"the stage that ships has no name, so there is no target to name")
}

// af init never set build.context, so a build always ran from the repository
// root, while a Dockerfile at dashboard/Dockerfile is conventionally built
// with dashboard/ as its context. With an explicit COPY the build fails
// outright. With COPY . . it SUCCEEDS and produces an image assembled from the
// wrong directory, so the failure moves to startup and names a path inside a
// container. The second is the dangerous one, so the evidence is read rather
// than a default being picked.
func TestRun_ADockerfileThatCopiesFromItsOwnDirectoryIsBuiltFromIt(t *testing.T) {
	t.Parallel()
	res := run(t, "myrepo", map[string]string{
		"dashboard/package.json": `{"name":"dash","scripts":{"start":"next start"},"dependencies":{"next":"16.0.0"}}`,
		"dashboard/Dockerfile": `FROM node:22-alpine
WORKDIR /app
COPY package.json ./
RUN npm ci
EXPOSE 3100
CMD ["npm", "start"]
`,
	})
	svc := serviceNamed(t, res.Draft, "dash")
	require.Equal(t, "dashboard", svc.Build.Context,
		"package.json exists in dashboard and not at the root, so the context is dashboard")
	for _, q := range res.Questions {
		require.NotEqual(t, "service.dash.context", q.ID,
			"the evidence settles it, so there is nothing to ask")
	}
}

// The monorepo, and the reason this reads evidence instead of changing the
// default. apps/web/Dockerfile reaching the lockfile at the top of the tree
// genuinely wants the repository root, and that build works today. Nothing
// about it may change.
func TestRun_AMonorepoDockerfileReachingTheRootLockfileKeepsTheRoot(t *testing.T) {
	t.Parallel()
	res := run(t, "myrepo", map[string]string{
		"package.json":      `{"name":"root"}`,
		"package-lock.json": `{}`,
		"apps/web/package.json": `{"name":"acme-web","scripts":{"start":"next start"},` +
			`"dependencies":{"next":"16.0.0"}}`,
		"apps/web/Dockerfile": `FROM node:22-alpine
WORKDIR /app
COPY package-lock.json ./
COPY apps/web ./apps/web
RUN npm ci
EXPOSE 3000
CMD ["npm", "start"]
`,
	})
	svc := serviceNamed(t, res.Draft, "acme-web")
	require.Empty(t, svc.Build.Context,
		"the lockfile is only at the root, so the root is the context and an unset value already means it")
	for _, q := range res.Questions {
		require.NotEqual(t, "service.acme-web.context", q.ID,
			"the evidence settles it, so there is nothing to ask")
	}
}

// COPY . . works from either directory, which is exactly why it is the shape
// that fails quietly. There is no evidence to read, so it is asked, the way a
// port disagreement is asked. The default is what 'docker build dashboard'
// does, and a non interactive run reports it as an assumption.
func TestRun_ADockerfileThatOnlyCopiesEverythingAsksWhichDirectory(t *testing.T) {
	t.Parallel()
	res := run(t, "myrepo", map[string]string{
		"dashboard/package.json": `{"name":"dash","scripts":{"start":"next start"},"dependencies":{"next":"16.0.0"}}`,
		"dashboard/Dockerfile": `FROM node:22-alpine
WORKDIR /app
COPY . .
EXPOSE 3100
CMD ["npm", "start"]
`,
	})
	var asked *detect.Question
	for i := range res.Questions {
		if res.Questions[i].ID == "service.dash.context" {
			asked = &res.Questions[i]
		}
	}
	require.NotNil(t, asked, "COPY . . carries no evidence either way, so it has to be asked")
	require.Equal(t, []string{"dashboard", "."}, asked.Options)
	require.Equal(t, "dashboard", asked.Default)
	require.Contains(t, asked.Why, "docker build dashboard")
}

// The other ambiguity, and it does not get the same answer. Here the source
// resolves in BOTH places, so a build from either produces an image, which
// means a repository doing this today is building from the root and working.
// Defaulting to the directory would break it. Defaulting the COPY . . case to
// the root would leave the quiet wrong-image failure exactly as it was. They
// are different questions, so they get different defaults, and both are asked.
func TestRun_WhenBothDirectoriesSatisfyEverySourceTheRootStaysTheDefault(t *testing.T) {
	t.Parallel()
	res := run(t, "myrepo", map[string]string{
		"package.json":           `{"name":"root"}`,
		"dashboard/package.json": `{"name":"dash","scripts":{"start":"next start"},"dependencies":{"next":"16.0.0"}}`,
		"dashboard/Dockerfile": `FROM node:22-alpine
WORKDIR /app
COPY package.json ./
EXPOSE 3100
CMD ["npm", "start"]
`,
	})
	var asked *detect.Question
	for i := range res.Questions {
		if res.Questions[i].ID == "service.dash.context" {
			asked = &res.Questions[i]
		}
	}
	require.NotNil(t, asked, "both would build, so a person should choose")
	require.Equal(t, ".", asked.Default,
		"a repository shaped like this builds from the root today and works")
	require.Equal(t, []string{"dashboard", "."}, asked.Options)
	require.Empty(t, serviceNamed(t, res.Draft, "dash").Build.Context,
		"an unattended run keeps today's behaviour")
}

// The question above lived on the candidate rather than inside build, and the
// fold moved build across without it, so the question vanished exactly when a
// Dockerfile was folded into the package that named it. That is the common
// case, which is what made it worth a test of its own rather than trusting the
// case above to cover it.
func TestRun_AnAmbiguousContextSurvivesTheFold(t *testing.T) {
	t.Parallel()
	res := run(t, "myrepo", map[string]string{
		"dashboard/package.json": `{"name":"a-name-that-is-not-the-directory",` +
			`"scripts":{"start":"next start"},"dependencies":{"next":"16.0.0"}}`,
		"dashboard/Dockerfile": `FROM node:22-alpine
WORKDIR /app
COPY . .
EXPOSE 3100
CMD ["npm", "start"]
`,
	})
	require.Len(t, res.Draft.Services, 1)
	found := false
	for _, q := range res.Questions {
		if q.ID == "service.a-name-that-is-not-the-directory.context" {
			found = true
		}
	}
	require.True(t, found, "the fold renamed the service and dropped the question with it")
}

// A Dockerfile that copies nothing does not read the context at all, so which
// directory it is cannot change what the image contains. Asking would be noise
// about a decision with no consequence.
func TestRun_ADockerfileThatCopiesNothingIsNotAskedAboutItsContext(t *testing.T) {
	t.Parallel()
	res := run(t, "myrepo", map[string]string{
		"svc/Dockerfile": `FROM alpine
EXPOSE 9000
CMD ["/bin/svc"]
`,
	})
	svc := serviceNamed(t, res.Draft, "svc")
	require.Empty(t, svc.Build.Context)
	for _, q := range res.Questions {
		require.NotEqual(t, "service.svc.context", q.ID,
			"nothing is copied, so the context has no effect to ask about")
	}
}

// A copy from an earlier stage reads out of the image, not out of the context,
// so it is not evidence about which directory the context is. Counting it
// would read /app as a root path and settle a question it knows nothing about.
func TestRun_ACopyFromAnEarlierStageIsNotEvidenceAboutTheContext(t *testing.T) {
	t.Parallel()
	res := run(t, "myrepo", map[string]string{
		"dashboard/package.json": `{"name":"dash","scripts":{"start":"next start"},"dependencies":{"next":"16.0.0"}}`,
		// The only COPY naming a path is the one from the earlier stage, and
		// that path also exists in dashboard/. Counting it would settle the
		// question on a line that never read the context at all, so this
		// fixture is red the moment the --from guard goes.
		"dashboard/Dockerfile": `FROM node:22-alpine AS build
WORKDIR /app
COPY . .
RUN npm run build

FROM node:22-alpine
WORKDIR /app
COPY --from=build package.json ./
EXPOSE 3100
CMD ["npm", "start"]
`,
	})
	var asked bool
	for _, q := range res.Questions {
		if q.ID == "service.dash.context" {
			asked = true
		}
	}
	require.True(t, asked,
		"only COPY . . reads the context here, so this is the ambiguous case and not a settled one")
}

// The median containerised Node repository, and the shape af init failed on:
// the package is not named after the directory somebody cloned it into.
//
// Before this, the Dockerfile produced a service named after the directory and
// the package produced one named after itself, both web, both on port 3000.
// The manifest validator refused the draft and af init told the user to fix a
// line in a file it had declined to write.
func TestRun_ADockerfileAndAPackageWithAnotherNameAreOneService(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"package.json": `{"name":"antifailure-example-next-app","scripts":{"start":"next start"},"dependencies":{"next":"16.3.3"}}`,
		"Dockerfile": `FROM node:22-alpine AS build
WORKDIR /app
RUN npm run build

FROM node:22-alpine
EXPOSE 3000
CMD ["node", "server.js"]
`,
	}
	res := run(t, "next-app", files)

	require.Len(t, res.Draft.Services, 1,
		"a Dockerfile and a package.json describing one application are one service")
	svc := serviceNamed(t, res.Draft, "antifailure-example-next-app")
	require.Equal(t, 3000, svc.Port)
	require.Equal(t, schema.BuildDockerfile, svc.Build.Strategy)
	require.Equal(t, "node server.js", svc.Command,
		"the command the image runs beats a package script of equal confidence")

	// The whole point. A draft that does not validate is never written, so the
	// draft has to validate.
	requireDraftValidates(t, res.Draft, files)
}

// The guard. Two services declared by one source in one directory are two
// services, and folding them would delete one from a file people commit.
func TestRun_TwoComposeServicesOnOneBuildContextStayTwo(t *testing.T) {
	t.Parallel()
	res := run(t, "stack", map[string]string{
		"docker-compose.yml": `services:
  web:
    build: .
    ports:
      - "3000:3000"
  admin:
    build: .
    ports:
      - "3001:3001"
`,
	})
	require.Len(t, res.Draft.Services, 2)
	require.Equal(t, 3000, serviceNamed(t, res.Draft, "web").Port)
	require.Equal(t, 3001, serviceNamed(t, res.Draft, "admin").Port)
}

// A Procfile names processes, not applications, so "web" loses to the package
// name. The worker is a different role and survives on its own.
func TestRun_AProcfileWebProcessFoldsIntoThePackageItRuns(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"package.json": `{"name":"acme-web","scripts":{"start":"next start"},"dependencies":{"next":"15.0.0"}}`,
		"Procfile": `web: npm start
worker: node worker.js
`,
	}
	res := run(t, "myrepo", files)

	require.Len(t, res.Draft.Services, 2)
	require.Equal(t, 3000, serviceNamed(t, res.Draft, "acme-web").Port)
	require.Equal(t, schema.ServiceWorker, serviceNamed(t, res.Draft, "worker").Kind)
	requireDraftValidates(t, res.Draft, files)
}

// A dependency on a name that folding renamed has to follow it. Left dangling,
// the draft names a service it does not declare and the validator refuses the
// file af init just wrote.
func TestRun_ADependencyOnAFoldedNameFollowsIt(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"package.json": `{"name":"acme-web","scripts":{"start":"next start"},"dependencies":{"next":"15.0.0"}}`,
		"jobs/Dockerfile": `FROM alpine
EXPOSE 9100
CMD ["/bin/jobs"]
`,
		"docker-compose.yml": `services:
  web:
    build: .
    ports:
      - "3000:3000"
  jobs:
    build: ./jobs
    depends_on:
      - web
`,
	}
	res := run(t, "myrepo", files)

	for _, s := range res.Draft.Services {
		require.NotEqual(t, "web", s.Name, "the compose key lost to the package name")
	}
	require.Equal(t, []string{"acme-web"}, serviceNamed(t, res.Draft, "jobs").DependsOn)
	requireDraftValidates(t, res.Draft, files)
}

func TestRun_DockerfileEntrypointAndCommandCombine(t *testing.T) {
	t.Parallel()
	res := run(t, "svc", map[string]string{
		"Dockerfile": "FROM alpine\nEXPOSE 9000\nENTRYPOINT [\"/bin/app\"]\nCMD [\"--serve\"]\n",
	})
	// An image with both runs the entrypoint with the command as arguments,
	// which is the semantics the runtime has to reproduce.
	require.Equal(t, "/bin/app --serve", serviceNamed(t, res.Draft, "svc").Command)
}

func TestRun_DockerfileLineContinuationsAreJoined(t *testing.T) {
	t.Parallel()
	res := run(t, "svc", map[string]string{
		"Dockerfile": "FROM alpine\nEXPOSE \\\n  7000\nCMD [\"/bin/app\"]\n",
	})
	require.Equal(t, 7000, serviceNamed(t, res.Draft, "svc").Port)
}

func TestRun_ComposePublishesTheContainerPortNotTheHostPort(t *testing.T) {
	t.Parallel()
	res := run(t, "stack", map[string]string{
		"docker-compose.yml": `version: "3.9"
services:
  web:
    build: .
    ports:
      - "8080:3000"
    environment:
      - DATABASE_URL
      - NODE_ENV=production
    depends_on:
      - db
  db:
    image: postgres:16
    ports:
      - "5432:5432"
  cache:
    image: redis:7
`,
	})
	m := res.Draft
	// "8080:3000" means the container listens on 3000. Reading the host side
	// would produce an environment that never becomes ready.
	require.Equal(t, 3000, serviceNamed(t, m, "web").Port)

	// Infrastructure is provided, not built. A postgres service in compose is
	// how the database provider gets chosen without asking.
	for _, s := range m.Services {
		require.NotEqual(t, "db", s.Name, "a database image is infrastructure, not a service to build")
		require.NotEqual(t, "cache", s.Name)
	}
	var sawPostgres bool
	for _, f := range detect.OfKind(res.Findings, detect.KindDatabase) {
		if f.Subject == "postgres" {
			sawPostgres = true
		}
	}
	require.True(t, sawPostgres)
}

func TestRun_ComposeRecognisesInfrastructureBehindARegistryPrefix(t *testing.T) {
	t.Parallel()
	res := run(t, "stack", map[string]string{
		"docker-compose.yml": "services:\n  db:\n    image: ghcr.io/acme/pgvector:pg16\n  web:\n    build: .\n    ports:\n      - \"3000:3000\"\n",
	})
	for _, s := range res.Draft.Services {
		require.NotEqual(t, "db", s.Name)
	}
}

func TestRun_ProcfileNamesProcessesDirectly(t *testing.T) {
	t.Parallel()
	res := run(t, "heroku-app", map[string]string{
		"Procfile": "web: bundle exec puma -p 3000\nworker: bundle exec sidekiq\nrelease: bin/rails db:migrate\n",
		"Gemfile":  "source 'https://rubygems.org'\ngem 'rails'\n",
	})
	m := res.Draft
	web := serviceNamed(t, m, "web")
	require.Equal(t, schema.ServiceWeb, web.Kind)
	require.Equal(t, 3000, web.Port)
	require.Equal(t, schema.ServiceWorker, serviceNamed(t, m, "worker").Kind,
		"Heroku's convention is that only the process named web receives traffic")

	// The release phase is where migrations go, and it belongs on a service
	// rather than becoming a phantom one.
	var migrateSeen bool
	for _, s := range m.Services {
		if s.Migrate == "bin/rails db:migrate" {
			migrateSeen = true
		}
		require.NotEqual(t, "release", s.Name)
	}
	require.True(t, migrateSeen)
}

func TestRun_VercelCronsBecomeCronServices(t *testing.T) {
	t.Parallel()
	res := run(t, "app", map[string]string{
		"package.json": `{"name":"app","dependencies":{"next":"15.0.0"},"scripts":{"start":"next start"}}`,
		"vercel.json":  `{"crons":[{"path":"/api/cron/digest","schedule":"0 8 * * *"}]}`,
	})
	var cron *schema.Service
	for i := range res.Draft.Services {
		if res.Draft.Services[i].Kind == schema.ServiceCron {
			cron = &res.Draft.Services[i]
		}
	}
	require.NotNil(t, cron, "a declared cron must become a service")
	require.Equal(t, "0 8 * * *", cron.Schedule)
	require.Equal(t, "/api/cron/digest", cron.HealthPath)
}

func TestRun_MonorepoProducesAServicePerPackage(t *testing.T) {
	t.Parallel()
	res := run(t, "acme", map[string]string{
		"package.json":             `{"name":"acme","private":true,"workspaces":["apps/*","packages/*"]}`,
		"pnpm-workspace.yaml":      "packages:\n  - 'apps/*'\n  - 'packages/*'\n",
		"turbo.json":               `{"tasks":{"build":{}}}`,
		"apps/web/package.json":    `{"name":"@acme/web","dependencies":{"next":"15.0.0"},"scripts":{"start":"next start"}}`,
		"apps/api/package.json":    `{"name":"@acme/api","dependencies":{"fastify":"5.0.0"},"scripts":{"start":"node dist/index.js"}}`,
		"packages/ui/package.json": `{"name":"@acme/ui","dependencies":{"react":"19.0.0"}}`,
	})
	m := res.Draft
	web := serviceNamed(t, m, "web")
	require.Equal(t, "apps/web", web.Path)
	api := serviceNamed(t, m, "api")
	require.Equal(t, "apps/api", api.Path)
	// A scoped name becomes its last segment, because the scope is the same
	// for every package and adds nothing to a hostname.
	for _, s := range m.Services {
		require.NotContains(t, s.Name, "@")
		require.NotEqual(t, "ui", s.Name, "a library package is not a service")
	}
}

func TestRun_TwoServicesOnOnePortProduceAValidManifestAnyway(t *testing.T) {
	t.Parallel()
	// Detection reports what it sees. The manifest validator is what rejects a
	// collision, and it names both services, which is more useful than
	// detection silently renumbering one.
	res := run(t, "stack", map[string]string{
		"docker-compose.yml": "services:\n  a:\n    build: ./a\n    ports:\n      - \"3000:3000\"\n  b:\n    build: ./b\n    ports:\n      - \"3001:3000\"\n",
	})
	require.Equal(t, 3000, serviceNamed(t, res.Draft, "a").Port)
	require.Equal(t, 3000, serviceNamed(t, res.Draft, "b").Port)
}

// Determinism is not a nicety. af init writes a manifest that gets committed,
// and a manifest that shuffles on every run is unusable in review.
func TestRun_IsDeterministic(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"package.json":          `{"name":"acme","workspaces":["apps/*"]}`,
		"apps/web/package.json": `{"name":"web","dependencies":{"next":"15.0.0","stripe":"17.0.0"},"scripts":{"start":"next start"}}`,
		"apps/api/package.json": `{"name":"api","dependencies":{"express":"4.0.0","resend":"4.0.0"},"scripts":{"start":"node index.js"}}`,
		".env.example":          "DATABASE_URL=\nSTRIPE_SECRET_KEY=\nRESEND_API_KEY=\n",
		"docker-compose.yml":    "services:\n  db:\n    image: postgres:17\n",
	}
	first := renderDraft(t, run(t, "acme", files))
	for i := 0; i < 10; i++ {
		require.Equal(t, first, renderDraft(t, run(t, "acme", files)),
			"detection must produce identical output across runs")
	}
}

func renderDraft(t *testing.T, res *detect.Result) string {
	t.Helper()
	var b strings.Builder
	for _, s := range res.Draft.Services {
		b.WriteString(s.Name + "|" + string(s.Kind) + "|" + s.Path + "|" + s.Command + "\n")
	}
	for _, r := range res.Draft.Egress.Rules {
		b.WriteString(r.Host + "=" + string(r.Mode) + "\n")
	}
	for _, q := range res.Questions {
		b.WriteString("q:" + q.ID + "\n")
	}
	return b.String()
}

func TestRun_AsksWhenAPortIsUnknown(t *testing.T) {
	t.Parallel()
	res := run(t, "mystery", map[string]string{
		"package.json": `{"name":"mystery","scripts":{"start":"node server.js"},"dependencies":{"pino":"9.0.0"}}`,
	})
	var ids []string
	for _, q := range res.Questions {
		ids = append(ids, q.ID)
	}
	require.Contains(t, ids, "service.mystery.port")
	for _, q := range res.Questions {
		if q.ID == "service.mystery.port" {
			require.NotEmpty(t, q.Why, "a question must say what detection saw")
		}
	}
}

func TestRun_AsksWhenSourcesDisagreeAboutAPort(t *testing.T) {
	t.Parallel()
	res := run(t, "app", map[string]string{
		"package.json": `{"name":"app","dependencies":{"next":"15.0.0"},"scripts":{"start":"next start --port 4000"}}`,
		"Dockerfile":   "FROM node:22\nEXPOSE 8080\nCMD [\"node\",\"server.js\"]\n",
	})
	var q *detect.Question
	for i := range res.Questions {
		if res.Questions[i].ID == "service.app.port" {
			q = &res.Questions[i]
		}
	}
	require.NotNil(t, q, "a disagreement is exactly the case where a silent choice is most likely wrong")
	require.Len(t, q.Options, 2)
	require.Contains(t, q.Why, "disagree")
}

func TestRun_AsksWhenNoPostgresIsFound(t *testing.T) {
	t.Parallel()
	res := run(t, "static", map[string]string{
		"package.json": `{"name":"static","dependencies":{"astro":"5.0.0"},"scripts":{"start":"astro preview"}}`,
	})
	var ids []string
	for _, q := range res.Questions {
		ids = append(ids, q.ID)
	}
	require.Contains(t, ids, "database.present")
}

func TestRun_AnEmptyRepositoryProducesNoServicesAndSaysSo(t *testing.T) {
	t.Parallel()
	res := run(t, "empty", map[string]string{"README.md": "# nothing here\n"})
	require.Empty(t, res.Draft.Services)
	require.NotEmpty(t, res.Questions, "an empty draft must come with questions rather than silence")
}

func TestRun_SkipsDependencyAndBuildDirectories(t *testing.T) {
	t.Parallel()
	res := run(t, "app", map[string]string{
		"package.json":                      `{"name":"app","dependencies":{"next":"15.0.0"},"scripts":{"start":"next start"}}`,
		"node_modules/express/package.json": `{"name":"express","dependencies":{"stripe":"17.0.0"}}`,
		"node_modules/foo/Dockerfile":       "FROM alpine\nEXPOSE 9999\n",
		".next/standalone/package.json":     `{"name":"built"}`,
		"vendor/github.com/x/y/go.mod":      "module x\n",
	})
	// A dependency's own dependencies are not this application's.
	for _, r := range res.Draft.Egress.Rules {
		require.NotEqual(t, "api.stripe.com", r.Host,
			"a transitive dependency inside node_modules must not create an egress rule")
	}
	require.Equal(t, 3000, serviceNamed(t, res.Draft, "app").Port)
}

func TestRun_SkipsTestAndExampleDockerfiles(t *testing.T) {
	t.Parallel()
	res := run(t, "app", map[string]string{
		"package.json":             `{"name":"app","dependencies":{"next":"15.0.0"},"scripts":{"start":"next start"}}`,
		"examples/demo/Dockerfile": "FROM alpine\nEXPOSE 9999\n",
		"test/Dockerfile":          "FROM alpine\nEXPOSE 8888\n",
	})
	require.Equal(t, 3000, serviceNamed(t, res.Draft, "app").Port)
	require.Len(t, res.Draft.Services, 1)
}

func TestRun_MalformedPackageJsonIsReportedNotFatal(t *testing.T) {
	t.Parallel()
	res := run(t, "app", map[string]string{
		"package.json":          `{"name": "app", this is not json`,
		"apps/web/package.json": `{"name":"web","dependencies":{"next":"15.0.0"},"scripts":{"start":"next start"}}`,
	})
	// One bad file must not lose the rest of the repository.
	require.Equal(t, 3000, serviceNamed(t, res.Draft, "web").Port)
	var noted bool
	for _, f := range detect.OfKind(res.Findings, detect.KindNote) {
		if strings.Contains(f.Detail, "not valid JSON") {
			noted = true
		}
	}
	require.True(t, noted, "the malformed file must be reported rather than silently ignored")
}

func TestRun_ReportsPartialResultsWhenTheBudgetRunsOut(t *testing.T) {
	t.Parallel()
	c := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	// An analyzer that consumes the whole budget, so the ones after it do not
	// run. Partial results with an explicit note beat an af init that hangs.
	slow := analyzerFunc{name: "slow", fn: func() ([]detect.Finding, error) {
		c.Advance(time.Hour)
		return nil, nil
	}}
	res, err := detect.Run(context.Background(), tree(map[string]string{
		"package.json": `{"name":"app","dependencies":{"next":"15.0.0"}}`,
	}), "app", detect.Options{
		Clock:     c,
		Budget:    time.Second,
		Analyzers: []detect.Analyzer{slow, &detect.NodeAnalyzer{}},
	})
	require.NoError(t, err)
	require.True(t, res.Partial)
	require.Empty(t, res.Draft.Services, "the node analyzer must not have run")
}

func TestRun_AnAnalyzerThatFailsDoesNotLoseTheOthers(t *testing.T) {
	t.Parallel()
	broken := analyzerFunc{name: "broken", fn: func() ([]detect.Finding, error) {
		return nil, errBoom
	}}
	res, err := detect.Run(context.Background(), tree(map[string]string{
		"package.json": `{"name":"app","dependencies":{"next":"15.0.0"},"scripts":{"start":"next start"}}`,
	}), "app", detect.Options{
		Clock:     clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)),
		Analyzers: []detect.Analyzer{broken, &detect.NodeAnalyzer{}},
	})
	require.NoError(t, err)
	require.Equal(t, 3000, serviceNamed(t, res.Draft, "app").Port)
	var noted bool
	for _, f := range detect.OfKind(res.Findings, detect.KindNote) {
		if strings.Contains(f.Detail, "broken analyzer failed") {
			noted = true
		}
	}
	require.True(t, noted)
}

func TestRun_StopsOnACancelledContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := detect.Run(ctx, tree(map[string]string{"package.json": "{}"}), "app", detect.Options{
		Clock: clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)),
	})
	require.NoError(t, err)
	require.True(t, res.Partial)
}

func TestRun_LargeFilesAreSkippedAndReported(t *testing.T) {
	t.Parallel()
	big := strings.Repeat("x", detect.MaxFileSize+1)
	res := run(t, "app", map[string]string{
		"package.json": `{"name":"app","dependencies":{"next":"15.0.0"},"scripts":{"start":"next start"}}`,
		"dump.sql":     big,
	})
	require.Contains(t, res.Skipped, "dump.sql")
	require.Equal(t, 3000, serviceNamed(t, res.Draft, "app").Port)
}

func TestRun_EveryFindingNamesItsEvidence(t *testing.T) {
	t.Parallel()
	res := run(t, "shop", map[string]string{
		"package.json": `{"name":"shop","dependencies":{"next":"15.0.0","stripe":"17.0.0"},"scripts":{"start":"next start"}}`,
		".env.example": "DATABASE_URL=\nSTRIPE_SECRET_KEY=\n",
		"Dockerfile":   "FROM node:22\nEXPOSE 3000\n",
	})
	for _, f := range res.Findings {
		if f.Kind == detect.KindNote {
			continue // a note about the run itself has no file
		}
		require.NotEmpty(t, f.Evidence,
			"finding %s/%s names no evidence, so a user cannot check the reasoning", f.Kind, f.Subject)
		require.NotEmpty(t, f.Analyzer)
	}
}

// Detection reads. It never runs anything from the repository, because the
// repository is untrusted input.
func TestRun_NeverReadsAValueFromAnExampleFile(t *testing.T) {
	t.Parallel()
	// An example file sometimes contains a real credential by accident.
	// Reading the value would put it into a finding, then an event, then a log.
	const planted = "sk" + "_live_51NotARealKeyButShapedLikeOne"
	res := run(t, "app", map[string]string{
		"package.json": `{"name":"app","dependencies":{"next":"15.0.0"},"scripts":{"start":"next start"}}`,
		".env.example": "STRIPE_SECRET_KEY=" + planted + "\n",
	})
	for _, f := range res.Findings {
		require.NotContains(t, f.Value, planted, "a finding must never carry a value from a dotenv file")
		require.NotContains(t, f.Detail, planted)
		for _, v := range f.Extra {
			require.NotContains(t, v, planted)
		}
	}
}

type analyzerFunc struct {
	name string
	fn   func() ([]detect.Finding, error)
}

func (a analyzerFunc) Name() string { return a.name }
func (a analyzerFunc) Analyze(context.Context, *detect.Repo) ([]detect.Finding, error) {
	return a.fn()
}

var errBoom = errorString("boom")

type errorString string

func (e errorString) Error() string { return string(e) }
