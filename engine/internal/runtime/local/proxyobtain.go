package local

// Obtaining the egress sidecar image, which is the first thing a new
// customer's first af up does.
//
// THE FAILURE THIS FILE EXISTS FOR. docs/plan/four-stacks/README.md measured
// `af up` against three published stacks on 2026-09-08 and recorded the same
// line three times: killed at 25 minutes 32 seconds on "building the egress
// proxy". Zero of four stacks came up and zero of fifty services reached
// ready.
//
// WHAT THOSE MINUTES WERE. Not a module fetch: the packaged source is standard
// library only and a test refuses anything else, so the compile downloads
// nothing. The one network operation inside that build is the pull of its
// golang base image, on a network the same report says was refusing TLS
// handshakes. So it was a stalled base image pull, and it was invisible,
// because the build's stream was drained into two variables and nothing was
// shown until it ended. A stalled pull and a slow compile were the same
// silence, and the only thing left to do was press control C.
//
// So: a release publishes this image and it is fetched before anything is
// compiled, both paths are bounded, and the stream is reported as it arrives,
// including the base image pull the build does on its own.
//
// AND ONE THAT WOULD HAVE SURVIVED THE OTHER THREE. The previous version asked
// the daemon for the image with `if _, err := ImageInspect(...); err == nil`,
// which reads EVERY error as absence. The machine that produced the
// measurement was at load average 30 with `docker ps` not answering inside 180
// seconds, so the inspect that decides whether to build had every reason to
// fail with something that is not a not found, and a daemon that was too busy
// to answer would send this straight into a build of an image it already had.
// dockerutil.ImagePresent exists for exactly that two valued read and is used
// here, so "I could not look" is now its own answer rather than a no.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	dockerbuild "github.com/docker/docker/api/types/build"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/proxyimage"
	"github.com/antifailure/antifailure/engine/internal/redact"
	"github.com/antifailure/antifailure/engine/pkg/airgap"
)

// The two budgets, and the variable that moves both.
//
// A pull of one small image is seconds on any working link, so two minutes
// already means something is wrong rather than slow. A build compiles a static
// Go binary inside a container and is a minute on an idle machine, so ten
// minutes is generous and is still less than half of what the measured run
// spent before somebody killed it.
//
// AF_PROXY_IMAGE_TIMEOUT replaces both, because a machine slow enough to need
// more time for one is slow enough to need more for the other, and one knob
// somebody can find beats two nobody documents.
const (
	defaultProxyPullTimeout  = 2 * time.Minute
	defaultProxyBuildTimeout = 10 * time.Minute
	// proxyProgressEvery is how often a step that is still working says so. A
	// silent minute and a hang look identical, and this is the whole of the
	// difference between them.
	proxyProgressEvery = 15 * time.Second
)

// proxyImageDaemon is the slice of the container daemon this needs.
//
// An interface rather than *client.Client, and that is the only reason the
// ordering table in proxyobtain_test.go can exist. The orderings that matter
// here are a registry answering, a registry refusing, a daemon that cannot say
// whether an image is present, and a pull that never returns, and not one of
// them can be produced on demand against a real daemon. The real client
// satisfies this, so nothing about the production path changes.
type proxyImageDaemon interface {
	ImageInspect(ctx context.Context, ref string, opts ...client.ImageInspectOption) (image.InspectResponse, error)
	ImagePull(ctx context.Context, ref string, opts image.PullOptions) (io.ReadCloser, error)
	ImageTag(ctx context.Context, source, target string) error
	ImageBuild(ctx context.Context, buildContext io.Reader, opts dockerbuild.ImageBuildOptions) (dockerbuild.ImageBuildResponse, error)
}

// proxyImageJob is one attempt to put the sidecar image on this daemon.
type proxyImageJob struct {
	daemon   proxyImageDaemon
	clock    clock.Clock
	redactor *redact.Redactor
	// labels are stamped on an image this builds, so `af down` and the reaper
	// can see it the way they see everything else this runtime creates.
	labels map[string]string
	getenv func(string) string

	// mu guards progress. The heartbeat below runs in its own goroutine, so
	// two lines can be produced at once, and a progress sink that is a terminal
	// renderer is not required to be safe for that.
	mu       sync.Mutex
	progress func(string)
	// last is the most recent thing the daemon's stream said. The heartbeat
	// repeats it and a timeout names it, because "still compiling, 4m elapsed"
	// is true of a stalled base image pull and of a slow compile alike, and
	// "last: Pulling fs layer" is the sentence that tells them apart.
	last string
}

// say emits one progress line.
func (j *proxyImageJob) say(line string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.progress != nil {
		j.progress(line)
	}
}

// heard records a line from the daemon's stream and reports it.
func (j *proxyImageJob) heard(line string) {
	j.mu.Lock()
	j.last = line
	j.mu.Unlock()
	j.say("  " + line)
}

// lastHeard is the most recent line from the daemon's stream, or a sentence
// saying there was none, which is itself the diagnosis for a daemon that
// accepted the request and then said nothing at all.
func (j *proxyImageJob) lastHeard() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.last == "" {
		return "the daemon had reported nothing at all"
	}
	return "the last thing the daemon reported was: " + j.last
}

// beat reports that a step is still working, every proxyProgressEvery, until
// the returned function is called.
//
// The elapsed time is in the line on purpose. "still building" repeated eight
// times says the process is alive; "still building, 2m0s elapsed" against a
// ten minute budget says how much patience is left, which is the question
// somebody with a cursor over control C is actually asking.
func (j *proxyImageJob) beat(ctx context.Context, what string, budget time.Duration) func() {
	started := j.clock.Now()
	ticker := j.clock.NewTicker(proxyProgressEvery)
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C():
				j.say(fmt.Sprintf("still %s, %s elapsed of %s, %s",
					what, j.clock.Since(started).Round(time.Second), budget, j.lastHeard()))
			}
		}
	}()
	return func() {
		ticker.Stop()
		close(done)
		<-stopped
	}
}

// env reads one variable, from the process when the caller named no reader.
//
// The fallback is the point. Options.Getenv is nil on the ordinary production
// path, and a reader that treated nil as "nothing is set" would make both
// variables below work in tests and do nothing for a customer, which is the
// quietest possible way to ship a knob.
func (j *proxyImageJob) env(name string) string {
	get := j.getenv
	if get == nil {
		get = os.Getenv
	}
	return strings.TrimSpace(get(name))
}

// timeout resolves the budget for one attempt.
func (j *proxyImageJob) timeout(fallback time.Duration) (time.Duration, error) {
	raw := j.env("AF_PROXY_IMAGE_TIMEOUT")
	if raw == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		// Refused rather than ignored. A typo silently falling back to the
		// default is how somebody concludes the variable does nothing, and the
		// person setting it is by definition somebody a default already failed.
		return 0, aferrors.Coded(aferrors.AFRUN048, "detail",
			"AF_PROXY_IMAGE_TIMEOUT is set to "+raw+", which is not a positive duration. "+
				"Write it as Go writes one, such as 20m")
	}
	return d, nil
}

// namedRef is the sidecar image this engine will look for, and whether an
// operator named it.
//
// AF_PROXY_IMAGE is the spelling the Kubernetes runtime has always used for
// this, documented in guides/kubernetes-runtime, and it means the same thing
// here: the image lives in a registry you control. It is honoured on this path
// too because an air gapped installation that mirrored the published image into
// its own registry had no way to say so, and the only thing left was loading a
// tarball under a content addressed tag by hand.
func (j *proxyImageJob) namedRef() (ref string, named bool) {
	if v := j.env("AF_PROXY_IMAGE"); v != "" {
		return v, true
	}
	return proxyimage.PublishedRef(), false
}

// obtain puts the sidecar image on this daemon, however it can.
//
// Present, then pulled, then built. The order is the whole point: the ordinary
// first run of a released binary fetches one small image, a contributor on a
// commit no release covers compiles it, and an air gapped installation is
// refused at both sites by name rather than sitting on a build that cannot
// finish.
func (j *proxyImageJob) obtain(ctx context.Context) error {
	local := proxyimage.Tag()
	switch have, err := j.alreadyHave(ctx, local); {
	case err != nil:
		return err
	case have:
		return nil
	}

	ref, named := j.namedRef()
	pullErr := j.pull(ctx, ref, local)
	if pullErr == nil {
		return nil
	}
	if named {
		// No fallback to the build. Naming an image is somebody saying where
		// this machine gets it, and compiling instead would reach Docker Hub
		// for a base image on a machine whose whole configuration says not to.
		return pullErr
	}

	buildErr := j.build(ctx, local)
	if buildErr == nil {
		return nil
	}
	// Both halves in one sentence, and the cause chain kept. An air gapped run
	// reaches here with two refusals and the reader needs both: one says the
	// registry was not reachable, the other says the base image was not
	// either, and each alone reads as the wrong problem.
	return aferrors.Wrap(buildErr, aferrors.AFRUN048, "detail",
		"the sidecar image "+local+" is not on this machine. Fetching it: "+
			messageOf(pullErr)+". Building it: "+messageOf(buildErr))
}

// alreadyHave reports whether this daemon holds an image that is this
// sidecar's.
//
// THE LABEL IS NOT CHECKED WHEN IT IS ABSENT, and that is deliberate. The tag
// is content addressed, so an image under it was either built from these
// sources or pulled and verified below before being tagged. An engine released
// before the label existed built images under the same tag from the same
// source, and refusing those would recompile the sidecar on every machine that
// has ever run af, for no gain. A label that is present and DISAGREES is a
// different matter: nothing legitimate produces that, so it is refused.
func (j *proxyImageJob) alreadyHave(ctx context.Context, local string) (bool, error) {
	insp, err := j.daemon.ImageInspect(ctx, local)
	if err != nil {
		if isNotFound(err) {
			return false, nil
		}
		return false, aferrors.Wrap(err, aferrors.AFRUN048, "detail",
			"the container daemon could not say whether "+local+" is present: "+err.Error()+
				". It is answering slowly or not at all, and building an image it may already "+
				"hold is a quarter of an hour spent on a question nobody asked")
	}
	if got := sourcesLabelOf(insp); got != "" && got != proxyimage.SourcesDigest() {
		return false, aferrors.Coded(aferrors.AFRUN048, "detail",
			"the image "+local+" on this daemon says it was built from sidecar source "+got+
				" and this engine carries "+proxyimage.SourcesDigest()+
				". The name is content addressed, so nothing legitimate produces that. Remove it "+
				"with 'docker image rm "+local+"' and run this again")
	}
	return true, nil
}

// pull fetches the sidecar image and verifies it is the one this engine wants.
func (j *proxyImageJob) pull(ctx context.Context, ref, local string) error {
	budget, err := j.timeout(defaultProxyPullTimeout)
	if err != nil {
		return err
	}
	// Checked before the daemon is asked, because a pull happens inside the
	// daemon over a socket the guard never sees. The site is the container
	// image pull, which is its own site with its own row in the air gap table
	// and its own entry in the ledger: a refused pull and a refused build are
	// different facts about what this machine reached for.
	if err := airgap.CheckImage(airgap.SiteImagePull, ref); err != nil {
		return aferrors.Wrap(err, aferrors.AFRUN048, "detail",
			"fetching the egress sidecar image "+ref+" would reach "+
				airgap.RegistryHost(ref)+", and this installation is sealed")
	}

	j.mu.Lock()
	j.last = ""
	j.mu.Unlock()
	j.say("fetching the egress proxy " + ref + " (once per version)")
	stop := j.beat(ctx, "fetching the egress proxy", budget)
	defer stop()

	pullCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	rc, err := j.daemon.ImagePull(pullCtx, ref, image.PullOptions{})
	if err != nil {
		return j.streamFailure(ctx, pullCtx, budget, "fetching "+ref, err)
	}
	streamErr := j.readPullStream(rc)
	if streamErr != nil {
		return j.streamFailure(ctx, pullCtx, budget, "fetching "+ref, streamErr)
	}

	// The authoritative check, and the reason a stale published image cannot be
	// run: the bytes have to declare the sources this binary carries. A
	// registry tag is something a publisher can move and an operator can
	// rewrite, so the tag is how the image is FOUND and the label is how it is
	// BELIEVED.
	insp, err := j.daemon.ImageInspect(ctx, ref)
	if err != nil {
		return aferrors.Wrap(err, aferrors.AFRUN048, "detail",
			ref+" is not present after fetching it: "+err.Error())
	}
	if got := sourcesLabelOf(insp); got != proxyimage.SourcesDigest() {
		held := got
		if held == "" {
			held = "nothing"
		}
		return aferrors.Coded(aferrors.AFRUN048, "detail",
			ref+" is not this sidecar. It says it was built from source "+held+
				" and this engine carries "+proxyimage.SourcesDigest()+
				", so running it would put a proxy in the environment that does not "+
				"match the policy this command explains")
	}
	if ref != local {
		if err := j.daemon.ImageTag(ctx, ref, local); err != nil {
			return aferrors.Wrap(err, aferrors.AFRUN048, "detail",
				"naming "+ref+" as "+local+": "+err.Error())
		}
	}
	j.say("fetched the egress proxy " + proxyimage.SourcesDigest())
	return nil
}

// build compiles the sidecar from the source this binary carries.
//
// KEPT, AND NOT A FALLBACK NOBODY RUNS. A contributor's commit changes a file
// in the packaged set, which changes the digest, which means no release ever
// published that image. So this is the path every development build takes and
// the published image is the path a customer takes.
func (j *proxyImageJob) build(ctx context.Context, local string) error {
	budget, err := j.timeout(defaultProxyBuildTimeout)
	if err != nil {
		return err
	}
	if err := airgap.Refuse(airgap.SiteImageBuild,
		"building the sidecar image "+local+", whose base image comes from Docker Hub"); err != nil {
		return aferrors.Wrap(err, aferrors.AFRUN048, "detail", err.Error())
	}
	j.mu.Lock()
	j.last = ""
	j.mu.Unlock()
	j.say("compiling the egress proxy " + proxyimage.SourcesDigest() +
		" (once per version, no published image covers this build)")
	stop := j.beat(ctx, "compiling the egress proxy", budget)
	defer stop()

	buildCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	labels := map[string]string{}
	for k, v := range j.labels {
		labels[k] = v
	}
	// The same label the release publishes, so an image this engine built and
	// an image a release published are believed by the same rule, and so an
	// operator can push what af built into their own registry and have it
	// accepted.
	labels[proxyimage.SourcesLabel] = proxyimage.SourcesDigest()

	resp, err := j.daemon.ImageBuild(buildCtx, proxyimage.BuildContext(), dockerbuild.ImageBuildOptions{
		Tags:   []string{local},
		Remove: true,
		Labels: labels,
	})
	if err != nil {
		return j.streamFailure(ctx, buildCtx, budget, "compiling the egress proxy", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if err := j.readBuildStream(resp.Body); err != nil {
		return j.streamFailure(ctx, buildCtx, budget, "compiling the egress proxy", err)
	}
	return nil
}

// readPullStream reads a pull's progress, reporting what it is doing and
// refusing on a failure the stream carries.
//
// THE STREAM CAN CARRY THE FAILURE. ImagePull returns a reader and a nil error
// for a pull that goes wrong part way through, exactly as ImageBuild does, and
// a caller that drains it and asks no questions reports a successful fetch of
// an image that is not there. That is why this decodes rather than discards.
func (j *proxyImageJob) readPullStream(rc io.ReadCloser) error {
	defer func() { _ = rc.Close() }()
	dec := json.NewDecoder(rc)
	said := map[string]bool{}
	for {
		var msg struct {
			Status      string `json:"status"`
			Error       string `json:"error"`
			ErrorDetail struct {
				Message string `json:"message"`
			} `json:"errorDetail"`
		}
		if err := dec.Decode(&msg); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			// A truncated stream is not a finished pull. The inspect after this
			// would catch it anyway, and saying which of the two happened is
			// the difference between a network to look at and an image to
			// remove.
			return fmt.Errorf("reading the pull's progress: %w", err)
		}
		if detail := msg.ErrorDetail.Message; detail != "" {
			return errors.New(detail)
		}
		if msg.Error != "" {
			return errors.New(msg.Error)
		}
		// One line per distinct status rather than one per layer per status,
		// which for a two layer image is four lines instead of forty.
		if s := strings.TrimSpace(msg.Status); s != "" && !said[s] {
			said[s] = true
			j.heard(s)
		}
	}
}

// readBuildStream reads a build's output as it arrives, reporting each step
// and the base image pull the build does on its own, and refusing on a
// failure the stream carries.
//
// THE BASE IMAGE PULL ARRIVES AS STATUS, NOT AS STREAM. A build that has to
// fetch its FROM image writes `{"status":"Pulling fs layer","id":...}` lines
// before its first step, and the previous reader kept only `stream`, so the
// one network operation in the whole build was the one part of it nobody
// could see. That is the exact silence the measured run sat in.
func (j *proxyImageJob) readBuildStream(body io.Reader) error {
	dec := json.NewDecoder(body)
	var buildErr, tail string
	said := map[string]bool{}
	for {
		var msg struct {
			Stream string `json:"stream"`
			Status string `json:"status"`
			Error  string `json:"error"`
		}
		if err := dec.Decode(&msg); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			// A stream cut off part way is not a finished build. The version
			// this replaced broke out of the loop on ANY decode error and
			// returned nil, so a build killed by its own deadline would have
			// been reported as a success and the container create below would
			// have failed on an image that was never written.
			return fmt.Errorf("reading the build's progress: %w", err)
		}
		if s := strings.TrimSpace(msg.Stream); s != "" {
			tail = s
			// The Dockerfile's own steps, which is the part of a build's
			// output that says where it has got to.
			if strings.HasPrefix(s, "Step ") {
				j.heard(s)
			}
		}
		// One line per distinct status, for the same reason the pull reader
		// does it: a base image is a handful of layers, and one line per layer
		// per percentage would bury the steps.
		if s := strings.TrimSpace(msg.Status); s != "" && !said[s] {
			said[s] = true
			j.heard("base image: " + s)
		}
		if msg.Error != "" {
			buildErr = msg.Error
		}
	}
	if buildErr != "" {
		return errors.New(j.redactor.String(buildErr + " " + tail))
	}
	return nil
}

// streamFailure turns a failed attempt into the coded error, saying whether it
// ran out of time rather than leaving a deadline exceeded to be read as a
// network fault.
func (j *proxyImageJob) streamFailure(
	ctx, attempt context.Context, budget time.Duration, what string, cause error,
) error {
	// The caller's own cancellation is not this command's failure to report.
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if attempt.Err() != nil {
		return aferrors.Wrap(cause, aferrors.AFRUN048, "detail",
			what+" did not finish within "+budget.String()+" and was given up on rather "+
				"than waited on, and "+j.lastHeard())
	}
	return aferrors.Wrap(cause, aferrors.AFRUN048, "detail", what+": "+j.redactor.String(cause.Error()))
}

// sourcesLabelOf reads the sidecar source digest an image declares.
func sourcesLabelOf(insp image.InspectResponse) string {
	if insp.Config == nil {
		return ""
	}
	return insp.Config.Labels[proxyimage.SourcesLabel]
}

// isNotFound distinguishes absence from every other answer, which is the whole
// of what dockerutil.ImagePresent exists for. It is used through the same
// helper here so the two cannot diverge.
func isNotFound(err error) bool {
	present, perr := dockerutil.ImagePresent(context.Background(), notFoundProbe{err}, "")
	return !present && perr == nil
}

// notFoundProbe hands one error back to ImagePresent, so the classification of
// a daemon error lives in exactly one place.
type notFoundProbe struct{ err error }

func (p notFoundProbe) ImageInspect(
	context.Context, string, ...client.ImageInspectOption,
) (image.InspectResponse, error) {
	return image.InspectResponse{}, p.err
}

// messageOf is what one failed attempt has to say, for composing two of them
// into one readable line.
//
// The detail field rather than the rendered message, because every error here
// carries the same catalog sentence and a line reading "could not be obtained:
// could not be obtained: ..." twice is how a composed message stops being read.
func messageOf(err error) string {
	var coded *aferrors.Error
	if errors.As(err, &coded) {
		if detail := coded.Fields["detail"]; detail != "" {
			return detail
		}
		return coded.Message()
	}
	return err.Error()
}
