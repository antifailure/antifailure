# The control plane's web application.
#
# Build context is the repository root, so this file can see the whole npm
# workspace. It has to: the application is a workspace member and npm resolves
# its dependencies through the root lockfile.
#
# Three stages, and the split is about what has to be reinstalled when. A
# source change must not reinstall node_modules, and neither the toolchain nor
# the sources may end up in the image that runs.

# ---------------------------------------------------------------------------
# Dependencies. Manifests only, so this layer is cached until one changes.
# ---------------------------------------------------------------------------
FROM node:24-alpine AS deps

WORKDIR /app

COPY web/package.json web/package-lock.json ./
COPY web/apps/app/package.json ./apps/app/
COPY web/apps/api/package.json ./apps/api/
COPY web/packages/db/package.json ./packages/db/
COPY web/packages/policy/package.json ./packages/policy/

# --ignore-scripts: nothing in this tree needs a build step, and a postinstall
# running at image build time is a supply chain hole that buys nothing.
RUN npm ci --ignore-scripts

# ---------------------------------------------------------------------------
# Build.
# ---------------------------------------------------------------------------
FROM node:24-alpine AS build

WORKDIR /app

COPY --from=deps /app/node_modules ./node_modules
COPY web/package.json ./
COPY web/apps/app ./apps/app

# Both faces come from a package rather than a font service, so this build
# needs no network at all. That is deliberate: a build step that fetches a
# stylesheet is a build that fails inside a sealed environment and nowhere
# else, which is the worst place for a failure to be introduced.
ENV NEXT_TELEMETRY_DISABLED=1
RUN cd apps/app && npm run build

# The build has to have produced a standalone server. Asserted rather than
# assumed: without `output: "standalone"` the copy below silently produces an
# image with no server in it, and the failure appears at start-up as a missing
# file rather than here as a missing build.
RUN test -f apps/app/.next/standalone/apps/app/server.js \
  || (echo 'no standalone server: next.config.ts must set output: "standalone"' && exit 1)

# ---------------------------------------------------------------------------
# Runtime.
# ---------------------------------------------------------------------------
FROM node:24-alpine AS runtime

ARG AF_VERSION=dev
ARG AF_COMMIT=unknown
ARG AF_BUILD_DATE=unknown

LABEL org.opencontainers.image.title="Antifailure control plane web application" \
      org.opencontainers.image.description="Environments, runs, network policy, and the audit log." \
      org.opencontainers.image.source="https://github.com/antifailure/antifailure" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.version="${AF_VERSION}" \
      org.opencontainers.image.revision="${AF_COMMIT}" \
      org.opencontainers.image.created="${AF_BUILD_DATE}"

ENV NODE_ENV=production \
    NEXT_TELEMETRY_DISABLED=1 \
    PORT=3100 \
    HOSTNAME=0.0.0.0

WORKDIR /app

# The standalone output carries its own node_modules, so nothing from the
# dependency stage is copied here. The image is the server and the assets and
# nothing else: no TypeScript, no Tailwind, no sources.
COPY --from=build /app/apps/app/.next/standalone ./
COPY --from=build /app/apps/app/.next/static ./apps/app/.next/static
COPY --from=build /app/apps/app/public ./apps/app/public

USER node

EXPOSE 3100

# Liveness only, and against a page that renders without a session. Asking for
# a page that needs one would report a healthy application as unhealthy the
# moment the cookie expired.
#
# The timeout is ten seconds rather than three, which sounds generous and is
# the point. This is a server rendered page, so answering it means starting a
# node process and rendering React, and on a machine that is busy that took
# 4.4 seconds and failed a three second check seventeen times in a row. The
# application was correct and serving 200s the whole time. A liveness check
# that fails on a loaded machine reports a healthy application as unhealthy,
# which is the exact failure the line above is already trying to avoid, and it
# is worse than a slow check because it takes the container down.
#
# The start period is longer for the same reason: a cold Next.js server on a
# contended host is not ready in twenty seconds, and killing it while it starts
# is a restart loop that looks like a crash.
HEALTHCHECK --interval=30s --timeout=10s --start-period=60s --retries=5 \
  CMD node -e "fetch('http://127.0.0.1:'+(process.env.PORT||3100)+'/login').then(r=>process.exit(r.ok?0:1)).catch(()=>process.exit(1))"

CMD ["node", "apps/app/server.js"]
