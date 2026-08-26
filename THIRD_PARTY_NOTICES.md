# Third party notices

Antifailure is MIT licensed, except for the `ee/` directory, which is
licensed under the Antifailure Enterprise License. This file lists the
dependencies the binary links, and is generated from them rather than
maintained by hand, so it cannot go stale.

Run `go run ./tools/notices` to regenerate it.

## Go modules (40)

- `github.com/cespare/xxhash/v2` v2.3.0
- `github.com/containerd/errdefs` v1.0.0
- `github.com/containerd/errdefs/pkg` v0.3.0
- `github.com/distribution/reference` v0.6.0
- `github.com/docker/docker` v28.5.1+incompatible
- `github.com/docker/go-connections` v0.6.0
- `github.com/docker/go-units` v0.5.0
- `github.com/dustin/go-humanize` v1.0.1
- `github.com/felixge/httpsnoop` v1.1.0
- `github.com/go-logr/logr` v1.4.4
- `github.com/go-logr/stdr` v1.2.2
- `github.com/google/uuid` v1.6.0
- `github.com/jackc/pgpassfile` v1.0.0
- `github.com/jackc/pgservicefile` v0.0.0-20240606120523-5a60cdf6a761
- `github.com/jackc/pgx/v5` v5.7.6
- `github.com/jackc/puddle/v2` v2.2.2
- `github.com/mattn/go-isatty` v0.0.20
- `github.com/moby/docker-image-spec` v1.3.1
- `github.com/ncruces/go-strftime` v0.1.9
- `github.com/opencontainers/go-digest` v1.0.0
- `github.com/opencontainers/image-spec` v1.1.1
- `github.com/pkg/errors` v0.9.1
- `github.com/remyoudompheng/bigfft` v0.0.0-20230129092748-24d4a6f8daec
- `github.com/spf13/cobra` v1.10.1
- `github.com/spf13/pflag` v1.0.9
- `go.opentelemetry.io/auto/sdk` v1.2.1
- `go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp` v0.70.0
- `go.opentelemetry.io/otel` v1.46.0
- `go.opentelemetry.io/otel/metric` v1.46.0
- `go.opentelemetry.io/otel/trace` v1.46.0
- `golang.org/x/crypto` v0.44.0
- `golang.org/x/exp` v0.0.0-20250620022241-b7579e27df2b
- `golang.org/x/sync` v0.22.0
- `golang.org/x/sys` v0.47.0
- `golang.org/x/text` v0.41.0
- `gopkg.in/yaml.v3` v3.0.1
- `modernc.org/libc` v1.66.10
- `modernc.org/mathutil` v1.7.1
- `modernc.org/memory` v1.11.0
- `modernc.org/sqlite` v1.39.1

## Node packages

The agent runner depends on Playwright, which is Apache 2.0 licensed,
and on its own transitive dependencies. Run `npm ls --all` inside
`runner/` for the full tree of whatever version is installed.
