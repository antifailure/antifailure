package build

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
	"sort"
	"strings"
	"time"

	dockerbuild "github.com/docker/docker/api/types/build"
	"github.com/docker/docker/client"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/redact"
)

// generatedDockerfile is where a buildpack's Dockerfile is placed inside the
// context.
//
// Under a dot directory so it cannot collide with anything in the repository,
// and named per service so two services building from one context do not
// overwrite each other's.
const generatedDockerfile = ".antifailure/Dockerfile"

// ImageRepo is where built service images live. One repository with the tag
// carrying the service and the content digest means docker images already
// lists them readably, and docker image rm already knows how to remove one.
const ImageRepo = "antifailure/service"

// Request is one service's build.
type Request struct {
	// Service is the manifest service name.
	Service string
	// Context is the files the build sees.
	Context *Context
	// Dockerfile is generated content to inject. Empty means the build uses a
	// file already in the context.
	Dockerfile string
	// DockerfilePath is the path within the context to build from, used when
	// Dockerfile is empty.
	DockerfilePath string
	// Target is the stage to stop at, for a multi stage Dockerfile.
	Target string
	// Args are build arguments.
	//
	// They are not secrets and are not treated as such: a build argument ends
	// up in the image history, where anyone who can pull the image can read
	// it. A build that needs a credential gets it through a mount, not here.
	Args map[string]string
	// EnvID labels the image, so teardown and the leak detector can find it.
	EnvID string
	// Progress receives build output a line at a time, already redacted.
	Progress func(line string)
}

// Result is what a build produced.
type Result struct {
	// Service is the manifest service name.
	Service string
	// ImageRef is the tag the image can be run by.
	ImageRef string
	// Cached is true when the image already existed and nothing was built.
	Cached bool
	// Duration is how long it took, zero when cached.
	Duration time.Duration
	// Log is the build output, redacted, kept for the failure report.
	Log []string
}

// Builder builds service images.
//
// It is an interface so that the plan can be exercised without a daemon, and
// so that a future remote builder is a package rather than a fork.
type Builder interface {
	Build(ctx context.Context, req Request) (Result, error)
	Close() error
}

// DockerBuilder builds with the local Docker daemon.
type DockerBuilder struct {
	cli      *client.Client
	clock    clock.Clock
	redactor *redact.Redactor
	// noCache forces a rebuild even when the tag exists, for af up --rebuild.
	noCache bool
}

// DockerOptions configure the builder.
type DockerOptions struct {
	Clock    clock.Clock
	Redactor *redact.Redactor
	NoCache  bool
}

// NewDockerBuilder returns a builder talking to the local daemon.
func NewDockerBuilder(opts DockerOptions) (*DockerBuilder, error) {
	cli, err := dockerutil.Client()
	if err != nil {
		return nil, err
	}
	if opts.Clock == nil {
		opts.Clock = clock.New()
	}
	if opts.Redactor == nil {
		opts.Redactor = redact.New()
	}
	return &DockerBuilder{cli: cli, clock: opts.Clock, redactor: opts.Redactor, noCache: opts.NoCache}, nil
}

// Close releases the daemon connection.
func (b *DockerBuilder) Close() error { return b.cli.Close() }

// ImageRef returns the tag a request will produce.
//
// It is derived from everything that can change the image: the context digest,
// the Dockerfile, the target stage, and the build arguments. Two requests that
// agree on all of those produce the same image, so the second one is a lookup
// rather than a build. Leaving any of them out would mean serving a stale
// image after a change nobody could see.
func ImageRef(req Request) string {
	h := sha256.New()
	write := func(parts ...string) {
		for _, p := range parts {
			_, _ = io.WriteString(h, p)
			_, _ = h.Write([]byte{0})
		}
	}
	write("v1", req.Context.Digest, req.Dockerfile, req.DockerfilePath, req.Target)
	for _, k := range sortedKeys(req.Args) {
		write(k, req.Args[k])
	}
	return fmt.Sprintf("%s:%s-%s", ImageRepo, sanitizeTag(req.Service), hex.EncodeToString(h.Sum(nil))[:16])
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// sanitizeTag keeps a service name to what a Docker tag allows.
func sanitizeTag(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '.', r == '_':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + 32)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-._")
	if out == "" {
		return "service"
	}
	if len(out) > 100 {
		out = out[:100]
	}
	return out
}

// Build produces an image for one service.
func (b *DockerBuilder) Build(ctx context.Context, req Request) (Result, error) {
	if req.Context == nil {
		return Result{}, aferrors.Coded(aferrors.AFBLD010, "service", req.Service)
	}
	ref := ImageRef(req)
	res := Result{Service: req.Service, ImageRef: ref}

	if !b.noCache {
		if _, _, err := b.cli.ImageInspectWithRaw(ctx, ref); err == nil {
			res.Cached = true
			return res, nil
		}
	}

	dockerfilePath := req.DockerfilePath
	var extra map[string]string
	if req.Dockerfile != "" {
		dockerfilePath = generatedDockerfile + "." + sanitizeTag(req.Service)
		extra = map[string]string{dockerfilePath: req.Dockerfile}
	}
	if dockerfilePath == "" {
		dockerfilePath = "Dockerfile"
	}

	args := make(map[string]*string, len(req.Args))
	for k, v := range req.Args {
		args[k] = &v
	}

	started := b.clock.Now()
	resp, err := b.cli.ImageBuild(ctx, req.Context.tarWith(extra), dockerbuild.ImageBuildOptions{
		Tags:        []string{ref},
		Dockerfile:  dockerfilePath,
		Target:      req.Target,
		BuildArgs:   args,
		Remove:      true,
		ForceRemove: true,
		NoCache:     b.noCache,
		PullParent:  false,
		Labels:      dockerutil.Managed(dockerutil.KindService, req.EnvID, started),
	})
	if err != nil {
		return res, aferrors.Wrap(err, aferrors.AFBLD001,
			"service", req.Service, "duration", b.clock.Since(started).Round(time.Second).String())
	}
	defer func() { _ = resp.Body.Close() }()

	log, buildErr := b.stream(resp.Body, req.Progress)
	res.Log = log
	res.Duration = b.clock.Since(started)
	if buildErr != nil {
		return res, aferrors.Wrap(buildErr, aferrors.AFBLD001,
			"service", req.Service, "duration", res.Duration.Round(time.Second).String())
	}
	return res, nil
}

// buildMessage is the daemon's streaming output.
type buildMessage struct {
	Stream string `json:"stream"`
	Status string `json:"status"`
	Error  string `json:"error"`
	Detail *struct {
		Message string `json:"message"`
	} `json:"errorDetail"`
}

// maxLoggedLines bounds what is kept from a build.
//
// A failing build can produce tens of thousands of lines, and the useful ones
// are at the end. Keeping everything would put a webpack log into a support
// bundle; keeping the tail keeps the error and the steps that led to it.
const maxLoggedLines = 400

// stream reads the daemon's output, redacting as it goes.
//
// Redaction happens here rather than at the call sites because a build log is
// the single most likely place for a secret to appear: an npm token in a
// registry URL, a connection string echoed by a migration, a key printed by a
// misconfigured script. Redacting at the writer means a missed call site
// cannot leak.
func (b *DockerBuilder) stream(r io.Reader, progress func(string)) ([]string, error) {
	dec := json.NewDecoder(r)
	var lines []string
	var buildErr error
	for {
		var msg buildMessage
		if err := dec.Decode(&msg); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			// A truncated stream means the daemon went away mid build. The
			// lines gathered so far are still the best evidence available.
			return lines, err
		}
		text := msg.Stream
		if text == "" {
			text = msg.Status
		}
		for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			clean := b.redactor.String(line)
			if progress != nil {
				progress(clean)
			}
			lines = append(lines, clean)
			if len(lines) > maxLoggedLines {
				lines = lines[len(lines)-maxLoggedLines:]
			}
		}
		if msg.Error != "" || (msg.Detail != nil && msg.Detail.Message != "") {
			detail := msg.Error
			if msg.Detail != nil && msg.Detail.Message != "" {
				detail = msg.Detail.Message
			}
			// Appended to the log as well as recorded. The daemon sends the
			// failure in its own message with no stream text, so a log built
			// only from stream text ends at the last successful step and
			// omits the one line that says what went wrong.
			clean := b.redactor.String(detail)
			lines = append(lines, clean)
			if progress != nil {
				progress(clean)
			}
			// Recorded rather than returned immediately, so the rest of the
			// stream is drained. Closing early leaves the daemon holding the
			// build open.
			buildErr = errors.New(clean)
		}
	}
	return lines, buildErr
}

// tarWith returns the context archive with extra files appended.
//
// The archive is stored complete, so appending means dropping the two zero
// blocks that terminate it and writing the new entries in their place. Copying
// every entry through a fresh writer would be correct and would also copy a
// gigabyte of context to add four hundred bytes.
func (c *Context) tarWith(extra map[string]string) io.Reader {
	if len(extra) == 0 {
		return c.Tar()
	}
	body := c.tarball
	const terminator = 2 * 512
	if len(body) >= terminator {
		body = body[:len(body)-terminator]
	}
	var buf bytes.Buffer
	buf.Write(body)
	tw := tar.NewWriter(&buf)
	for _, name := range sortedKeys(extra) {
		content := extra[name]
		// Errors here cannot happen for an in memory writer with a valid
		// header, and a partial archive would fail in the daemon with a
		// message about an unexpected EOF, so they are checked rather than
		// ignored.
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o644, Size: int64(len(content)),
			ModTime: epoch, Format: tar.FormatPAX, Typeflag: tar.TypeReg,
		}); err != nil {
			return failingReader{err}
		}
		if _, err := io.WriteString(tw, content); err != nil {
			return failingReader{err}
		}
	}
	if err := tw.Close(); err != nil {
		return failingReader{err}
	}
	return bytes.NewReader(buf.Bytes())
}

type failingReader struct{ err error }

func (f failingReader) Read([]byte) (int, error) { return 0, f.err }
