// Package proxyimage names and builds the egress sidecar's container image.
//
// The sidecar has to run inside the environment, so it has to be an image, so
// something has to produce it. There are two ways and this package describes
// both: a release publishes the image, and the engine can compile it from
// source carried in the binary. The source build is what makes af up work on a
// development commit that no release covers, and on a machine with no route to
// a registry. The alternative, a second implementation of the matching logic
// written to be standalone, would put two versions of a security control in
// one repository and the one that drifted would be the one deciding real
// traffic.
//
// WHY THE IDENTITY IS A CONTENT DIGEST AND NOT A VERSION. Both references this
// package produces carry SourcesDigest as their tag. A version number would
// name bytes that no longer match the source the moment somebody edited a
// policy file without cutting a release, and a stale sidecar is the worst thing
// in this product to be stale: it is the component deciding what an
// environment may reach. A content digest cannot be stale. It can only be
// ABSENT, which is a pull that finds nothing and a build that fills the gap.
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
//
// THE BASE IMAGE IS PINNED BY DIGEST, AND THE CONTENT ADDRESS IS WHY. Tag and
// PublishedRef hash this TEXT together with the sources. While the first line
// read `FROM golang:1.25-alpine`, Docker Hub could repoint that tag with no
// change here, so the hash stayed put while the compiler under it moved: a
// machine that already held the image never rebuilt, and a fresh machine built
// a DIFFERENT binary and stored it under the SAME content addressed name. Two
// sidecars sharing one identity is the stale sidecar hazard from the other
// side. With a digest, the text names the bytes, and moving the compiler is a
// commit that changes the tag. The digest is the multi architecture index, so
// one line serves an amd64 daemon and an arm64 one alike, and
// TestEveryContainerImageIsPinnedToADigest in tools/gatecheck reads this
// literal. It is refreshed the way ci.yml says to refresh every other pin.
//
// The Dockerfile is kept to what the classic builder accepts, because
// ImageBuild on a daemon with BuildKit switched off takes this same text: no
// BUILDPLATFORM, no heredocs, no cache mounts.
const dockerfile = `FROM golang:1.25-alpine@sha256:1ae0735f00daffa3aaf1363a5184c0d2dc55c78e3db4ec70241cdac97bf84b59 AS build
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

// LocalRepository is the image name the engine gives the sidecar on a
// container daemon it can reach. It carries no registry host, so nothing ever
// tries to push it or pull it by this name.
const LocalRepository = "antifailure/proxy"

// PublishedRepository is where a release publishes the sidecar image.
//
// .github/workflows/release.yml pushes exactly this repository with
// SourcesDigest as the tag, and refuses to run when the owner half does not
// match the repository the workflow is running for, so a fork cannot quietly
// publish under this name and this constant cannot quietly stop naming what
// the release publishes.
const PublishedRepository = "ghcr.io/antifailure/af-proxy"

// SourcesLabel is the label an image of this sidecar carries, whose value is
// the SourcesDigest of the sources it was built from.
//
// IT IS THE INVARIANT, AND THE TAG IS ONLY A NAME. A tag is a label in a
// registry and whoever can push can move it; the bytes are what run. So every
// image this engine builds carries this label, the release publishes it, and
// anything the engine PULLS is required to carry the digest this binary
// expects before it is used. A published image that was built from different
// source is then refused rather than run, whatever it was called, and the
// engine falls back to compiling the source it holds.
const SourcesLabel = "dev.antifailure.proxy-sources"

// SourcesDigest is the content id of the sidecar: every packaged file, its
// path, and the Dockerfile, hashed.
//
// Content addressed, so a change to the policy package produces a different
// image and an unchanged one is neither rebuilt nor re-pulled. A fixed version
// would serve a stale sidecar after a policy change, which is the worst
// possible thing to be stale.
func SourcesDigest() string {
	h := sha256.New()
	for _, name := range names() {
		_, _ = io.WriteString(h, name)
		_, _ = h.Write([]byte{0})
		_, _ = io.WriteString(h, Sources[name])
		_, _ = h.Write([]byte{0})
	}
	_, _ = io.WriteString(h, dockerfile)
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// Tag is the local image reference, derived from the sources it is built from.
func Tag() string { return LocalRepository + ":" + SourcesDigest() }

// PublishedRef is the published image holding the sidecar this binary carries.
//
// A release of this commit publishes it. A development commit that changed any
// packaged file has a digest no release ever published, so the pull finds
// nothing and the source build is the path. That is the normal case for a
// contributor and it is why the build cannot be removed.
func PublishedRef() string { return PublishedRepository + ":" + SourcesDigest() }

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
