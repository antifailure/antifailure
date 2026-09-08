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

## Go modules (98)

- `github.com/aymanbagabas/go-osc52/v2` v2.0.1
- `github.com/cespare/xxhash/v2` v2.3.0
- `github.com/charmbracelet/bubbletea` v1.3.10
- `github.com/charmbracelet/colorprofile` v0.4.1
- `github.com/charmbracelet/lipgloss` v1.1.0
- `github.com/charmbracelet/x/ansi` v0.11.8
- `github.com/charmbracelet/x/cellbuf` v0.0.15
- `github.com/charmbracelet/x/term` v0.2.2
- `github.com/clipperhouse/displaywidth` v0.11.0
- `github.com/clipperhouse/uax29/v2` v2.7.0
- `github.com/containerd/errdefs` v1.0.0
- `github.com/containerd/errdefs/pkg` v0.3.0
- `github.com/davecgh/go-spew` v1.1.2-0.20180830191138-d8f796af33cc
- `github.com/distribution/reference` v0.6.0
- `github.com/docker/docker` v28.5.2+incompatible
- `github.com/docker/go-connections` v0.8.1
- `github.com/docker/go-units` v0.5.0
- `github.com/dustin/go-humanize` v1.0.1
- `github.com/emicklei/go-restful/v3` v3.13.0
- `github.com/felixge/httpsnoop` v1.1.0
- `github.com/fxamacker/cbor/v2` v2.9.1
- `github.com/go-logr/logr` v1.4.4
- `github.com/go-logr/stdr` v1.2.2
- `github.com/go-openapi/jsonpointer` v1.0.0
- `github.com/go-openapi/jsonreference` v1.0.0
- `github.com/go-openapi/swag` v0.27.1
- `github.com/go-openapi/swag/cmdutils` v0.27.1
- `github.com/go-openapi/swag/conv` v0.27.1
- `github.com/go-openapi/swag/fileutils` v0.27.1
- `github.com/go-openapi/swag/jsonutils` v0.27.1
- `github.com/go-openapi/swag/loading` v0.27.1
- `github.com/go-openapi/swag/mangling` v0.27.1
- `github.com/go-openapi/swag/netutils` v0.27.1
- `github.com/go-openapi/swag/pools` v0.27.1
- `github.com/go-openapi/swag/stringutils` v0.27.1
- `github.com/go-openapi/swag/typeutils` v0.27.1
- `github.com/go-openapi/swag/yamlutils` v0.27.1
- `github.com/google/gnostic-models` v0.7.0
- `github.com/google/uuid` v1.6.0
- `github.com/jackc/pgpassfile` v1.0.0
- `github.com/jackc/pgservicefile` v0.0.0-20240606120523-5a60cdf6a761
- `github.com/jackc/pgx/v5` v5.10.0
- `github.com/jackc/puddle/v2` v2.2.2
- `github.com/json-iterator/go` v1.1.12
- `github.com/lucasb-eyer/go-colorful` v1.4.0
- `github.com/mattn/go-isatty` v0.0.24
- `github.com/mattn/go-runewidth` v0.0.24
- `github.com/moby/docker-image-spec` v1.3.1
- `github.com/modern-go/concurrent` v0.0.0-20180306012644-bacd9c7ef1dd
- `github.com/modern-go/reflect2` v1.0.3-0.20250322232337-35a7c28c31ee
- `github.com/muesli/ansi` v0.0.0-20230316100256-276c6243b2f6
- `github.com/muesli/cancelreader` v0.2.2
- `github.com/muesli/termenv` v0.16.0
- `github.com/munnerz/goautoneg` v0.0.0-20191010083416-a7dc8b61c822
- `github.com/ncruces/go-strftime` v1.0.0
- `github.com/opencontainers/go-digest` v1.0.0
- `github.com/opencontainers/image-spec` v1.1.1
- `github.com/pkg/errors` v0.9.1
- `github.com/remyoudompheng/bigfft` v0.0.0-20230129092748-24d4a6f8daec
- `github.com/rivo/uniseg` v0.4.7
- `github.com/spf13/cobra` v1.10.2
- `github.com/spf13/pflag` v1.0.10
- `github.com/x448/float16` v0.8.4
- `github.com/xo/terminfo` v0.0.0-20220910002029-abceb7e1c41e
- `go.opentelemetry.io/auto/sdk` v1.2.1
- `go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp` v0.70.0
- `go.opentelemetry.io/otel` v1.46.0
- `go.opentelemetry.io/otel/metric` v1.46.0
- `go.opentelemetry.io/otel/sdk` v1.46.0
- `go.opentelemetry.io/otel/trace` v1.46.0
- `go.yaml.in/yaml/v2` v2.4.4
- `go.yaml.in/yaml/v3` v3.0.5
- `golang.org/x/crypto` v0.56.0
- `golang.org/x/net` v0.58.0
- `golang.org/x/oauth2` v0.36.0
- `golang.org/x/sync` v0.22.0
- `golang.org/x/sys` v0.47.0
- `golang.org/x/term` v0.45.0
- `golang.org/x/text` v0.41.0
- `golang.org/x/time` v0.15.0
- `google.golang.org/protobuf` v1.36.12
- `gopkg.in/evanphx/json-patch.v4` v4.13.0
- `gopkg.in/inf.v0` v0.9.1
- `gopkg.in/yaml.v3` v3.0.1
- `k8s.io/api` v0.37.0
- `k8s.io/apimachinery` v0.37.0
- `k8s.io/client-go` v0.37.0
- `k8s.io/klog/v2` v2.140.0
- `k8s.io/kube-openapi` v0.0.0-20260721132016-d427ff9ee9ad
- `k8s.io/utils` v0.0.0-20260626114624-be93311217bd
- `modernc.org/libc` v1.75.6
- `modernc.org/mathutil` v1.7.1
- `modernc.org/memory` v1.12.1
- `modernc.org/sqlite` v1.58.0
- `sigs.k8s.io/json` v0.0.0-20250730193827-2d320260d730
- `sigs.k8s.io/randfill` v1.0.0
- `sigs.k8s.io/structured-merge-diff/v6` v6.4.2
- `sigs.k8s.io/yaml` v1.6.0

## Container images

An environment starts an emulator when a manifest asks for one, and an
emulator is somebody else's software running beside the application.
It is not linked into the binary, so the module list above cannot see
it, and an image whose licence is recorded by hand goes stale the
first time a digest is bumped. These come from the declarations the
engine starts the containers from.

- LocalStack, Apache License 2.0. Copyright (c) 2017+ LocalStack contributors, Copyright (c) 2016 Atlassian Pty Ltd
  - Answers for AWS as `aws`
  - `localstack/localstack@sha256:4aef81c531684570d7b3cfd2805afa02194c929d52bbeedabb7d4874798b1572`
  - https://github.com/localstack/localstack/blob/main/LICENSE.txt
- Google Cloud CLI emulators, Apache License 2.0. Copyright Google LLC. /google-cloud-sdk/LICENSE inside the image is the grant, and it adds that use against a Google Cloud product is additionally governed by that product's own terms.
  - Answers for Google Cloud as `bigtable`
  - `gcr.io/google.com/cloudsdktool/google-cloud-cli@sha256:07e4b8c3075ca793552fcfaf4808f104ef155d7805d87ade8e01b440463be262`
  - https://www.apache.org/licenses/LICENSE-2.0
- Google Cloud CLI emulators, Apache License 2.0. Copyright Google LLC. /google-cloud-sdk/LICENSE inside the image is the grant, and it adds that use against a Google Cloud product is additionally governed by that product's own terms.
  - Answers for Google Cloud as `datastore`
  - `gcr.io/google.com/cloudsdktool/google-cloud-cli@sha256:07e4b8c3075ca793552fcfaf4808f104ef155d7805d87ade8e01b440463be262`
  - https://www.apache.org/licenses/LICENSE-2.0
- Google Cloud CLI emulators, Apache License 2.0. Copyright Google LLC. /google-cloud-sdk/LICENSE inside the image is the grant, and it adds that use against a Google Cloud product is additionally governed by that product's own terms.
  - Answers for Google Cloud as `firestore`
  - `gcr.io/google.com/cloudsdktool/google-cloud-cli@sha256:07e4b8c3075ca793552fcfaf4808f104ef155d7805d87ade8e01b440463be262`
  - https://www.apache.org/licenses/LICENSE-2.0
- fake-gcs-server, BSD 2-Clause License. Copyright (c) Francisco Souza. Not affiliated with Google.
  - Answers for Google Cloud as `gcs`
  - `fsouza/fake-gcs-server@sha256:797ce226d62f947c009dc40246b30cfb456b8473d8241407f9d6f2c04e4d69ef`
  - https://github.com/fsouza/fake-gcs-server/blob/main/LICENSE
- Google Cloud CLI emulators, Apache License 2.0. Copyright Google LLC. /google-cloud-sdk/LICENSE inside the image is the grant, and it adds that use against a Google Cloud product is additionally governed by that product's own terms.
  - Answers for Google Cloud as `pubsub`
  - `gcr.io/google.com/cloudsdktool/google-cloud-cli@sha256:07e4b8c3075ca793552fcfaf4808f104ef155d7805d87ade8e01b440463be262`
  - https://www.apache.org/licenses/LICENSE-2.0
- Cloud Spanner Emulator, Apache License 2.0. Copyright Google LLC
  - Answers for Google Cloud as `spanner`
  - `gcr.io/cloud-spanner-emulator/emulator@sha256:4987860c9f8ecf1fffbbcdac115cb88cb9d1a42bd966c235a9ab843aea34fbd1`
  - https://github.com/GoogleCloudPlatform/cloud-spanner-emulator/blob/master/LICENSE

## Node packages

The agent runner depends on Playwright, which is Apache 2.0 licensed,
and on its own transitive dependencies. Run `npm ls --all` inside
`runner/` for the full tree of whatever version is installed.
