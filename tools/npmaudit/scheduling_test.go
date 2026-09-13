package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A and B must start together; C waits until one slot is free. The barrier
// proves overlap without relying on the speed of registry requests.
func TestAuditConcurrencyIsBoundedAndResultsArePreserved(t *testing.T) {
	started := make(chan string, 3)
	release := make(chan struct{})
	var active, peak atomic.Int32
	audit := func(ctx context.Context, _, project string) ([]*finding, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		started <- project
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return []*finding{{Project: project}}, nil
	}
	type answer struct {
		found []*finding
		err   error
	}
	done := make(chan answer, 1)
	go func() {
		found, err := auditProjects(context.Background(), ".", []string{"a", "b", "c"}, io.Discard, auditBudget{2, time.Second, 2 * time.Second}, audit)
		done <- answer{found, err}
	}()
	overlapped := true
	for range 2 {
		select {
		case <-started:
		case <-time.After(500 * time.Millisecond):
			overlapped = false
		}
	}
	select {
	case <-started:
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	got := <-done
	t.Run("overlap", func(t *testing.T) {
		if !overlapped {
			t.Fatal("independent projects ran sequentially")
		}
	})
	t.Run("bound", func(t *testing.T) {
		if peak.Load() > 2 {
			t.Fatalf("worker limit exceeded: %d", peak.Load())
		}
	})
	t.Run("findings", func(t *testing.T) {
		seen := map[string]bool{}
		for _, f := range got.found {
			seen[f.Project] = true
		}
		if got.err != nil || len(seen) != 3 {
			t.Fatalf("lost completed projects: %v, %v", seen, got.err)
		}
	})
}

// A fails before B succeeds. Neither an early error nor a late success may
// erase the other project's result.
func TestAuditFailureDoesNotHideTheOtherProjects(t *testing.T) {
	var out bytes.Buffer
	want := errors.New("registry unavailable")
	probe := func(_ context.Context, _, project string) ([]*finding, error) {
		if project == "a" {
			return nil, want
		}
		return []*finding{{Project: project}}, nil
	}
	found, err := auditProjects(context.Background(), ".", []string{"a", "b"}, &out, auditBudget{1, time.Second, 2 * time.Second}, probe)
	t.Run("failure", func(t *testing.T) {
		if !errors.Is(err, want) {
			t.Fatalf("unknown registry result accepted: %v", err)
		}
	})
	t.Run("later project", func(t *testing.T) {
		if len(found) != 1 || found[0].Project != "b" {
			t.Fatalf("later project lost: %+v", found)
		}
	})
	for name, text := range map[string]string{"failed progress": "INCONCLUSIVE a after", "completed progress": "AUDITED b in"} {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(out.String(), text) {
				t.Fatalf("missing %q: %s", text, out.String())
			}
		})
	}
}

// A never responds. Its deadline frees the worker so B can still be audited.
func TestProjectDeadlineReleasesItsWorker(t *testing.T) {
	var ranB bool
	audit := func(ctx context.Context, _, project string) ([]*finding, error) {
		if project == "a" {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		ranB = true
		return nil, nil
	}
	_, err := auditProjects(context.Background(), ".", []string{"a", "b"}, io.Discard, auditBudget{1, 20 * time.Millisecond, 500 * time.Millisecond}, audit)
	if !errors.Is(err, context.DeadlineExceeded) || !ranB {
		t.Fatalf("project deadline failed to release worker: B ran=%v error=%v", ranB, err)
	}
}

// The overall budget ends while A runs and B is queued. B must be named as
// unstarted, never dispatched after the budget expired or reported as clean.
func TestOverallDeadlineNamesUnstartedProjects(t *testing.T) {
	var out bytes.Buffer
	var calls atomic.Int32
	audit := func(ctx context.Context, _, _ string) ([]*finding, error) {
		calls.Add(1)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	_, err := auditProjects(context.Background(), ".", []string{"a", "b"}, &out, auditBudget{1, 500 * time.Millisecond, 20 * time.Millisecond}, audit)
	t.Run("overall budget", func(t *testing.T) {
		if !errors.Is(err, context.DeadlineExceeded) || calls.Load() != 1 {
			t.Fatalf("work dispatched after overall budget: %d, %v", calls.Load(), err)
		}
	})
	t.Run("queued diagnostic", func(t *testing.T) {
		if !strings.Contains(out.String(), "INCONCLUSIVE b after 0s: not started: overall audit budget ended") {
			t.Fatalf("queued project disappeared: %s", out.String())
		}
	})
}

type progressWriter struct{ writes int }

func (w *progressWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes > 1 {
		return 0, io.ErrClosedPipe
	}
	return len(p), nil
}

func TestAuditProgressWriteFailureFailsTheGate(t *testing.T) {
	for name, writer := range map[string]io.Writer{"header": failedWriter{io.ErrClosedPipe}, "result": &progressWriter{}} {
		t.Run(name, func(t *testing.T) {
			_, err := auditProjects(context.Background(), ".", []string{"a"}, writer, auditBudget{1, time.Second, time.Second}, func(context.Context, string, string) ([]*finding, error) { return nil, nil })
			if !errors.Is(err, io.ErrClosedPipe) {
				t.Fatalf("lost progress accepted: %v", err)
			}
		})
	}
}

// The real child prints a clean JSON report and stderr but never exits within
// its budget. JSON alone must not let an unfinished process pass the gate.
//
// THE DEADLINE PASSES WHEN THE CHILD HAS WRITTEN, NOT AT A FIXED TIME. The child
// is this test binary started again through a shell, and before it writes a
// byte it has to start that shell, a Go runtime and the testing framework. A
// fixed 500 millisecond deadline started before any of that. Inside a full
// `cd tools && go test ./...` on a developer Mac the child had not reached its
// first line when that deadline passed, and the test reported evidence lost
// that had never been written. The child now says when it has written, and
// only then does the deadline pass, so the stderr assertion is about whether
// the tool keeps what was written, which is what the tool is for.
func TestAuditProjectKillsAStalledProcessAndKeepsEvidence(t *testing.T) {
	dir := npmFixture(t, "stall")
	ready := filepath.Join(t.TempDir(), "ready")
	t.Setenv("AF_NPM_TEST_READY", ready)
	ctx := newTestDeadline()
	t.Cleanup(ctx.pass)
	started := time.Now()
	finished := make(chan error, 1)
	go func() {
		_, err := auditProject(ctx, dir, "stalled")
		finished <- err
	}()
	if err := waitForFile(ready, finished); err != nil {
		t.Fatal(err)
	}
	// Logged so a slow start is visible: this is the time the fixed deadline
	// used to spend before the child could say anything.
	t.Logf("the child said it had written %s after the audit started", time.Since(started).Round(time.Millisecond))
	passed := time.Now()
	ctx.pass()
	err := <-finished
	elapsed := time.Since(passed)
	t.Run("deadline", func(t *testing.T) {
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("unfinished clean report accepted: %v", err)
		}
	})
	// Measured from the deadline, not from the start, so the time the child
	// takes to start is not counted. The child would stall for thirty seconds,
	// so one the tool failed to stop is unmistakable.
	t.Run("process stopped", func(t *testing.T) {
		if elapsed > 2*time.Second {
			t.Fatalf("child outlived its budget: %s", elapsed)
		}
	})
	for name, text := range map[string]string{"project": "stalled", "stderr": "stderr: registry request is waiting", "process error": "process error: signal: killed"} {
		t.Run(name, func(t *testing.T) {
			if err == nil || !strings.Contains(err.Error(), text) {
				t.Fatalf("missing %q: %v", text, err)
			}
		})
	}
}

// testDeadline is a context whose deadline passes when the test says so. The
// tool cannot tell it from one made by context.WithTimeout: Done closes and Err
// reports context.DeadlineExceeded, which is what the kill and the message are
// built on. It implements the four methods itself rather than embedding a
// context, so no caller can reach a second, unrelated Done.
type testDeadline struct {
	done chan struct{}
	once sync.Once
}

func newTestDeadline() *testDeadline {
	return &testDeadline{done: make(chan struct{})}
}

// Deadline reports none, because the moment is chosen by the test rather than
// by the clock.
func (d *testDeadline) Deadline() (time.Time, bool) { return time.Time{}, false }

func (d *testDeadline) Done() <-chan struct{} { return d.done }

func (d *testDeadline) Value(any) any { return nil }

func (d *testDeadline) Err() error {
	select {
	case <-d.done:
		return context.DeadlineExceeded
	default:
		return nil
	}
}

func (d *testDeadline) pass() { d.once.Do(func() { close(d.done) }) }

// waitForFile returns once path exists. A child that finishes first was never
// stalled, so that fails. The minute is not a timing assumption: a child that
// starts at all writes long before it, and only one that cannot start reaches
// it.
func waitForFile(path string, finished <-chan error) error {
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	giveUp := time.NewTimer(time.Minute)
	defer giveUp.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		select {
		case err := <-finished:
			return fmt.Errorf("the stalled child finished before it said it had written: %w", err)
		case <-giveUp.C:
			return errors.New("the stalled child never said it had written, so there was nothing to keep")
		case <-tick.C:
		}
	}
}

func npmFixture(t *testing.T, mode string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("npm executable fixture uses a POSIX shell")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	shim := filepath.Join(dir, "npm")
	write(t, shim, "#!/bin/sh\nexec '"+strings.ReplaceAll(executable, "'", "'\\''")+"' -test.run=TestAuditProcess\n")
	if err := os.Chmod(shim, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("AF_NPM_TEST_PROCESS", mode)
	return dir
}

func TestAuditProcess(t *testing.T) {
	mode := os.Getenv("AF_NPM_TEST_PROCESS")
	if mode == "" {
		return
	}
	if mode == "refuse" {
		fmt.Fprintln(os.Stdout, `{"message":"registry refused","error":{}}`)
		os.Exit(1)
	}
	if mode == "hold" {
		time.Sleep(3 * time.Second)
		os.Exit(0)
	}
	fmt.Fprintln(os.Stderr, "registry request is waiting")
	fmt.Fprintln(os.Stdout, `{"auditReportVersion":2,"vulnerabilities":{}}`)
	// Both writes are system calls on unbuffered files, so the bytes are in the
	// pipes by the time this file exists.
	if ready := os.Getenv("AF_NPM_TEST_READY"); ready != "" {
		if err := os.WriteFile(ready, nil, 0o600); err != nil {
			os.Exit(3)
		}
	}
	if mode == "stall" {
		time.Sleep(30 * time.Second)
	}
	if mode == "pipes" {
		child := exec.Command(os.Args[0], "-test.run=TestAuditProcess")
		child.Env = append(os.Environ(), "AF_NPM_TEST_PROCESS=hold")
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if err := child.Start(); err != nil {
			os.Exit(2)
		}
	}
	os.Exit(0)
}

func TestAnInheritedPipeCannotKeepAnAuditWaiting(t *testing.T) {
	dir := npmFixture(t, "pipes")
	_, err := auditProject(context.Background(), dir, "retained-output")
	if !errors.Is(err, exec.ErrWaitDelay) {
		t.Fatalf("retained pipe did not fail within the output budget: %v", err)
	}
}

func TestRunNeverSummarizesAnIncompleteScanAsAudited(t *testing.T) {
	dir := npmFixture(t, "refuse")
	write(t, filepath.Join(dir, "package.json"), `{"dependencies":{"fixture":"1"}}`)
	write(t, filepath.Join(dir, "package-lock.json"), `{}`)
	var out bytes.Buffer
	err := run([]string{dir}, &out)
	if err == nil || strings.Contains(out.String(), "lockfile(s) audited") || !strings.Contains(out.String(), "INCONCLUSIVE . after") {
		t.Fatalf("incomplete scan reached policy summary: %v\n%s", err, out.String())
	}
}

func TestRunSummarizesCompletedScan(t *testing.T) {
	dir := npmFixture(t, "clean")
	write(t, filepath.Join(dir, "package.json"), `{"dependencies":{"fixture":"1"}}`)
	write(t, filepath.Join(dir, "package-lock.json"), `{}`)
	var out bytes.Buffer
	err := run([]string{dir}, &out)
	if err != nil || !strings.Contains(out.String(), "1 lockfile(s) audited") {
		t.Fatalf("completed scan did not reach policy summary: %v\n%s", err, out.String())
	}
}
