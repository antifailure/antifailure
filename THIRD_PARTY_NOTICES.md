# Third party notices

Antifailure is MIT licensed, except for the `ee/` directory, which is
licensed under the Antifailure Enterprise License. This file lists the
dependencies the binary links, and is generated from them rather than
maintained by hand.

The list is the union over every platform a release publishes, because
one release ships all of them and a module can be linked on one platform
and not another. A list taken from a single platform attributes too few
people on every other one.

Platforms: darwin/amd64, darwin/arm64, linux/amd64, linux/arm64.

Run `just generate` to regenerate it. `just _generated` and CI both
regenerate it and fail on a difference, so a stale copy cannot be
committed. It used to go stale anyway, because for a long time the
generator ran only while building a release and nothing compared its
output against this file.

## Go modules (89)

- `github.com/aymanbagabas/go-osc52/v2` v2.0.1
- `github.com/cespare/xxhash/v2` v2.3.0
- `github.com/charmbracelet/bubbletea` v1.3.10
- `github.com/charmbracelet/colorprofile` v0.2.3-0.20250311203215-f60798e515dc
- `github.com/charmbracelet/lipgloss` v1.1.0
- `github.com/charmbracelet/x/ansi` v0.10.1
- `github.com/charmbracelet/x/cellbuf` v0.0.13-0.20250311204145-2c3ea96c31dd
- `github.com/charmbracelet/x/term` v0.2.1
- `github.com/containerd/errdefs` v1.0.0
- `github.com/containerd/errdefs/pkg` v0.3.0
- `github.com/davecgh/go-spew` v1.1.1
- `github.com/distribution/reference` v0.6.0
- `github.com/docker/docker` v28.5.1+incompatible
- `github.com/docker/go-connections` v0.6.0
- `github.com/docker/go-units` v0.5.0
- `github.com/dustin/go-humanize` v1.0.1
- `github.com/emicklei/go-restful/v3` v3.12.2
- `github.com/felixge/httpsnoop` v1.1.0
- `github.com/fxamacker/cbor/v2` v2.9.0
- `github.com/go-logr/logr` v1.4.4
- `github.com/go-logr/stdr` v1.2.2
- `github.com/go-openapi/jsonpointer` v0.21.0
- `github.com/go-openapi/jsonreference` v0.20.2
- `github.com/go-openapi/swag` v0.23.0
- `github.com/gogo/protobuf` v1.3.2
- `github.com/google/gnostic-models` v0.7.0
- `github.com/google/uuid` v1.6.0
- `github.com/jackc/pgpassfile` v1.0.0
- `github.com/jackc/pgservicefile` v0.0.0-20240606120523-5a60cdf6a761
- `github.com/jackc/pgx/v5` v5.9.2
- `github.com/jackc/puddle/v2` v2.2.2
- `github.com/josharian/intern` v1.0.0
- `github.com/json-iterator/go` v1.1.12
- `github.com/lucasb-eyer/go-colorful` v1.2.0
- `github.com/mailru/easyjson` v0.7.7
- `github.com/mattn/go-isatty` v0.0.20
- `github.com/mattn/go-runewidth` v0.0.16
- `github.com/moby/docker-image-spec` v1.3.1
- `github.com/modern-go/concurrent` v0.0.0-20180306012644-bacd9c7ef1dd
- `github.com/modern-go/reflect2` v1.0.3-0.20250322232337-35a7c28c31ee
- `github.com/muesli/ansi` v0.0.0-20230316100256-276c6243b2f6
- `github.com/muesli/cancelreader` v0.2.2
- `github.com/muesli/termenv` v0.16.0
- `github.com/munnerz/goautoneg` v0.0.0-20191010083416-a7dc8b61c822
- `github.com/ncruces/go-strftime` v0.1.9
- `github.com/opencontainers/go-digest` v1.0.0
- `github.com/opencontainers/image-spec` v1.1.1
- `github.com/pkg/errors` v0.9.1
- `github.com/remyoudompheng/bigfft` v0.0.0-20230129092748-24d4a6f8daec
- `github.com/rivo/uniseg` v0.4.7
- `github.com/spf13/cobra` v1.10.1
- `github.com/spf13/pflag` v1.0.9
- `github.com/x448/float16` v0.8.4
- `github.com/xo/terminfo` v0.0.0-20220910002029-abceb7e1c41e
- `go.opentelemetry.io/auto/sdk` v1.2.1
- `go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp` v0.70.0
- `go.opentelemetry.io/otel` v1.46.0
- `go.opentelemetry.io/otel/metric` v1.46.0
- `go.opentelemetry.io/otel/sdk` v1.46.0
- `go.opentelemetry.io/otel/trace` v1.46.0
- `go.yaml.in/yaml/v2` v2.4.2
- `go.yaml.in/yaml/v3` v3.0.5
- `golang.org/x/crypto` v0.55.0
- `golang.org/x/exp` v0.0.0-20250620022241-b7579e27df2b
- `golang.org/x/net` v0.58.0
- `golang.org/x/oauth2` v0.27.0
- `golang.org/x/sync` v0.22.0
- `golang.org/x/sys` v0.47.0
- `golang.org/x/term` v0.45.0
- `golang.org/x/text` v0.41.0
- `golang.org/x/time` v0.15.0
- `google.golang.org/protobuf` v1.36.12
- `gopkg.in/evanphx/json-patch.v4` v4.12.0
- `gopkg.in/inf.v0` v0.9.1
- `gopkg.in/yaml.v3` v3.0.1
- `k8s.io/api` v0.34.1
- `k8s.io/apimachinery` v0.34.1
- `k8s.io/client-go` v0.34.1
- `k8s.io/klog/v2` v2.130.1
- `k8s.io/kube-openapi` v0.0.0-20250710124328-f3f2b991d03b
- `k8s.io/utils` v0.0.0-20250604170112-4c0f3b243397
- `modernc.org/libc` v1.66.10
- `modernc.org/mathutil` v1.7.1
- `modernc.org/memory` v1.11.0
- `modernc.org/sqlite` v1.39.1
- `sigs.k8s.io/json` v0.0.0-20241014173422-cfa47c3a1cc8
- `sigs.k8s.io/randfill` v1.0.0
- `sigs.k8s.io/structured-merge-diff/v6` v6.3.0
- `sigs.k8s.io/yaml` v1.6.0

## Node packages

The agent runner depends on Playwright, which is Apache 2.0 licensed,
and on its own transitive dependencies. Run `npm ls --all` inside
`runner/` for the full tree of whatever version is installed.
