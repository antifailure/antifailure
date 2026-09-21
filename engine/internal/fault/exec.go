package fault

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/moby/moby/client"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
)

// execOutputLimit is how much of a command's output is read.
//
// Everything run in here prints a process table, a permission mode or a df
// line. A command that produces more than this has not done what it was asked,
// and reading it all would put an unbounded amount of somebody else's output
// into a report.
const execOutputLimit = 64 << 10

// execPollInterval is how often the daemon is asked whether an exec finished.
const execPollInterval = 20 * time.Millisecond

// execResult is what a command inside a container said.
type execResult struct {
	Output   string
	ExitCode int
}

// exec runs one command inside a container and waits for it to finish.
//
// The exit code is read only once the daemon says the process is gone, for the
// reason the emulator probe gives: an exec that is still running reports exit
// code zero, so the first answer would read an unfinished command as a
// successful one. A fault that reported success because it had not finished
// yet is the whole category of defect this package is built to find.
func (i *Injector) exec(ctx context.Context, id string, argv []string) (execResult, error) {
	return i.execAs(ctx, id, "", argv)
}

// execAs runs one command inside a container as a named user.
//
// The user matters for exactly one thing and it matters completely: a
// permission fault is only a fault for a process the permissions apply to.
// An exec defaults to root, root ignores a directory's mode, and a read only
// data directory probed as root reports that the write succeeded. Probing as
// the directory's OWNER is what turns "the mode changed" into "a write now
// fails", which is the only version of that claim worth making.
func (i *Injector) execAs(ctx context.Context, id, user string, argv []string) (execResult, error) {
	created, err := i.cli.ExecCreate(ctx, id, client.ExecCreateOptions{
		User:         user,
		Cmd:          argv,
		AttachStdout: true,
		AttachStderr: true,
		// A terminal, so the two streams arrive as one unframed stream. What
		// is read here is a few short lines, and the eight byte per frame
		// multiplexing would have to be undone to compare them.
		TTY: true,
	})
	if err != nil {
		return execResult{}, fmt.Errorf("fault: creating an exec in %s: %w", id, err)
	}
	attached, err := i.cli.ExecAttach(ctx, created.ID, client.ExecAttachOptions{TTY: true})
	if err != nil {
		return execResult{}, fmt.Errorf("fault: attaching to an exec in %s: %w", id, err)
	}
	out, readErr := io.ReadAll(io.LimitReader(attached.Reader, execOutputLimit))
	attached.Close()
	if readErr != nil {
		return execResult{Output: string(out)}, fmt.Errorf("fault: reading an exec in %s: %w", id, readErr)
	}
	for {
		insp, err := i.cli.ExecInspect(ctx, created.ID, client.ExecInspectOptions{})
		if err != nil {
			return execResult{Output: string(out)}, fmt.Errorf("fault: inspecting an exec in %s: %w", id, err)
		}
		if !insp.Running {
			return execResult{Output: string(out), ExitCode: insp.ExitCode}, nil
		}
		select {
		case <-ctx.Done():
			return execResult{Output: string(out)}, ctx.Err()
		case <-time.After(execPollInterval):
		}
	}
}

// sh runs a shell command inside a container and fails on a non-zero exit.
//
// The output is carried into the error, because a fault that could not be
// injected has to say what the container said about it. "could not fill the
// disk" with the df output attached is a fact; without it, it is a guess.
func (i *Injector) sh(ctx context.Context, id, script string) (string, error) {
	res, err := i.exec(ctx, id, []string{"/bin/sh", "-c", script})
	if err != nil {
		return res.Output, err
	}
	if res.ExitCode != 0 {
		return res.Output, fmt.Errorf("fault: the command exited %d: %s",
			res.ExitCode, strings.TrimSpace(res.Output))
	}
	return res.Output, nil
}

// codedExec turns a failed command into the catalog's "the fault could not be
// injected" code, which is a different fact from "the fault was injected and
// the system survived".
func codedExec(f Fault, c Container, detail string) error {
	return aferrors.Coded(aferrors.AFCHS003,
		"fault", f.Name, "target", name(c), "detail", detail)
}
