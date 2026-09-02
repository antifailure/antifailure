# Three stages, so the image that runs carries the server and the modules it
# actually reached rather than a toolchain and a module cache.
FROM node:26-alpine AS deps
WORKDIR /app
# The lockfile first, so a change to a page does not reinstall the world.
COPY package.json package-lock.json ./
RUN npm ci

FROM node:26-alpine AS build
WORKDIR /app
COPY --from=deps /app/node_modules ./node_modules
COPY . .
# This build must not need a database. app/page.tsx is force-dynamic and the
# pool in lib/db.ts is created on first use, which together keep the build from
# trying to connect to something that does not exist yet.
RUN npm run build

FROM node:26-alpine
# psql, because the manifest's migrate command uses it and that command runs in
# this image. postgresql-client, not postgresql17-client: Alpine has no such
# package, and the unversioned name gives psql 16, which talks to a 17 server.
RUN apk add --no-cache postgresql-client \
 && addgroup -S app && adduser -S -u 10001 -G app app
WORKDIR /app

# next.config.ts sets output: "standalone", which produces a server and a
# pruned node_modules under .next/standalone. Static assets are not included in
# it and have to be copied separately, which is the step everyone misses: the
# page renders and arrives with no CSS.
COPY --from=build /app/.next/standalone ./
COPY --from=build /app/.next/static ./.next/static

# The migration reads this by a relative path, so it has to be in the image and
# the working directory has to be the one it is relative to.
COPY migrations ./migrations

USER app
EXPOSE 3000
# Standalone binds to the value of HOSTNAME. Left unset it has bound to
# localhost in some versions, which inside a container means the port is open
# and nothing outside the container can reach it: a service that looks started
# and never becomes ready.
ENV HOSTNAME=0.0.0.0
ENV PORT=3000
CMD ["node", "server.js"]
