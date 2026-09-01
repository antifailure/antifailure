package cli_test

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/cli"
	"github.com/antifailure/antifailure/engine/internal/clock"
)

// syncBuffer is a writer the status line goroutine and the test can both touch.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// The transcript is the same with or without a terminal. This is the property
// the whole design rests on: the elapsed time is real information a person
// watching wants, and it must not reach the bytes a snapshot test, a diff, or
// a CI log compares, because a duration in there makes every run differ from
// every other for no reason anybody can act on.
func TestProgress_StepsReadIdenticallyWithAndWithoutATerminal(t *testing.T) {
	t.Parallel()

	run := func(tty bool) string {
		var buf syncBuffer
		out := cli.NewOutput(&buf, &buf)
		out.TTY = tty
		clk := clock.NewFake(epoch)
		p := cli.NewProgress(out, clk)
		p.Step("branching the database from gv_20260830044013")
		clk.Advance(90 * time.Second)
		p.Step("sealing the network")
		p.Close()
		return buf.String()
	}

	plain := run(false)
	require.Equal(t, "  branching the database from gv_20260830044013\n  sealing the network\n", plain)
	// On a terminal the same records are there, with the status line drawn and
	// erased around them. Stripping the escape sequences and the carriage
	// returns leaves exactly the file a pipe would have received.
	require.Equal(t, plain, stripLive(run(true)))
}

// The status line says how long, and how to stop. Both halves are the point:
// a reader with no elapsed time cannot tell a slow step from a hung one, and
// the first interrupt here rolls the environment back rather than abandoning
// it, which is worth saying out loud.
func TestProgress_StatusLineCarriesElapsedTimeAndTheWayOut(t *testing.T) {
	t.Parallel()
	var buf syncBuffer
	out := cli.NewOutput(&buf, &buf)
	out.TTY = true
	clk := clock.NewFake(epoch)

	p := cli.NewProgress(out, clk)
	p.Step("building web")
	require.Contains(t, buf.String(), "00:00 elapsed, 00:00 on this step")
	require.Contains(t, buf.String(), "Ctrl-C to stop")

	// A second step restarts the step timer and leaves the run timer running,
	// which is what separates "this has taken a while" from "this one step is
	// not moving".
	clk.Advance(125 * time.Second)
	p.Step("branching the database")
	require.Contains(t, buf.String(), "02:05 elapsed, 00:00 on this step")
	p.Close()
}

// Nothing is left on the screen for the next thing to be printed over. The
// failure this guards against is an error message rendered on top of a status
// line that is still being rewritten underneath it.
func TestProgress_CloseErasesTheStatusLine(t *testing.T) {
	t.Parallel()
	var buf syncBuffer
	out := cli.NewOutput(&buf, &buf)
	out.TTY = true
	p := cli.NewProgress(out, clock.NewFake(epoch))
	p.Step("building web")
	p.Close()
	require.True(t, strings.HasSuffix(buf.String(), "\r\x1b[2K"),
		"the last thing written must be the erase, not a status line:\n%q", buf.String())

	// Twice, because the command that starts a run defers this and the failure
	// path calls it early.
	p.Close()
}

// Quiet and JSON mode draw nothing, for the same reason Printf writes nothing
// in them: a status line rewritten into a document a script is parsing is a
// parse error, and --quiet asked for only what was requested.
func TestProgress_DrawsNothingInQuietOrJSONMode(t *testing.T) {
	t.Parallel()
	for name, set := range map[string]func(*cli.Output){
		"json":  func(o *cli.Output) { o.Format = cli.FormatJSON },
		"quiet": func(o *cli.Output) { o.Quiet = true },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var buf syncBuffer
			out := cli.NewOutput(&buf, &buf)
			out.TTY = true
			set(out)
			p := cli.NewProgress(out, clock.NewFake(epoch))
			p.Step("building web")
			p.Close()
			require.Empty(t, buf.String())
		})
	}
}

// A dumb terminal is a character device with no erase-to-end-of-line, so the
// escape would be printed rather than obeyed.
func TestDetectTTY_RefusesADumbTerminal(t *testing.T) {
	t.Parallel()
	env := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}
	require.False(t, cli.DetectTTY(&bytes.Buffer{}, env(map[string]string{"TERM": "xterm"})),
		"a buffer is not a terminal")
	require.False(t, cli.DetectTTY(&bytes.Buffer{}, env(map[string]string{"TERM": "dumb"})))
	require.False(t, cli.DetectTTY(&bytes.Buffer{}, env(map[string]string{})))
}

// stripLive removes the status line and the escapes that draw it, leaving the
// permanent transcript.
//
// Everything from a carriage return to the erase that follows it is status.
// Nothing else in this output contains a carriage return, which is what makes
// the two separable at all, and separating them is the point: it is how this
// test proves a terminal and a pipe receive the same file.
func stripLive(s string) string {
	const erase = "\x1b[2K"
	var b strings.Builder
	for {
		i := strings.IndexByte(s, '\r')
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i])
		j := strings.Index(s[i:], erase)
		if j < 0 {
			return b.String()
		}
		s = s[i+j+len(erase):]
	}
}
