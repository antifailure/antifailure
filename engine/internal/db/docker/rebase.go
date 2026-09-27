package docker

// OPENING ONE GOLDEN'S DATA DIRECTORY WITH A DIFFERENT DATABASE BUILD.
//
// WHY THIS EXISTS. A golden here is a committed image: the data directory and
// the Postgres that wrote it, in one artifact, and a branch is a container
// started from that artifact. That is exactly right for comparing two builds
// of an APPLICATION over one database, which is what everything above this
// file was built for, and it makes one experiment unreachable. Somebody
// hardening their own storage engine wants the same workload and the same rows
// on two builds of THEIR DATABASE, and there is no way to express it: both
// sides of every comparison ran the golden's own image, so the database was
// the one thing that could never vary.
//
// WHAT VARYING IT MEANS, said plainly because it decides the shape. There is
// one data directory and there has to be: two goldens would mean the two sides
// answered queries over different rows, and then every difference in the
// report is a difference in the data. So one build writes the directory and
// the other OPENS it. That asymmetry is not a compromise, it is the
// experiment, and it is the one a storage engine developer cares about most:
// if the other build cannot open it, that is the most useful thing this tool
// could ever say, and it is reported as its own finding rather than as a
// container that did not come up.
//
// HOW, AND WHY NOT THE OTHER TWO WAYS. The golden's data directory is copied
// onto the other image by the daemon, with a two line Dockerfile, and branches
// of that pairing start from the result. The alternatives were both worse.
// Streaming the directory out through the daemon's archive endpoint and back
// in sends every byte of a production sized golden over a socket twice, for a
// copy the daemon can do inside itself. Mounting the golden's filesystem into
// a container of the other image cannot be done at all: an image is not a
// mountable source, which is the whole reason the golden is an image.
//
// WHAT THE RESULT IS NOT. It is not a golden. It carries no provenance, no
// rules digest and no attestation, it lives in its own repository so that
// ListGoldens cannot see it and nothing can branch it by name, and
// DestroyGolden removes the ones derived from a version along with the version
// itself. A derived image that looked like a golden would be a golden nothing
// had verified, which is the failure the attestation exists to prevent.

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"

	"github.com/antifailure/antifailure/engine/internal/db/pgcopy"
	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/airgap"
)

// RebaseRepo is where a golden's data directory copied onto another image is
// kept.
//
// A repository of its own rather than another tag under ImageRepo, and that is
// load bearing rather than tidy. ListGoldens enumerates every tag under
// ImageRepo and turns each one into a version, so a derived image there would
// be listed as a golden version that no verifier ever attested to. It would be
// refused on provenance, which is the right answer arrived at by luck: the
// refusal counts would rise, `af golden list` would show it, and the reason it
// could not be branched would be a missing label rather than the truth, which
// is that it is not a golden at all.
const RebaseRepo = "antifailure/rebased"

// rebaseKind is the value of the kind label on a derived image, so the
// inventory can name it and DestroyGolden can find it.
const rebaseKind = "rebased"

// rebaseTag is the deterministic name for one golden opened by one BUILD.
//
// Keyed by a digest rather than by the text, because a tag may not contain a
// slash or a colon and an image reference routinely contains both.
// Deterministic so that a second branch of the same pairing finds the image the
// first one built: the copy is the expensive part, and a comparison brings a
// base environment up once per run.
//
// THE KEY IS THE IMAGE ID AND NOT THE REFERENCE, and the first version of this
// got that wrong in the way that matters most to the people this feature is
// for. A reference is a name somebody can repoint. Somebody hardening a storage
// engine rebuilds `mybuild:candidate` in place and runs the comparison again,
// which is the whole iteration loop: keyed on the name, the cache still finds
// the copy made from the PREVIOUS build, the preflight validates the new image
// because it resolves the name freshly, and the branch runs the old one. The
// report then attributes its numbers to a build that never ran, silently, on
// the one workflow this exists to serve. Keyed on the id, a rebuilt image is a
// different key and a fresh copy is made.
//
// The reference is still in the digest, so two different names for one id keep
// their own copies. That costs a duplicate image in a case nobody hits and it
// keeps the name a person typed recoverable from nothing but the tag.
func rebaseTag(version, image, imageID string) string {
	sum := sha256.Sum256([]byte(imageID + "\x00" + image))
	return RebaseRepo + ":" + version + "-on-" + hex.EncodeToString(sum[:8])
}

// rebase copies a golden's data directory onto the branch image and returns the
// tag branches of that pairing start from.
//
// The checks come before the copy, in the order that makes each one cheap. The
// image has to be present before anything can be built on it; an image that
// declares a volume over the data directory would swallow the copy and produce
// a branch holding no rows, which is the failure checkDataDirectory was written
// for and it applies here with more force than on the golden path, because
// here the rows already exist and would be silently dropped.
func (p *Provider) rebase(ctx context.Context, goldenTag, version string) (string, error) {
	if err := p.ensureImage(ctx, p.branchImage); err != nil {
		return "", err
	}
	if err := p.checkDataDirectory(ctx, p.branchImage); err != nil {
		return "", err
	}

	// Resolved AFTER ensureImage, so the id is the id of the image this run will
	// actually build on rather than of whatever was present before the pull.
	base, err := p.cli.ImageInspect(ctx, p.branchImage)
	if err != nil {
		return "", fmt.Errorf("db.docker: inspect the database build %s: %w", p.branchImage, err)
	}
	tag := rebaseTag(version, p.branchImage, base.ID)
	if _, err := p.cli.ImageInspect(ctx, tag); err == nil {
		return tag, nil
	}

	if err := airgap.Refuse(airgap.SiteImageBuild,
		"copying the golden "+version+"'s data directory onto "+p.branchImage); err != nil {
		return "", fmt.Errorf("db.docker: %w", err)
	}

	// Two instructions and no build context. COPY from an IMAGE rather than
	// from a build stage, which both builders have accepted since multi stage
	// builds existed, so this does not depend on the daemon having BuildKit.
	//
	// Ownership rides along with the copy, and the entrypoint is what makes
	// that safe: the published Postgres images start as root, chown the data
	// directory to their own postgres user, and only then drop to it. An image
	// that instead runs as a fixed user cannot do that, and the postmaster
	// then says so in its own words, which is reported as the finding below
	// rather than guessed at here.
	dockerfile := fmt.Sprintf("FROM %s\nCOPY --from=%s %s %s\n",
		p.branchImage, goldenTag, dataDir, dataDir)

	resp, err := p.cli.ImageBuild(ctx, tarOf("Dockerfile", dockerfile), client.ImageBuildOptions{
		Tags:   []string{tag},
		Remove: true,
		Labels: map[string]string{
			LabelManaged: dockerutil.ManagedValue,
			LabelKind:    rebaseKind,
			LabelGolden:  version,
			LabelCreated: p.clock.Now().UTC().Format(time.RFC3339),
		},
	})
	if err != nil {
		return "", fmt.Errorf("db.docker: copy the golden %s onto %s: %w",
			version, p.branchImage, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := readBuildStream(resp.Body); err != nil {
		return "", fmt.Errorf("db.docker: copy the golden %s onto %s: %w",
			version, p.branchImage, err)
	}
	// Inspected rather than trusted, for the reason ensureImage inspects after
	// a pull: a build stream can end without an error document and without
	// having written the tag, and the next call would then create a container
	// from an image that does not exist and report that instead.
	if _, err := p.cli.ImageInspect(ctx, tag); err != nil {
		return "", fmt.Errorf("db.docker: %s is not present after copying the golden %s onto %s: %w",
			tag, version, p.branchImage, err)
	}
	return tag, nil
}

// tarOf is a one file build context.
func tarOf(name, body string) io.Reader {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	// Writing to a buffer with a valid header cannot fail, and a truncated
	// archive would surface inside the daemon as an unexpected end of file, so
	// the errors are checked rather than dropped.
	if err := tw.WriteHeader(&tar.Header{
		Name: name, Mode: 0o644, Size: int64(len(body)),
		Format: tar.FormatPAX, Typeflag: tar.TypeReg,
	}); err != nil {
		panic("db.docker: " + err.Error())
	}
	if _, err := io.WriteString(tw, body); err != nil {
		panic("db.docker: " + err.Error())
	}
	if err := tw.Close(); err != nil {
		panic("db.docker: " + err.Error())
	}
	return bytes.NewReader(buf.Bytes())
}

// readBuildStream reports a failure the stream carries.
//
// ImageBuild returns a reader and a nil error for a build that goes wrong part
// way through, so the stream is the only place the failure appears. A decode
// error part way is a build that was cut off rather than one that finished:
// returning nil there is how a killed build reads as a success.
func readBuildStream(body io.Reader) error {
	dec := json.NewDecoder(body)
	var buildErr, tail string
	for {
		var msg struct {
			Stream string `json:"stream"`
			Error  string `json:"error"`
		}
		if err := dec.Decode(&msg); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return fmt.Errorf("reading the copy's progress: %w", err)
		}
		if s := strings.TrimSpace(msg.Stream); s != "" {
			tail = s
		}
		if msg.Error != "" {
			buildErr = msg.Error
		}
	}
	if buildErr != "" {
		if tail != "" {
			return fmt.Errorf("%s (last output: %s)", buildErr, tail)
		}
		return errors.New(buildErr)
	}
	return nil
}

// branchImageOrDeclared is the image whose contents a message about this branch
// should name.
//
// The rebased image is never named to a person. It is a local artifact with a
// digest in its tag, nobody chose it, and nothing they could do acts on it. The
// image they named is the one in the remedy.
func (p *Provider) branchImageOrDeclared() string {
	if p.branchImage != "" {
		return p.branchImage
	}
	return p.imageFor(p.version)
}

// branchReady waits for a branch, and on the rebased path turns a server that
// refused the data directory into the finding that says so.
//
// THREE OUTCOMES, NOT TWO, and keeping them apart is the point of this
// function. A branch that answers is ready. A branch whose server printed a
// FATAL or a PANIC and exited could not open the other build's data directory,
// and that is a finding about the two builds. A branch that stopped saying
// nothing recognisable, or that never answered in time, is an environment that
// did not come up, which says nothing about either build and must not borrow
// the finding's words. The ordinary path is untouched: with no branch image
// there is no other build in the picture and the wait is the one every other
// caller gets.
func (p *Provider) branchReady(
	ctx context.Context, conn secrets.Value, id, version string,
) error {
	if p.branchImage == "" {
		return p.waitReady(ctx, conn)
	}
	err := p.waitReadyOrGone(ctx, conn, id)
	if err == nil {
		return nil
	}
	// Asked for whichever way the wait ended, including a timeout: a server can
	// refuse a data directory, print why, and leave a process behind that never
	// exits. The log is the evidence either way, and its absence is what makes
	// this not the finding.
	// WithoutCancel, because the reason the wait ended may be that the context
	// did, and the server's own account of why it refused is the deliverable. A
	// log read on a cancelled context returns nothing, and nothing here is read
	// as "not this finding".
	logCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if said, ok := p.postmasterRefusal(logCtx, id); ok {
		return aferrors.Coded(aferrors.AFDB044,
			"image", p.branchImage, "version", version, "said", said)
	}
	if errors.Is(err, errPostmasterGone) {
		return aferrors.Wrap(err, aferrors.AFDB002, "host", "127.0.0.1")
	}
	return err
}

// CheckImage refuses an image that cannot be the database this manifest
// declares, before anything has been built on it.
//
// WHY A SEPARATE ENTRY POINT AND NOT A SECOND CHECK. versionMatches already
// refuses an image whose server disagrees with database.version, and it does it
// against server_version_num rather than against a tag, because a tag is a
// string somebody chose. Every word of that reasoning applies to an image named
// on a command line, and it applies BEFORE two environments are built rather
// than after: a comparison that spends ten minutes bringing two environments up
// and then refuses has wasted the ten minutes. So the same comparison runs here
// against a throwaway container, which is the only thing that can answer the
// question the reasoning insists on asking.
//
// It also runs the volume check, which costs one inspect and no container, and
// which catches the image that would silently swallow the data directory.
func (p *Provider) CheckImage(ctx context.Context, image string) error {
	if image == "" {
		return nil
	}
	if err := p.ensureImage(ctx, image); err != nil {
		return err
	}
	if err := p.checkDataDirectory(ctx, image); err != nil {
		return err
	}
	name := fmt.Sprintf("af-imagecheck-%d", p.clock.Now().UnixNano())
	// NO DECLARED FILESYSTEM, and that is a choice rather than an omission. This
	// container exists to be asked its server version and its extension list and
	// is then removed; a data directory volume would be created, filled from the
	// image and destroyed for no answer, and the refusal that a filesystem
	// smaller than the data directory produces belongs to the branch that will
	// run on it rather than to a question about the image.
	c, err := p.start(ctx, name, image,
		map[string]string{LabelKind: "candidate"}, p.preload, nil)
	if err != nil {
		return err
	}
	// Removed whatever happens, and labelled a candidate so that the sweep
	// which collects orphaned candidates collects one of these too if the
	// process is killed between the start and this defer.
	defer func() { _ = p.remove(context.WithoutCancel(ctx), c.id) }()
	conn := p.connString(c.port)
	if err := p.waitReady(ctx, conn); err != nil {
		return err
	}
	if err := majorMatches(pgcopy.ServerMajor(ctx, conn), p.version, image); err != nil {
		return err
	}
	// And the extensions the manifest declares, under the code and in the shape
	// the golden path already refuses them with. An extension is files on the
	// server's disk before it is anything in a database, so an image that does
	// not carry one can never run this manifest; catching it here costs the
	// statements that were going to run anyway on a container that is already up,
	// and it means the refusal arrives before two environments are built rather
	// than in the middle of the second one.
	return p.createExtensions(ctx, conn, image)
}

// errPostmasterGone is the readiness wait giving up because the container is no
// longer running, rather than because time ran out.
//
// The two are different facts and only one of them is worth five minutes. A
// container with a restart policy of "no" that has exited will never answer, so
// waiting for the full readiness timeout to say so turns the single most
// valuable finding this file can produce into a generic timeout.
var errPostmasterGone = errors.New("the database container stopped before it accepted a connection")

// waitReadyOrGone waits for the database, and gives up early if the container
// has exited.
//
// Only the rebased path uses it, and that is a deliberate limit rather than an
// oversight. Short circuiting an exited container is right everywhere, and
// changing the shared wait would change the error every other command produces
// for a container that died on start. This is the path where the distinction
// carries the meaning: on every other path an exited container is a bug to
// report, and here it is the answer to the question that was asked.
func (p *Provider) waitReadyOrGone(ctx context.Context, conn secrets.Value, id string) error {
	deadline := p.clock.Now().Add(readyTimeout)
	for {
		if err := pgcopy.Ping(ctx, conn); err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		running, err := p.running(ctx, id)
		if err == nil && !running {
			return errPostmasterGone
		}
		if !p.clock.Now().Before(deadline) {
			return aferrors.Coded(aferrors.AFDB002, "host", "127.0.0.1")
		}
		// The sleep's own error is the context ending, and it is returned rather
		// than discarded. Discarding it left the loop able to spin through every
		// remaining second of the five minute budget after a cancel, because the
		// check above it runs before the ping rather than after the wait: a
		// cancel landing during the sleep is not noticed until the next ping has
		// already been attempted. It reports the cancel as the cancel, which is
		// what branchReady's caller distinguishes from the finding.
		if err := p.clock.Sleep(ctx, time.Second); err != nil {
			return err
		}
	}
}

// running reports whether a container is still running. An inspect that fails
// is "could not tell", never "it stopped": a daemon that would not answer must
// not be read as evidence about the postmaster.
func (p *Provider) running(ctx context.Context, id string) (bool, error) {
	insp, err := p.cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return false, err
	}
	if insp.Container.State == nil {
		return false, errors.New("db.docker: the daemon described the container without a state")
	}
	return insp.Container.State.Running, nil
}

// postmasterRefusal is what the server itself said about the data directory.
//
// Read from the container's log rather than inferred, because the one thing
// worth reporting here is the build's own words. A caller that printed "this
// build could not open the other build's data directory" and nothing else
// would be asking a storage engine developer to reproduce it by hand to find
// out why.
//
// Only FATAL and PANIC lines. A data directory a server refuses produces one
// of those and exits; WARNING and LOG lines are what a server that opened it
// perfectly well also prints, and including them would let a container that
// died for an unrelated reason look like this finding.
func (p *Provider) postmasterRefusal(ctx context.Context, id string) (string, bool) {
	logs, err := p.cli.ContainerLogs(ctx, id, client.ContainerLogsOptions{
		ShowStdout: true, ShowStderr: true, Tail: "200",
	})
	if err != nil {
		return "", false
	}
	defer func() { _ = logs.Close() }()
	var buf bytes.Buffer
	// Postgres writes to stderr and the entrypoint to stdout, and both are
	// multiplexed into one stream. Demultiplexed into one buffer because the
	// interleaving is what makes the log readable.
	if _, err := stdcopy.StdCopy(&buf, &buf, io.LimitReader(logs, maxRefusalBytes)); err != nil {
		// A partial log is still evidence. The copy fails on a truncated frame
		// at the limit, which is exactly the case where what was read matters.
		if buf.Len() == 0 {
			return "", false
		}
	}
	return refusalLines(buf.String())
}

// maxRefusalBytes bounds how much of a log is read. A server that refuses says
// so in a handful of lines; a server that started and served for an hour has a
// log nobody wants in an error message.
const maxRefusalBytes = 64 << 10

// refusalLines picks the server's own refusals out of a log.
//
// A pure function so the classification can be tested against real postmaster
// output without a daemon, which matters because the interesting inputs are
// the ones that are hard to produce on demand: a data directory written by a
// build compiled with a different block size, or with a catalog version this
// server does not know.
//
// THE DETAIL AND THE HINT ARE PART OF THE REFUSAL, and leaving them out was the
// first version of this function. Postgres writes the verdict and the reason on
// separate lines at separate severities: "FATAL: database files are incompatible
// with server" is the same sentence for a catalog version, a block size, a WAL
// segment size and a toast chunk size, and the DETAIL beneath it is the only line
// that says which. Reporting the FATAL alone would name the finding and withhold
// the one fact a storage engine developer needs from it. So a DETAIL or a HINT
// following a refusal is taken with it, and one following anything else is not.
func refusalLines(log string) (string, bool) {
	var out []string
	seen := map[string]bool{}
	refusing := false
	for _, line := range strings.Split(log, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fatal := strings.Contains(line, "FATAL:") || strings.Contains(line, "PANIC:")
		follows := refusing &&
			(strings.Contains(line, "DETAIL:") || strings.Contains(line, "HINT:"))
		if !fatal && !follows {
			// A LOG or a WARNING between a refusal and its detail ends the
			// refusal, because the detail then belongs to something else.
			refusing = false
			continue
		}
		refusing = fatal || follows
		if seen[line] {
			continue
		}
		seen[line] = true
		out = append(out, line)
		// A server prints its refusal, its reason and its hint, and more than a
		// few lines in a message is a wall rather than an explanation.
		if len(out) == maxRefusalLines {
			break
		}
	}
	if len(out) == 0 {
		return "", false
	}
	return strings.Join(out, " / "), true
}

// maxRefusalLines is how much of the server's own account is carried into the
// message: enough for a refusal, its detail and its hint, twice over.
const maxRefusalLines = 6
