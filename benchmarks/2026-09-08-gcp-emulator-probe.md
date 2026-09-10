<!-- Produced by `just benchmark-emulators`, which runs
     engine/pkg/emulator/testdata/probe/run.sh. Every number below came out of
     that script on the machine and date named in it. The container half was
     NOT run: the Docker daemon on this machine stopped scheduling containers
     under memory pressure, and an estimate in a table of measurements is the
     thing this repository refuses to publish. -->

# GCP emulator probe, 2026-09-08

Machine: Darwin arm64, node v24.2.0, python 3.14.7

ca.crt and leaf.crt written
## Image sizes, from each registry

    fake-gcs-server 1.56.1             linux/amd64         25.4 MB compressed, 4 layers
    fake-gcs-server 1.56.1             linux/arm64         24.2 MB compressed, 4 layers
    google-cloud-cli 583.0.0-emulators linux/amd64        447.2 MB compressed, 7 layers
    google-cloud-cli 583.0.0-emulators linux/arm64        356.4 MB compressed, 7 layers
    cloud-spanner-emulator 1.5.57      single              71.2 MB compressed, 23 layers

## Reported in full by ./cases.sh

## Cloud Storage, two languages, no endpoint override

### JavaScript

    {
      "steps": [
        {
          "name": "createBucket",
          "ok": true,
          "ms": 502,
          "value": "af-l33-probe"
        },
        {
          "name": "upload",
          "ok": true,
          "ms": 208,
          "value": "hello.txt"
        },
        {
          "name": "download",
          "ok": true,
          "ms": 24,
          "value": "the twin wrote this"
        },
        {
          "name": "list",
          "ok": true,
          "ms": 28,
          "value": [
            "hello.txt"
          ]
        }
      ]
    }

### Python

    {
      "library": "google-cloud-storage",
      "version": "3.13.1",
      "steps": [
        {
          "name": "createBucket",
          "ok": true,
          "ms": 126,
          "value": "af-l33-probe-py"
        },
        {
          "name": "upload",
          "ok": true,
          "ms": 18,
          "value": "hello.txt"
        },
        {
          "name": "download",
          "ok": true,
          "ms": 7,
          "value": "the twin wrote this"
        },
        {
          "name": "list",
          "ok": true,
          "ms": 10,
          "value": [
            "hello.txt"
          ]
        }
      ]
    }

### Hosts each client asked for before its first storage call

    www.googleapis.com/oauth2/v4/token
    oauth2.googleapis.com/token

## gRPC through a proxy that reads HTTP/1.1, three runs

### reads HTTP/1.1, no ALPN, which is what af-proxy does

    ok: False  code: 4
    Total timeout of API google.pubsub.v1.Publisher exceeded 60000 milliseconds retrying error Error: 14 UNAVAILABLE: No connection established. Last error: read EC
    elapsed 80.48 seconds

./cases.sh: line 58: 21627 Terminated: 15          "$@" > /dev/null 2>&1
### reads HTTP/1.1, offers ALPN h2

    ok: False  code: 4
    Total timeout of API google.pubsub.v1.Publisher exceeded 60000 milliseconds retrying error Error: 14 UNAVAILABLE: No connection established. Last error: read EC
    elapsed 76.99 seconds

./cases.sh: line 58: 29032 Terminated: 15          "$@" > /dev/null 2>&1
### forwards the terminated socket to an HTTP/2 backend

    ok: False  code: 12
    12 UNIMPLEMENTED: the h2 sink answers nothing, and answering is the point
    elapsed 2.63 seconds

./cases.sh: line 58: 33617 Terminated: 15          "$@" > /dev/null 2>&1
./cases.sh: line 58: 33618 Terminated: 15          "$@" > /dev/null 2>&1
done
