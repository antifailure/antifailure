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
FROM node:24-alpine AS deps

WORKDIR /app

# Every workspace manifest, and nothing else. npm needs all of them present to
# resolve the workspace graph, and copying only manifests means this layer is
# cached until a dependency actually changes.
COPY web/package.json web/package-lock.json ./
COPY web/apps/api/package.json ./apps/api/
COPY web/packages/db/package.json ./packages/db/
COPY web/packages/policy/package.json ./packages/policy/

# --ignore-scripts: nothing in this dependency tree needs a build step, and a
# postinstall script running at image build time is a supply chain hole that
# buys nothing here.
RUN npm ci --omit=dev --ignore-scripts

# ---------------------------------------------------------------------------
# Runtime.
# ---------------------------------------------------------------------------
FROM node:24-alpine AS runtime

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

# The migrations are read from disk at runtime by AF_MIGRATE=1, so they have to
# be in the image. Asserted rather than assumed: an image whose migration
# directory is empty fails at deploy time here instead of at three in the
# morning when someone sets AF_MIGRATE and it silently applies nothing.
RUN test -n "$(ls -A ./packages/db/migrations)" || (echo 'no migrations in image' && exit 1)

# Runs as the unprivileged `node` user that the base image already provides.
# Nothing in the container is owned by it, so nothing in the container can be
# rewritten by a process that gets code execution inside it.
USER node

EXPOSE 8080

# Liveness only. /health is a static literal and does not check the database,
# so this answers "is the process up", never "can it serve". Readiness is left
# to the orchestrator, and the reason is written down in the self-hosting page
# rather than implied by a probe that claims more than it checks.
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
  CMD node -e "fetch('http://127.0.0.1:'+(process.env.AF_PORT||8080)+'/health').then(r=>process.exit(r.ok?0:1)).catch(()=>process.exit(1))"

CMD ["node", "apps/api/src/main.ts"]
