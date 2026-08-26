// Package proxyimage builds the egress sidecar's container image.
//
// The sidecar has to run inside the environment, so it has to be an image, so
// something has to compile it. This does, from source carried in the engine
// binary, which means af up works with no registry and no network beyond the
// Go base image. The alternative, a second implementation of the matching
// logic written to be standalone, would put two versions of a security control
// in one repository and the one that drifted would be the one deciding real
// traffic.
package proxyimage

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"sort"
	"time"
)

// dockerfile compiles the sidecar and ships nothing else.
//
// The runtime stage is scratch: the sidecar sits between an application and
// the internet, so it is the container in the environment with the most reason
// to hold nothing an attacker could use. No shell, no package manager, no libc.
const dockerfile = `FROM golang:1.25-alpine AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 GOFLAGS=-mod=mod go build -trimpath -o /out/af-proxy ./cmd/af-proxy

FROM scratch AS runtime
COPY --from=build /out/af-proxy /af-proxy
EXPOSE 3128
ENTRYPOINT ["/af-proxy"]
`

// epoch is the modification time every entry carries, so that the same sources
// produce the same archive and the same tag on every machine.
var epoch = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// Tag is the image reference, derived from the sources it is built from.
//
// Content addressed, so a change to the policy package produces a different
// image and an unchanged one is not rebuilt. A fixed tag would serve a stale
// sidecar after a policy change, which is the worst possible thing to be stale.
func Tag() string {
	h := sha256.New()
	for _, name := range names() {
		_, _ = io.WriteString(h, name)
		_, _ = h.Write([]byte{0})
		_, _ = io.WriteString(h, Sources[name])
		_, _ = h.Write([]byte{0})
	}
	_, _ = io.WriteString(h, dockerfile)
	return "antifailure/proxy:" + hex.EncodeToString(h.Sum(nil))[:16]
}

func names() []string {
	out := make([]string, 0, len(Sources))
	for n := range Sources {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// BuildContext returns the tar archive to hand the daemon.
func BuildContext() io.Reader {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	write := func(name, body string) {
		// Errors cannot happen writing to a buffer with a valid header, and a
		// truncated archive would fail inside the daemon with a message about
		// an unexpected EOF, so they are checked rather than ignored.
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o644, Size: int64(len(body)),
			ModTime: epoch, Format: tar.FormatPAX, Typeflag: tar.TypeReg,
		}); err != nil {
			panic("proxyimage: " + err.Error())
		}
		if _, err := io.WriteString(tw, body); err != nil {
			panic("proxyimage: " + err.Error())
		}
	}
	for _, name := range names() {
		write(name, Sources[name])
	}
	write("Dockerfile", dockerfile)
	if err := tw.Close(); err != nil {
		panic("proxyimage: " + err.Error())
	}
	return bytes.NewReader(buf.Bytes())
}
