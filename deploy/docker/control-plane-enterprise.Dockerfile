# The enterprise control plane image.
#
# THE GAP THIS CLOSES, AND IT IS THE SAME GAP ONE LEVEL UP. ee/web/server holds
# the enterprise entry point, and its own header records that four finished,
# tested enterprise packages had no process to mount them. That entry point now
# exists and runs, and until this file existed NO IMAGE BUILT IT, so the four
# licensed products were mounted by a binary that no deployment could ever pull.
# A program that runs under `node src/main.ts` on a laptop and ships in nothing
# is the same dead, shippable gap wearing a container.
#
# WHAT IS DIFFERENT FROM THE COMMUNITY IMAGE, AND IT IS ONLY THE LAYOUT.
# deploy/docker/control-plane.Dockerfile flattens web/ onto /app, because that
# image holds one npm workspace. This one holds TWO: the community workspace in
# web/ and the enterprise workspace in ee/web/, and the enterprise packages
# reach the community ones through `file:../../../web/apps/api` dependencies
# that npm installs as RELATIVE symlinks. A flattened layout breaks every one of
# those links, and it breaks them into a dangling symlink rather than into a
# build error, so the image would build clean and the process would die on its
# first import. So the repository's own shape is preserved: /app/web and
# /app/ee/web, exactly as they sit in a checkout.
#
# WORKDIR IS /app/web AND THAT IS LOAD BEARING. The bootstrap and maintenance
# entrypoints are invoked as `node bootstrap.mjs` by the Helm chart
# (templates/job-bootstrap.yaml), by Terraform (modules/control-plane/app.tf)
# and by the self-hosting documentation. Putting the working directory where
# the community image puts it means this image is a drop-in for those commands
# rather than a second set of paths for an operator to learn.
#
# THE BUILD CONTEXT, WHICH IS THE ONE THING THAT COULD NOT BE COPIED FROM THE
# SIBLING. The repository root .dockerignore excludes ee/, and ee/README.md
# names that exclusion as the fourth of the four mechanisms that keep the
# enterprise tree out of the community artifact. Weakening it to build this
# image would trade a real boundary for a packaging convenience. So this file
# carries its own ignore list in control-plane-enterprise.Dockerfile.dockerignore,
# which BuildKit reads in place of the root one for this Dockerfile only. The
# community context is untouched and still cannot see ee/.
#
# There is no compile step here either. The control plane is TypeScript run
# directly by Node's type stripping, so the image runs the same code path a
# developer runs.

# ---------------------------------------------------------------------------
# The community dependencies, which the enterprise packages resolve through.
#
# npm installs a `file:` dependency as a link and does NOT copy the target's
# own dependency tree into the consuming workspace: ee/web/package-lock.json
# records @antifailure/api as `"link": true` and carries none of its
# dependencies. Node resolves a symlinked module from its REAL path, so
# boot.ts's imports are answered out of /app/web/node_modules. Without this
# stage the enterprise install succeeds and the process cannot start, which is
# why `just test-ee` installs web/ before ee/web and says so in a comment.
# ---------------------------------------------------------------------------
FROM node:26-alpine AS deps

WORKDIR /app/web

COPY web/package.json web/package-lock.json ./
COPY web/apps/api/package.json ./apps/api/
COPY web/packages/db/package.json ./packages/db/
COPY web/packages/policy/package.json ./packages/policy/

# Scoped to the API workspace, the same flags and the same reason as the
# community image: a plain `npm ci` would install every workspace member's
# dependencies into an image that runs one server, and --ignore-scripts keeps a
# postinstall hook from executing at image build time.
RUN npm ci --omit=dev --ignore-scripts \
      --workspace @antifailure/api --include-workspace-root

RUN test ! -d node_modules/next \
  || (echo 'the web framework is in the API image: the workspace scoping above stopped working' && exit 1)

# ---------------------------------------------------------------------------
# The enterprise dependencies.
#
# NOT SCOPED TO ONE WORKSPACE, and that is deliberate rather than lazy. The
# server workspace links to scim and sso, which link to features, and an
# install narrowed to the server would be relying on npm to walk that chain of
# local links correctly under --omit=dev. The whole enterprise workspace is
# xml-crypto, xpath, xmldom, hono and the node server: installing all of it
# costs a few hundred kilobytes and removes a way for a transitively linked
# package to arrive empty.
# ---------------------------------------------------------------------------
FROM node:26-alpine AS eedeps

# The community manifests, at the path the enterprise `file:` dependencies name.
# Only the manifests: npm needs the link targets to exist and to be readable to
# build the tree, and nothing here needs their source.
WORKDIR /app/web
COPY web/apps/api/package.json ./apps/api/
COPY web/packages/db/package.json ./packages/db/

WORKDIR /app/ee/web
COPY ee/web/package.json ee/web/package-lock.json ./
COPY ee/web/features/package.json ./features/
COPY ee/web/rbac/package.json ./rbac/
COPY ee/web/audit/package.json ./audit/
COPY ee/web/sso/package.json ./sso/
COPY ee/web/scim/package.json ./scim/
COPY ee/web/server/package.json ./server/

RUN npm ci --omit=dev --ignore-scripts

# The same assertion the sibling stage makes, for the same reason. Nothing in
# the enterprise tree depends on a web framework, so its presence would mean
# the install pulled in something nobody asked for.
RUN test ! -d node_modules/next \
  || (echo 'the web framework is in the enterprise image: the install above stopped working' && exit 1)

# The link, resolved rather than assumed. `npm ci` writes
# node_modules/@antifailure/api as a relative symlink to ../../web/apps/api,
# and a symlink is created just as happily when it points at nothing. This is
# the check that the two workspaces are actually at the relative offset the
# lockfile believes, in the image, before anything is copied anywhere.
RUN test -f node_modules/@antifailure/api/package.json \
  || (echo 'the enterprise workspace cannot see the community one: the file: links are dangling' && exit 1)

# ---------------------------------------------------------------------------
# The console. Byte for byte what the community image builds, because it is the
# same console: the enterprise edition adds routes to the control plane, it
# does not fork the web application.
# ---------------------------------------------------------------------------
FROM node:26-alpine AS console

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

ARG AF_VERSION=dev
ARG AF_COMMIT=unknown
ARG AF_BUILD_DATE=unknown

LABEL org.opencontainers.image.title="Antifailure enterprise control plane" \
      org.opencontainers.image.description="The control plane with single sign-on and directory provisioning mounted, gated on a license." \
      org.opencontainers.image.source="https://github.com/antifailure/antifailure" \
      org.opencontainers.image.documentation="https://antifailure.dev/docs/self-hosting/control-plane/" \
      # NOT MIT, and this is the one label that must not be copied from the
      # sibling. Everything under ee/ is the Antifailure Enterprise License:
      # readable, auditable, modifiable, and not runnable in production without
      # a license key. An image asserting MIT over that content would be a
      # licensing claim this repository does not make anywhere else.
      org.opencontainers.image.licenses="LicenseRef-Antifailure-Enterprise" \
      org.opencontainers.image.version="${AF_VERSION}" \
      org.opencontainers.image.revision="${AF_COMMIT}" \
      org.opencontainers.image.created="${AF_BUILD_DATE}"

ENV NODE_ENV=production \
    AF_PORT=8080 \
    AF_VERSION=${AF_VERSION} \
    AF_COMMIT=${AF_COMMIT} \
    NODE_OPTIONS=--disable-warning=ExperimentalWarning

WORKDIR /app/web

COPY --from=deps /app/web/node_modules ./node_modules
COPY web/package.json ./
COPY web/apps/api ./apps/api
COPY web/packages/db ./packages/db
COPY web/packages/policy ./packages/policy

# The bootstrap entrypoint, at the working directory, so `node bootstrap.mjs`
# means the same thing here as it does in the community image. maintenance.mjs
# imports ./apps/api/src/maintenance.ts by that relative path, so this is not
# only a convenience: moving it would break the import.
COPY deploy/docker/bootstrap.mjs ./bootstrap.mjs
COPY deploy/docker/maintenance.mjs ./maintenance.mjs
COPY deploy/docker/personas.mjs ./personas.mjs

# The operator command, on PATH. The wrapper finds the API in either image's
# layout and refuses when it is in neither, which is why this file is copied
# unchanged from the community image rather than forked for this one.
COPY deploy/docker/af-operator /usr/local/bin/af-operator
RUN chmod 755 /usr/local/bin/af-operator

# The console build, where src/console/static.ts looks for it by default. That
# default is resolved four directories up from apps/api/src/console, which is
# /app/web here, so this image needs no AF_CONSOLE_DIR to find its own console.
COPY --from=console /console/out ./console-out

# The enterprise workspace, source and installed tree, at the offset its
# relative symlinks point back from.
WORKDIR /app/ee/web
COPY --from=eedeps /app/ee/web/node_modules ./node_modules
COPY ee/web/package.json ./
COPY ee/web/features ./features
COPY ee/web/rbac ./rbac
COPY ee/web/audit ./audit
COPY ee/web/sso ./sso
COPY ee/web/scim ./scim
COPY ee/web/server ./server
COPY ee/LICENSE.md /app/ee/LICENSE.md

WORKDIR /app/web

RUN test -n "$(ls -A ./packages/db/migrations)" || (echo 'no migrations in image' && exit 1)

RUN test -f ./console-out/index.html || (echo 'no console build in image' && exit 1)

# The two things that make this image an ENTERPRISE control plane rather than a
# larger community one, asserted here so that a layout mistake fails the build
# instead of failing at start-up in somebody's cluster.
#
# The first follows the symlink out of the enterprise tree and into the
# community one. If the two workspaces end up at any other relative offset,
# this file is not readable through that path and the process would die on its
# first import with a resolution error that names a package rather than a
# layout.
RUN test -f /app/ee/web/node_modules/@antifailure/api/src/boot.ts \
  || (echo 'the enterprise workspace cannot reach the community API through its link' && exit 1)
RUN test -f /app/ee/web/server/src/main.ts \
  || (echo 'the enterprise entry point is not in the image, which is the whole point of it' && exit 1)

USER node

EXPOSE 8080

# Liveness only, the same route, the same generous timeout and the same reason
# as the community image: /health is a static literal, so this answers "is the
# process up" and never "can it serve".
HEALTHCHECK --interval=30s --timeout=10s --start-period=30s --retries=5 \
  CMD node -e "fetch('http://127.0.0.1:'+(process.env.AF_PORT||8080)+'/health').then(r=>process.exit(r.ok?0:1)).catch(()=>process.exit(1))"

# Absolute, not relative to the working directory. The working directory is
# where the bootstrap and maintenance commands have to run from, and the entry
# point is in the other workspace; spelling it in full means the two facts
# cannot quietly drift into each other.
CMD ["node", "/app/ee/web/server/src/main.ts"]
