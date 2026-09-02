# The control plane image.
#
# Build context is the repository root, so that this file can see the whole
# npm workspace. It deliberately cannot see ee/: the enterprise workspace is a
# separate root and the community image must not carry an enterprise symbol.
#
# There is no compile step. The control plane is TypeScript run directly by
# Node's type stripping, which is what `npm start` does in development, so the
# image runs the same code path a developer runs rather than a transpiled
# approximation of it.

# ---------------------------------------------------------------------------
# Dependencies. Separated so that a source-only change does not reinstall.
# ---------------------------------------------------------------------------
FROM node:26-alpine AS deps

WORKDIR /app

# Every workspace manifest, and nothing else. npm needs all of them present to
# resolve the workspace graph, and copying only manifests means this layer is
# cached until a dependency actually changes.
COPY web/package.json web/package-lock.json ./
COPY web/apps/api/package.json ./apps/api/
COPY web/packages/db/package.json ./packages/db/
COPY web/packages/policy/package.json ./packages/policy/

# Scoped to this workspace, not the whole tree. A plain `npm ci` here would
# install every workspace member's dependencies into an image that runs one
# server, and the assertion below exists because that scoping is one flag away
# from bringing a web framework along for the ride.
#
# The console is deliberately not among the manifests above. It is its own npm
# project with its own lockfile rather than a workspace member, and it is built
# in its own stage further down, which is what keeps a compiler and a framework
# out of this stage entirely rather than merely unselected.
#
# --ignore-scripts: nothing in this dependency tree needs a build step, and a
# postinstall script running at image build time is a supply chain hole that
# buys nothing here.
RUN npm ci --omit=dev --ignore-scripts \
      --workspace @antifailure/api --include-workspace-root

# Asserted rather than assumed. The scoping above is one flag away from
# silently installing a web framework into this image, and an image that is
# three hundred megabytes larger than it should be is not something anybody
# notices from a build log.
RUN test ! -d node_modules/next \
  || (echo 'the web framework is in the API image: the workspace scoping above stopped working' && exit 1)

# ---------------------------------------------------------------------------
# The console.
#
# A Next.js static export, built here and copied into the runtime image, so the
# control plane serves its own web application from its own origin. That is the
# security model rather than a packaging choice: the session is a SameSite=Lax
# cookie on this origin, and a console served from a second hostname would need
# SameSite=None and credentialed CORS on every endpoint of the API.
#
# Its own lockfile and its own node_modules. The console depends on React and
# Next; the control plane must not, and putting it in the web/ workspace would
# have put both into `npm ci --omit=dev` and into the runtime image.
# ---------------------------------------------------------------------------
FROM node:26-alpine AS console

# Off, in the image and in CI. A build step that reports anonymous usage to a
# third party from a machine holding this repository is not a trade anybody
# here agreed to, and it is one line.
ENV NEXT_TELEMETRY_DISABLED=1

WORKDIR /console

COPY console/package.json console/package-lock.json ./
RUN npm ci --no-audit --no-fund

COPY console/ ./
RUN npm run build && test -f out/index.html

# ---------------------------------------------------------------------------
# Runtime.
# ---------------------------------------------------------------------------
FROM node:26-alpine AS runtime

# Stamped by the build workflow from the git tag and commit. Declared here so
# that `docker inspect` on a running container answers "which build is this"
# without going back to the registry.
ARG AF_VERSION=dev
ARG AF_COMMIT=unknown
ARG AF_BUILD_DATE=unknown

LABEL org.opencontainers.image.title="Antifailure control plane" \
      org.opencontainers.image.description="Environments that outlive a CI job, quotas, and history." \
      org.opencontainers.image.source="https://github.com/antifailure/antifailure" \
      # /docs/ IS PART OF THE PATH. The site is served under it, so the URL
      # without it is a 404, and this label is the one thing in the image that
      # tells an operator where to read about what they are running. It was
      # wrong in every image published so far, and nothing could have caught it:
      # claimcheck reads markdown, and a string in a Dockerfile is not markdown.
      org.opencontainers.image.documentation="https://antifailure.dev/docs/self-hosting/control-plane/" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.version="${AF_VERSION}" \
      org.opencontainers.image.revision="${AF_COMMIT}" \
      org.opencontainers.image.created="${AF_BUILD_DATE}"

ENV NODE_ENV=production \
    AF_PORT=8080 \
    AF_VERSION=${AF_VERSION} \
    AF_COMMIT=${AF_COMMIT} \
    NODE_OPTIONS=--disable-warning=ExperimentalWarning

WORKDIR /app

COPY --from=deps /app/node_modules ./node_modules
COPY web/package.json ./
COPY web/apps/api ./apps/api
COPY web/packages/db ./packages/db
COPY web/packages/policy ./packages/policy

# The bootstrap entrypoint. Shipped in the same image as the application so
# that the schema it applies and the code that reads it are the same build:
# a migration job pinned to a different tag from the deployment it precedes is
# how a schema arrives that the running code does not understand.
COPY deploy/docker/bootstrap.mjs ./bootstrap.mjs
COPY deploy/docker/maintenance.mjs ./maintenance.mjs
# The preview identities. Refuses to run outside an environment the engine
# created; see the file for why that check is not a formality.
COPY deploy/docker/personas.mjs ./personas.mjs

# The console's build. src/console/static.ts looks here by default; the path is
# overridable with AF_CONSOLE_DIR for a self-hosted operator who serves it some
# other way.
COPY --from=console /console/out ./console-out

# The migrations are read from disk at runtime by AF_MIGRATE=1, so they have to
# be in the image. Asserted rather than assumed: an image whose migration
# directory is empty fails at deploy time here instead of at three in the
# morning when someone sets AF_MIGRATE and it silently applies nothing.
RUN test -n "$(ls -A ./packages/db/migrations)" || (echo 'no migrations in image' && exit 1)

# The same assertion for the console, for the same reason. An image whose
# console directory is empty answers every page with a 503 that explains
# itself -- which is far better than a blank 404 and still not something to
# discover in production.
RUN test -f ./console-out/index.html || (echo 'no console build in image' && exit 1)

# Runs as the unprivileged `node` user that the base image already provides.
# Nothing in the container is owned by it, so nothing in the container can be
# rewritten by a process that gets code execution inside it.
USER node

EXPOSE 8080

# Liveness only. /health is a static literal and does not check the database,
# so this answers "is the process up", never "can it serve". Readiness is left
# to the orchestrator, and the reason is written down in the self-hosting page
# rather than implied by a probe that claims more than it checks.
# Ten seconds, and five retries. /health is a cheap route and this is still
# generous on purpose: a check that fails on a loaded machine takes down a
# container that was answering correctly, which is a worse outcome than a check
# that waits. Measured against the sibling image, where a three second timeout
# failed seventeen times in a row against a page that was returning 200 in 4.4
# seconds.
HEALTHCHECK --interval=30s --timeout=10s --start-period=30s --retries=5 \
  CMD node -e "fetch('http://127.0.0.1:'+(process.env.AF_PORT||8080)+'/health').then(r=>process.exit(r.ok?0:1)).catch(()=>process.exit(1))"

CMD ["node", "apps/api/src/main.ts"]
