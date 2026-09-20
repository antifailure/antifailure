package fault

import (
	"context"
	"io"
	"strings"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
)

// logLimit is how much of a container's log is read back.
//
// The recovery evidence is a handful of lines the postmaster writes in the
// seconds around a crash, and the window is narrowed by time before it is
// narrowed by size. This is the bound that holds when a container is writing
// a line per query.
const logLimit = 4 << 20

// Shell runs commands inside one container the environment owns, and reads
// that container's log.
//
// It exists so that the packages which READ a database after a fault do not
// have to know about Docker, and so that they cannot reach a container this
// package would have refused: a Shell is only ever handed out by Shell(),
// which resolves through the same ownership guard every fault goes through,
// and every call re-proves ownership before it runs anything.
type Shell struct {
	i *Injector
	c Container
}

// Shell opens a command runner against the container a target resolves to.
func (i *Injector) Shell(ctx context.Context, t Target) (*Shell, error) {
	c, err := i.Resolve(ctx, t)
	if err != nil {
		return nil, err
	}
	owned, err := i.mustOwn(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	return &Shell{i: i, c: owned}, nil
}

// Container is what the shell runs in.
func (s *Shell) Container() Container { return s.c }

// Output is what a command said and how it ended.
type Output struct {
	Stdout   string
	ExitCode int
}

// Run runs one command and returns its output and exit code.
//
// A non-zero exit is not an error here. Half the commands this is used for are
// questions whose answer is the exit code: pg_isready exits 2 while a database
// is still starting, and turning that into a Go error would make "not ready
// yet" indistinguishable from "the daemon is gone".
func (s *Shell) Run(ctx context.Context, argv []string) (Output, error) {
	if _, err := s.i.mustOwn(ctx, s.c.ID); err != nil {
		return Output{}, err
	}
	res, err := s.i.exec(ctx, s.c.ID, argv)
	if err != nil {
		return Output{}, err
	}
	return Output{Stdout: res.Output, ExitCode: res.ExitCode}, nil
}

// Logs reads what the container has written since a moment.
//
// Since rather than a line count, because the evidence wanted is "what the
// postmaster said after the kill" and a tail of N lines answers a different
// question whenever the workload is chatty. The daemon takes whole seconds, so
// the moment is rounded down: a window that starts a second early contains the
// lines that matter, and one that starts a second late contains none of them.
func (s *Shell) Logs(ctx context.Context, since time.Time) (string, error) {
	if _, err := s.i.mustOwn(ctx, s.c.ID); err != nil {
		return "", err
	}
	opts := client.ContainerLogsOptions{ShowStdout: true, ShowStderr: true, Timestamps: true}
	if !since.IsZero() {
		opts.Since = since.UTC().Add(-time.Second).Format(time.RFC3339)
	}
	body, err := s.i.cli.ContainerLogs(ctx, s.c.ID, opts)
	if err != nil {
		return "", aferrors.Wrap(err, aferrors.AFRUN002, "endpoint", s.c.Name)
	}
	defer func() { _ = body.Close() }()

	// The container has no TTY, so the daemon frames stdout and stderr into
	// one stream with an eight byte header per chunk. Reading it raw puts the
	// headers into the middle of the log lines, and a line with a NUL in it
	// stops matching the string a recovery assertion is looking for.
	var out strings.Builder
	if _, err := stdcopy.StdCopy(&out, &out, io.LimitReader(body, logLimit)); err != nil {
		// Whatever arrived before the error is still evidence, and a truncated
		// log that shows the crash is worth more than an empty one that shows
		// the read failed.
		return out.String(), err
	}
	return out.String(), nil
}
