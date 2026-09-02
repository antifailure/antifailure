# Multi stage, so the image that runs carries a binary and nothing else: no
# compiler, no module cache, no source. It is the smallest thing that can be
# the service, which is what makes an image worth scanning.
FROM golang:1.27-alpine AS build
WORKDIR /src

# Dependencies first, so a change to the source does not re-download them.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/api .

FROM alpine:3.24
# psql, because the manifest's migrate command uses it and it runs inside this
# image. Leaving it out made `af up` wait on a migration that could never run,
# which the walkthrough found and reading the Dockerfile did not.
#
# postgresql-client, not postgresql17-client. Alpine 3.20 has no such package
# and `apk add` fails the build; the unversioned name gives psql 16, which
# talks to a 17 server perfectly well.
RUN apk add --no-cache postgresql-client && adduser -D -u 10001 api
USER api

# The migration runs in this image, as a one shot container with the manifest's
# migrate command in place of the entrypoint. That command names
# migrations/0001_init.sql by a relative path, so the file has to be in the
# image and the working directory has to be the one it is relative to. Shipping
# psql without the migrations was half a fix: the first run then failed with
# "No such file or directory" instead of "psql: not found".
WORKDIR /app
COPY migrations ./migrations

COPY --from=build /out/api /usr/local/bin/api
EXPOSE 3000
ENTRYPOINT ["/usr/local/bin/api"]
