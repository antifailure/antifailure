package local

// The ordering table for obtaining the sidecar image.
//
// One test per cell, each against a daemon whose answers are chosen, because
// not one of these orderings can be produced on demand against a real one: a
// registry that answers, a registry that has nothing, a pull that never
// returns, a base image pull that stalls inside a build, and a daemon too busy
// to say whether it holds an image. The real client satisfies the same
// interface, and the live half of this (a real af up against a real daemon)
// is recorded in the pull request rather than here.
//
// Not parallel. The air gap guard is process wide, and two of these seal it.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	dockerbuild "github.com/docker/docker/api/types/build"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/proxyimage"
	"github.com/antifailure/antifailure/engine/internal/redact"
	"github.com/antifailure/antifailure/engine/pkg/airgap"
)

// fakeDaemon answers the four calls obtaining the sidecar makes.
type fakeDaemon struct {
	mu         sync.Mutex
	images     map[string]image.InspectResponse
	inspectErr map[string]error
	pull       func(ctx context.Context, ref string) (io.ReadCloser, error)
	build      func(ctx context.Context, opts dockerbuild.ImageBuildOptions) (io.ReadCloser, error)

	pulled    []string
	builds    int
	buildOpts dockerbuild.ImageBuildOptions
}

func newFakeDaemon() *fakeDaemon {
	return &fakeDaemon{images: map[string]image.InspectResponse{}, inspectErr: map[string]error{}}
}

func (d *fakeDaemon) ImageInspect(
	_ context.Context, ref string, _ ...client.ImageInspectOption,
) (image.InspectResponse, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.inspectErr[ref]; err != nil {
		return image.InspectResponse{}, err
	}
	if img, ok := d.images[ref]; ok {
		return img, nil
	}
	return image.InspectResponse{}, fmt.Errorf("No such image: %s: %w", ref, cerrdefs.ErrNotFound)
}

func (d *fakeDaemon) ImagePull(ctx context.Context, ref string, _ image.PullOptions) (io.ReadCloser, error) {
	d.mu.Lock()
	d.pulled = append(d.pulled, ref)
	pull := d.pull
	d.mu.Unlock()
	if pull == nil {
		return nil, fmt.Errorf("manifest for %s not found: manifest unknown: %w", ref, cerrdefs.ErrNotFound)
	}
	return pull(ctx, ref)
}

func (d *fakeDaemon) ImageTag(_ context.Context, source, target string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	img, ok := d.images[source]
	if !ok {
		return fmt.Errorf("No such image: %s: %w", source, cerrdefs.ErrNotFound)
	}
	d.images[target] = img
	return nil
}

func (d *fakeDaemon) ImageBuild(
	ctx context.Context, _ io.Reader, opts dockerbuild.ImageBuildOptions,
) (dockerbuild.ImageBuildResponse, error) {
	d.mu.Lock()
	d.builds++
	d.buildOpts = opts
	build := d.build
	d.mu.Unlock()
	if build == nil {
		return dockerbuild.ImageBuildResponse{}, errors.New("Cannot connect to the Docker daemon")
	}
	rc, err := build(ctx, opts)
	return dockerbuild.ImageBuildResponse{Body: rc}, err
}

func (d *fakeDaemon) put(ref string, img image.InspectResponse) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.images[ref] = img
}

func (d *fakeDaemon) has(ref string) (image.InspectResponse, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	img, ok := d.images[ref]
	return img, ok
}

func (d *fakeDaemon) counts() (pulls, builds int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.pulled), d.builds
}

// declaring is an image whose label says which sidecar source built it. An
// empty digest is an image with no label at all, which is what an engine
// released before the label existed built.
func declaring(digest string) image.InspectResponse {
	labels := map[string]string{}
	if digest != "" {
		labels[proxyimage.SourcesLabel] = digest
	}
	return image.InspectResponse{
		ID:     "sha256:" + strings.Repeat("a", 64),
		Config: &dockerspec.DockerOCIImageConfig{ImageConfig: ocispec.ImageConfig{Labels: labels}},
	}
}

// stream is a daemon's JSON progress, one message per line.
func stream(lines ...string) io.ReadCloser {
	return io.NopCloser(strings.NewReader(strings.Join(lines, "\n") + "\n"))
}

// stalling yields lines and then says nothing until ctx ends, which is what a
// pull on a network refusing TLS handshakes looks like from the client.
func stalling(ctx context.Context, lines ...string) io.ReadCloser {
	pr, pw := io.Pipe()
	go func() {
		for _, l := range lines {
			if _, err := io.WriteString(pw, l+"\n"); err != nil {
				return
			}
		}
		<-ctx.Done()
		_ = pw.CloseWithError(ctx.Err())
	}()
	return pr
}

// publishes makes the registry hold an image under ref declaring digest.
func (d *fakeDaemon) publishes(digest string) {
	d.pull = func(_ context.Context, ref string) (io.ReadCloser, error) {
		d.put(ref, declaring(digest))
		return stream(
			`{"status":"Pulling from antifailure/af-proxy","id":"x"}`,
			`{"status":"Pulling fs layer","id":"1"}`,
			`{"status":"Download complete","id":"1"}`,
		), nil
	}
}

// compiles makes a build succeed, storing an image with the labels asked for.
func (d *fakeDaemon) compiles() {
	d.build = func(_ context.Context, opts dockerbuild.ImageBuildOptions) (io.ReadCloser, error) {
		img := declaring("")
		img.Config.Labels = opts.Labels
		for _, tag := range opts.Tags {
			d.put(tag, img)
		}
		return stream(
			`{"stream":"Step 1/7 : FROM golang:1.25-alpine@sha256:1ae0 AS build\n"}`,
			`{"stream":"Step 4/7 : RUN go build ./cmd/af-proxy\n"}`,
			`{"stream":"Successfully built 0123456789ab\n"}`,
		), nil
	}
}

// recorder collects progress lines, safely, because the heartbeat writes from
// its own goroutine.
type recorder struct {
	mu    sync.Mutex
	lines []string
	seen  chan string
}

func newRecorder() *recorder { return &recorder{seen: make(chan string, 256)} }

func (r *recorder) add(line string) {
	r.mu.Lock()
	r.lines = append(r.lines, line)
	r.mu.Unlock()
	select {
	case r.seen <- line:
	default:
	}
}

func (r *recorder) text() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.lines, "\n")
}

// waitFor blocks until a progress line containing want arrives.
func (r *recorder) waitFor(t *testing.T, want string) {
	t.Helper()
	if strings.Contains(r.text(), want) {
		return
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case line := <-r.seen:
			if strings.Contains(line, want) {
				return
			}
		case <-deadline:
			t.Fatalf("no progress line containing %q arrived. The lines were:\n%s", want, r.text())
		}
	}
}

func newJob(d *fakeDaemon, env map[string]string) (*proxyImageJob, *recorder) {
	rec := newRecorder()
	return &proxyImageJob{
		daemon:   d,
		clock:    clock.New(),
		redactor: redact.New(),
		labels:   map[string]string{dockerutil.LabelManaged: "true"},
		getenv:   func(k string) string { return env[k] },
		progress: rec.add,
	}, rec
}

// obtainWithin runs obtain and fails the test if it has not returned within
// limit. The cells below are about bounded waits, so the test itself must be
// bounded: a removed timeout should fail THIS test by name in seconds, not
// hang the package until go test's own ten minute limit reports nothing
// useful about which bound went missing.
func obtainWithin(t *testing.T, job *proxyImageJob, limit time.Duration) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- job.obtain(context.Background()) }()
	select {
	case err := <-done:
		return err
	case <-time.After(limit):
		t.Fatalf("obtaining the sidecar image had not returned after %s, so the bound under test is not "+
			"being applied", limit)
		return nil
	}
}

func requireCode(t *testing.T, err error, code aferrors.Code) {
	t.Helper()
	require.Error(t, err)
	var coded *aferrors.Error
	require.True(t, errors.As(err, &coded), "not a catalog error: %v", err)
	require.Equal(t, code, coded.Code(), "%v", err)
}

// ---------------------------------------------------------------------------
// The ordering table.
// ---------------------------------------------------------------------------

func TestProxyImage_AlreadyPresentIsUsedAndNothingIsFetchedOrBuilt(t *testing.T) {
	d := newFakeDaemon()
	d.put(proxyimage.Tag(), declaring(proxyimage.SourcesDigest()))
	d.publishes(proxyimage.SourcesDigest())
	d.compiles()

	job, _ := newJob(d, nil)
	require.NoError(t, job.obtain(context.Background()))
	pulls, builds := d.counts()
	require.Zero(t, pulls, "an image already on the daemon was fetched again")
	require.Zero(t, builds, "an image already on the daemon was compiled again")
}

func TestProxyImage_AnUnlabelledImageUnderItsOwnNameIsStillUsed(t *testing.T) {
	// Built by an engine released before the label existed, from the same
	// source, under the same content addressed name. Refusing it would
	// recompile the sidecar on every machine that has ever run af.
	d := newFakeDaemon()
	d.put(proxyimage.Tag(), declaring(""))
	job, _ := newJob(d, nil)
	require.NoError(t, job.obtain(context.Background()))
	pulls, builds := d.counts()
	require.Zero(t, pulls+builds)
}

func TestProxyImage_AbsentIsFetchedFromTheReleaseAndNotCompiled(t *testing.T) {
	d := newFakeDaemon()
	d.publishes(proxyimage.SourcesDigest())
	d.compiles()

	job, rec := newJob(d, nil)
	require.NoError(t, job.obtain(context.Background()))

	pulls, builds := d.counts()
	require.Equal(t, 1, pulls)
	// The literal reference, not proxyimage.PublishedRef(). Comparing against
	// that function is comparing it with itself: a mutation replacing the
	// content digest with a floating :latest changed both sides of the equality
	// and this assertion stayed green, which is the one mutant that survived
	// the first run of this table. What matters is that the fetch asks for a
	// reference carrying THIS source digest, so that is written out.
	require.Equal(t, []string{"ghcr.io/antifailure/af-proxy:" + proxyimage.SourcesDigest()}, d.pulled,
		"the fetch has to ask the release's registry for this engine's own source digest")
	require.Zero(t, builds, "a fetch that succeeded was followed by a compile anyway")

	img, ok := d.has(proxyimage.Tag())
	require.True(t, ok, "the fetched image was not named %s, so the container create would not find it",
		proxyimage.Tag())
	require.Equal(t, proxyimage.SourcesDigest(), img.Config.Labels[proxyimage.SourcesLabel])
	require.Contains(t, rec.text(), "Pulling fs layer", "the pull's own progress was not reported")
}

func TestProxyImage_AFetchThatFindsNothingFallsBackToCompiling(t *testing.T) {
	// A development commit: the digest changed, so no release published it.
	// This is every contributor's path and it cannot become the broken one.
	d := newFakeDaemon()
	d.compiles()

	job, rec := newJob(d, nil)
	require.NoError(t, job.obtain(context.Background()))

	pulls, builds := d.counts()
	require.Equal(t, 1, pulls)
	require.Equal(t, 1, builds)
	require.Equal(t, []string{proxyimage.Tag()}, d.buildOpts.Tags)
	require.Equal(t, proxyimage.SourcesDigest(), d.buildOpts.Labels[proxyimage.SourcesLabel],
		"an image this engine compiles must declare its source, or an operator who pushes it "+
			"to their own registry has an image no engine will accept")
	require.Equal(t, "true", d.buildOpts.Labels[dockerutil.LabelManaged],
		"the managed labels were dropped, so af down could no longer see this image")
	require.Contains(t, rec.text(), "Step 4/7", "the build's own steps were not reported")
}

func TestProxyImage_WhenBothFailTheErrorNamesBoth(t *testing.T) {
	d := newFakeDaemon() // no registry answer, no build

	job, _ := newJob(d, nil)
	err := job.obtain(context.Background())
	requireCode(t, err, aferrors.AFRUN048)
	require.Contains(t, err.Error(), "Fetching it:", "the fetch's failure was dropped")
	require.Contains(t, err.Error(), "manifest unknown")
	require.Contains(t, err.Error(), "Building it:", "the build's failure was dropped")
	require.Contains(t, err.Error(), "Cannot connect to the Docker daemon")
}

func TestProxyImage_AirGappedWithTheImagePresentReachesForNothing(t *testing.T) {
	airgap.Reset()
	t.Cleanup(airgap.Reset)
	airgap.Seal("this test is measuring the refusal")

	d := newFakeDaemon()
	d.put(proxyimage.Tag(), declaring(proxyimage.SourcesDigest()))
	d.publishes(proxyimage.SourcesDigest())
	d.compiles()

	job, _ := newJob(d, nil)
	require.NoError(t, job.obtain(context.Background()))
	require.Empty(t, airgap.Refusals(), "a present image made this reach for a registry anyway")
	pulls, builds := d.counts()
	require.Zero(t, pulls+builds)
}

func TestProxyImage_AirGappedWithTheImageAbsentRefusesBothSitesByName(t *testing.T) {
	airgap.Reset()
	t.Cleanup(airgap.Reset)
	airgap.Seal("this test is measuring the refusal")

	d := newFakeDaemon()
	d.publishes(proxyimage.SourcesDigest())
	d.compiles()

	job, _ := newJob(d, nil)
	err := job.obtain(context.Background())
	requireCode(t, err, aferrors.AFRUN048)
	require.ErrorIs(t, err, airgap.ErrSealed)
	require.Contains(t, err.Error(), "ghcr.io", "the refusal did not name the registry it would have reached")
	require.Contains(t, err.Error(), "Docker Hub", "the refusal did not name where the build would have reached")

	// Two refusals at two sites, in the order they were tried. A pull and a
	// build reach different places, and an operator reading the ledger has
	// to be able to tell which one this machine asked for.
	refused := airgap.Refusals()
	require.Len(t, refused, 2)
	require.Equal(t, airgap.SiteImagePull, refused[0].Site)
	require.Equal(t, "ghcr.io:443", refused[0].Address)
	require.Equal(t, airgap.SiteImageBuild, refused[1].Site)

	pulls, builds := d.counts()
	require.Zero(t, pulls, "the daemon was asked to pull on a sealed machine")
	require.Zero(t, builds, "the daemon was asked to build on a sealed machine")
}

func TestProxyImage_AFetchThatHangsIsGivenUpOnAndCompiled(t *testing.T) {
	d := newFakeDaemon()
	d.pull = func(ctx context.Context, _ string) (io.ReadCloser, error) {
		<-ctx.Done() // a registry that accepted the connection and never answered
		return nil, ctx.Err()
	}
	d.compiles()

	job, _ := newJob(d, map[string]string{"AF_PROXY_IMAGE_TIMEOUT": "200ms"})
	require.NoError(t, obtainWithin(t, job, 5*time.Second))
	pulls, builds := d.counts()
	require.Equal(t, 1, pulls)
	require.Equal(t, 1, builds, "a fetch that timed out did not fall back to compiling")
}

func TestProxyImage_AHungFetchIsReportedAsATimeoutRatherThanANetworkFault(t *testing.T) {
	// Named, so there is no build to fall back on and the fetch's own error is
	// the one returned.
	d := newFakeDaemon()
	d.pull = func(ctx context.Context, _ string) (io.ReadCloser, error) {
		return stalling(ctx, `{"status":"Pulling from mirror/af-proxy","id":"x"}`), nil
	}
	job, _ := newJob(d, map[string]string{
		"AF_PROXY_IMAGE_TIMEOUT": "200ms",
		"AF_PROXY_IMAGE":         "registry.internal:5000/af-proxy:mirror",
	})
	err := obtainWithin(t, job, 5*time.Second)
	requireCode(t, err, aferrors.AFRUN048)
	require.Contains(t, err.Error(), "did not finish within 200ms")
	require.Contains(t, err.Error(), "Pulling from mirror/af-proxy",
		"a timeout has to say what the daemon was last doing")
}

func TestProxyImage_ABaseImagePullThatStallsInsideTheBuildIsNamedWhenItIsGivenUpOn(t *testing.T) {
	// The measured 25 minutes. The build's one network operation is fetching
	// its FROM image, and it reports that as status lines, not as stream.
	d := newFakeDaemon()
	d.build = func(ctx context.Context, _ dockerbuild.ImageBuildOptions) (io.ReadCloser, error) {
		return stalling(ctx,
			`{"status":"Pulling from library/golang","id":"1.25-alpine"}`,
			`{"status":"Pulling fs layer","id":"a1"}`,
		), nil
	}

	job, rec := newJob(d, map[string]string{"AF_PROXY_IMAGE_TIMEOUT": "200ms"})
	err := obtainWithin(t, job, 5*time.Second)
	requireCode(t, err, aferrors.AFRUN048)
	require.Contains(t, err.Error(), "compiling the egress proxy did not finish within 200ms")
	require.Contains(t, err.Error(), "base image: Pulling fs layer",
		"the error has to say the build was stuck fetching its base image, not compiling")
	require.Contains(t, rec.text(), "base image: Pulling from library/golang",
		"the base image pull was not shown as it happened")
}

func TestProxyImage_AStillWorkingLineNamesWhatTheDaemonLastSaid(t *testing.T) {
	fake := clock.NewFake(time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC))
	release := make(chan struct{})
	d := newFakeDaemon()
	d.build = func(ctx context.Context, _ dockerbuild.ImageBuildOptions) (io.ReadCloser, error) {
		pr, pw := io.Pipe()
		go func() {
			_, _ = io.WriteString(pw, `{"status":"Pulling fs layer","id":"a1"}`+"\n")
			select {
			case <-release:
				_ = pw.Close()
			case <-ctx.Done():
				_ = pw.CloseWithError(ctx.Err())
			}
		}()
		return pr, nil
	}
	job, rec := newJob(d, nil)
	job.clock = fake

	done := make(chan error, 1)
	go func() { done <- job.obtain(context.Background()) }()

	rec.waitFor(t, "base image: Pulling fs layer")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, fake.BlockUntil(ctx, 1), "the heartbeat never started")
	fake.Advance(proxyProgressEvery)
	rec.waitFor(t, "still compiling the egress proxy, 15s elapsed of 10m0s, "+
		"the last thing the daemon reported was: base image: Pulling fs layer")

	close(release)
	require.NoError(t, <-done)
	require.Zero(t, fake.WaiterCount(), "the heartbeat's ticker outlived the build")
}

func TestProxyImage_ADaemonThatCannotSayIsNotAnsweredWithABuild(t *testing.T) {
	// The measuring machine: load average 30, docker ps silent for three
	// minutes. An inspect that fails with anything but not found is "I could
	// not look", and the old reading took it as absence and started a build.
	d := newFakeDaemon()
	d.inspectErr[proxyimage.Tag()] = errors.New("context deadline exceeded while awaiting headers")
	d.publishes(proxyimage.SourcesDigest())
	d.compiles()

	job, _ := newJob(d, nil)
	err := job.obtain(context.Background())
	requireCode(t, err, aferrors.AFRUN048)
	require.Contains(t, err.Error(), "could not say whether")
	pulls, builds := d.counts()
	require.Zero(t, pulls+builds, "a daemon that could not answer was sent a pull or a build")
}

func TestProxyImage_AFetchedImageBuiltFromOtherSourceIsRefusedAndCompiled(t *testing.T) {
	// The invariant. The registry holds something under this digest's tag
	// whose bytes say they came from different source: moved by hand, or
	// built wrong. It must not run, and the engine compiles what it holds.
	d := newFakeDaemon()
	d.publishes("0000000000000000")
	d.compiles()

	job, _ := newJob(d, nil)
	require.NoError(t, job.obtain(context.Background()))
	pulls, builds := d.counts()
	require.Equal(t, 1, pulls)
	require.Equal(t, 1, builds, "an image declaring other source was accepted instead of compiled")
	img, ok := d.has(proxyimage.Tag())
	require.True(t, ok)
	require.Equal(t, proxyimage.SourcesDigest(), img.Config.Labels[proxyimage.SourcesLabel],
		"the local name points at the mismatched image rather than the compiled one")
}

func TestProxyImage_AFetchedImageThatDeclaresNothingIsRefused(t *testing.T) {
	d := newFakeDaemon()
	d.publishes("")
	job, _ := newJob(d, map[string]string{"AF_PROXY_IMAGE": "registry.internal/af-proxy:anything"})
	err := job.obtain(context.Background())
	requireCode(t, err, aferrors.AFRUN048)
	require.Contains(t, err.Error(), "is not this sidecar")
	require.Contains(t, err.Error(), "built from source nothing")
	_, tagged := d.has(proxyimage.Tag())
	require.False(t, tagged, "an unverified image was given the sidecar's name")
}

func TestProxyImage_ALocalImageThatDisagreesWithItsNameIsRefused(t *testing.T) {
	d := newFakeDaemon()
	d.put(proxyimage.Tag(), declaring("ffffffffffffffff"))
	job, _ := newJob(d, nil)
	err := job.obtain(context.Background())
	requireCode(t, err, aferrors.AFRUN048)
	require.Contains(t, err.Error(), "nothing legitimate produces that")
}

func TestProxyImage_AnOperatorsNamedImageIsFetchedAndNeverSubstituted(t *testing.T) {
	// AF_PROXY_IMAGE says where this machine gets the sidecar. Compiling
	// instead would reach Docker Hub on a machine configured not to.
	d := newFakeDaemon()
	d.compiles()
	const mirror = "registry.internal:5000/antifailure/af-proxy:mirror"
	job, _ := newJob(d, map[string]string{"AF_PROXY_IMAGE": mirror})
	err := job.obtain(context.Background())
	requireCode(t, err, aferrors.AFRUN048)
	require.Contains(t, err.Error(), mirror)
	pulls, builds := d.counts()
	require.Equal(t, 1, pulls)
	require.Zero(t, builds, "a named image that could not be fetched was replaced by a compile")
}

func TestProxyImage_AnOperatorsNamedImageIsUsedWhenItIsThisSidecar(t *testing.T) {
	d := newFakeDaemon()
	d.publishes(proxyimage.SourcesDigest())
	const mirror = "registry.internal:5000/antifailure/af-proxy:mirror"
	job, _ := newJob(d, map[string]string{"AF_PROXY_IMAGE": mirror})
	require.NoError(t, job.obtain(context.Background()))
	require.Equal(t, []string{mirror}, d.pulled)
	_, ok := d.has(proxyimage.Tag())
	require.True(t, ok)
}

func TestProxyImage_AFailureInsideThePullStreamIsAFailure(t *testing.T) {
	// ImagePull returns a reader and a nil error for a pull that fails part
	// way through; the failure is a line in the stream.
	d := newFakeDaemon()
	d.pull = func(context.Context, string) (io.ReadCloser, error) {
		return stream(
			`{"status":"Pulling fs layer","id":"1"}`,
			`{"errorDetail":{"message":"unexpected EOF"},"error":"unexpected EOF"}`,
		), nil
	}
	job, _ := newJob(d, map[string]string{"AF_PROXY_IMAGE": "registry.internal/af-proxy:x"})
	err := job.obtain(context.Background())
	requireCode(t, err, aferrors.AFRUN048)
	require.Contains(t, err.Error(), "unexpected EOF")
}

func TestProxyImage_AMalformedTimeoutIsRefusedRatherThanIgnored(t *testing.T) {
	d := newFakeDaemon()
	d.publishes(proxyimage.SourcesDigest())
	job, _ := newJob(d, map[string]string{"AF_PROXY_IMAGE_TIMEOUT": "twenty minutes"})
	err := job.obtain(context.Background())
	requireCode(t, err, aferrors.AFRUN048)
	require.Contains(t, err.Error(), "twenty minutes")
	pulls, _ := d.counts()
	require.Zero(t, pulls)
}

func TestProxyImage_TheVariablesAreReadFromTheProcessWhenNoReaderIsGiven(t *testing.T) {
	// Options.Getenv is nil on the production path. A reader that took nil
	// as "nothing is set" would make both variables work here and do nothing
	// for a customer.
	t.Setenv("AF_PROXY_IMAGE", "registry.internal/af-proxy:from-the-process")
	d := newFakeDaemon()
	d.publishes(proxyimage.SourcesDigest())
	job, _ := newJob(d, nil)
	job.getenv = nil
	require.NoError(t, job.obtain(context.Background()))
	require.Equal(t, []string{"registry.internal/af-proxy:from-the-process"}, d.pulled)
}
