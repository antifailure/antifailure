//go:build !windows

package termimg

import (
	"os"
	"strings"
	"testing"
	"time"
)

// TestReadWithDeadlineIsTheFallbackArm exercises the read a unix falls back to
// when its terminal will not go non-blocking.
//
// It can be tested with a pipe because a pipe takes a read deadline on every
// unix, and it is a macOS TERMINAL the runtime poller refuses. It was named for
// Windows, which was said to use this read outright; on Windows a pipe refuses
// a deadline too, which is how the claim was found to be false.
func TestReadWithDeadlineIsTheFallbackArm(t *testing.T) {
	// A terminal that answers: the read returns as soon as the device
	// attributes reply is complete, without waiting the deadline out.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer func() { _ = r.Close() }()
	if _, err := w.WriteString(kittyReply); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = w.Close()

	started := time.Now()
	got, err := readWithDeadline(r, started.Add(5*time.Second))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got != kittyReply {
		t.Fatalf("read %q, want the terminal's reply", got)
	}
	if waited := time.Since(started); waited > 2*time.Second {
		t.Fatalf("waited %s for an answer that had already arrived; the reply "+
			"is not being recognised as complete", waited)
	}
	// And the answer is one Interpret can actually use, so this is the whole
	// path rather than a string comparison.
	if cap := Interpret(got, envOf(nil), Winsize{}); cap.Protocol != Kitty {
		t.Fatalf("the reply read back as %v", cap.Protocol)
	}

	// A terminal that answers nothing costs the deadline once and is reported
	// as unknown rather than as a terminal that draws nothing.
	silent, sw, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer func() { _ = silent.Close() }()
	defer func() { _ = sw.Close() }()

	quiet, err := readWithDeadline(silent, time.Now().Add(150*time.Millisecond))
	if err != nil {
		t.Fatalf("a silent terminal returned an error rather than nothing: %v", err)
	}
	if quiet != "" {
		t.Fatalf("a silent terminal returned %q", quiet)
	}
	if cap := Interpret(quiet, envOf(nil), Winsize{}); !strings.Contains(cap.Why, "did not answer") {
		t.Fatalf("a silent terminal was not reported as unasked: %q", cap.Why)
	}
}
