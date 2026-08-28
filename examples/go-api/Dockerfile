# Multi stage, so the image that runs carries a binary and nothing else: no
# compiler, no module cache, no source. It is the smallest thing that can be
# the service, which is what makes an image worth scanning.
FROM golang:1.25-alpine AS build
WORKDIR /src

# Dependencies first, so a change to the source does not re-download them.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/api .

FROM alpine:3.20
RUN adduser -D -u 10001 api
USER api
COPY --from=build /out/api /usr/local/bin/api
EXPOSE 3000
ENTRYPOINT ["/usr/local/bin/api"]
