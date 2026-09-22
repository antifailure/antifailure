package cli_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/cli"
	"github.com/antifailure/antifailure/engine/internal/clock"
)

// screen is the smallest terminal that can tell a status line drawn in place
// from one left behind: a grid of a fixed width that wraps a row when a cell
// would pass its right edge, a carriage return that goes back to the start of
// the CURRENT row only, and the two erases the status line uses. Colour
// sequences are consumed and ignored.
//
// It exists because the failure it catches is invisible in the byte stream. A
// status line that wraps looks, as bytes, exactly like one that does not; it
// is only on a grid of a real width that the carriage return lands on the
// second row and the first one is left on the screen.
type screen struct {
	width   int
	rows    [][]rune
	row     int
	col     int
	pending bool // the last column was written and the wrap is deferred
}

func newScreen(width int) *screen { return &screen{width: width, rows: [][]rune{nil}} }

func (s *screen) line() []rune {
	for len(s.rows) <= s.row {
		s.rows = append(s.rows, nil)
	}
	return s.rows[s.row]
}

func (s *screen) put(r rune) {
	w := ansi.StringWidth(string(r))
	if s.pending || s.col+w > s.width {
		s.row++
		s.col = 0
		s.pending = false
	}
	l := s.line()
	for len(l) < s.col+w {
		l = append(l, ' ')
	}
	l[s.col] = r
	for i := 1; i < w; i++ {
		l[s.col+i] = 0 // the second cell of a wide rune
	}
	s.rows[s.row] = l
	s.col += w
	if s.col >= s.width {
		s.col = s.width
		s.pending = true
	}
}

func (s *screen) feed(b string) *screen {
	rs := []rune(b)
	for i := 0; i < len(rs); i++ {
		switch r := rs[i]; {
		case r == '\r':
			s.col, s.pending = 0, false
		case r == '\n':
			// The terminal's line discipline turns a newline into a carriage
			// return and a line feed, which is what a person sees.
			s.row++
			s.col, s.pending = 0, false
			s.line()
		case r == 0x1b && i+1 < len(rs) && rs[i+1] == '[':
			j := i + 2
			for j < len(rs) && (rs[j] < 0x40 || rs[j] > 0x7e) {
				j++
			}
			if j < len(rs) && rs[j] == 'K' {
				l := s.line()
				switch string(rs[i+2 : j]) {
				case "2":
					s.rows[s.row] = nil
				default:
					if s.col < len(l) {
						s.rows[s.row] = l[:s.col]
					}
				}
			}
			i = j
		default:
			s.put(r)
		}
	}
	return s
}

// text is the screen as lines, trailing blank rows and trailing spaces gone.
func (s *screen) text() []string {
	var out []string
	for _, l := range s.rows {
		out = append(out, strings.TrimRight(strings.ReplaceAll(string(l), "\x00", ""), " "))
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

// runUnderStatusLine drives a run the way af load compare does: a step through
// the Progress, and then records printed through the Output while the status
// line is on the screen, each followed by a tick that must bring the status
// line back. It returns what the terminal shows with the status line still up,
// and what it shows once the run is closed.
func runUnderStatusLine(t *testing.T, width int, color bool, label string, records []string) (live, closed []string, raw string) {
	t.Helper()
	var buf syncBuffer
	out := cli.NewOutput(&buf, &buf)
	out.TTY, out.Color, out.Width = true, color, width
	clk := clock.NewFake(epoch)
	p := cli.NewProgress(out, clk)
	p.Step(label)
	for _, r := range records {
		out.Printf("  %s\n", r)
		before := strings.Count(buf.String(), "\r")
		clk.Advance(time.Second)
		require.Eventually(t, func() bool {
			return strings.Count(buf.String(), "\r") > before
		}, 5*time.Second, time.Millisecond, "the tick after a record must draw the status line again")
		// The tick erases and draws; wait for the draw, which is the status
		// text after the last carriage return.
		require.Eventually(t, func() bool {
			s := buf.String()
			return strings.Contains(s[strings.LastIndexByte(s, '\r'):], "elapsed")
		}, 5*time.Second, time.Millisecond)
	}
	live = newScreen(width).feed(buf.String()).text()
	p.Close()
	raw = buf.String()
	return live, newScreen(width).feed(raw).text(), raw
}

// The film frame from 2026-09-22, as a test. af load compare printed each
// round through the Output while the status line was drawn, so "round 4 of 48:
// sending the mix at this build" was written onto the end of "Ctrl-C to stop
// and roll back". In a 110 column terminal the pair wrapped, and because
// nothing erased the status line before the record, every round left a status
// line glued into the transcript and the screen filled with them.
//
// The property: what the terminal shows is the transcript a pipe would have
// received, with at most one status line under it, on its own row. Measured on
// a grid of the real width, at the widths people use and at the floor, with
// colour on and off, and with a label whose runes are two cells wide.
func TestProgress_ARecordPrintedUnderTheStatusLineIsNotWrittenOntoIt(t *testing.T) {
	t.Parallel()
	labels := map[string]string{
		"ascii":      "bringing the base environment up for main",
		"multi-byte": "データベースを分岐しています gv_20260830044013 から",
	}
	records := []string{
		"round 1 of 48: sending the mix at main",
		"round 1 of 48: sending the mix at this build",
		"round 2 of 48: sending the mix at this build, which is long enough to wrap at forty",
	}
	for _, width := range []int{40, 80, 110, 200} {
		for _, color := range []bool{false, true} {
			for name, label := range labels {
				t.Run(fmt.Sprintf("%d/color=%v/%s", width, color, name), func(t *testing.T) {
					t.Parallel()
					live, closed, _ := runUnderStatusLine(t, width, color, label, records)

					// What a pipe would get, laid out on the same grid.
					var plain strings.Builder
					plain.WriteString("  " + label + "\n")
					for _, r := range records {
						plain.WriteString("  " + r + "\n")
					}
					want := newScreen(width).feed(plain.String()).text()

					require.Equal(t, want, closed,
						"once the run is closed the screen must be the transcript and nothing else")
					require.Len(t, live, len(want)+1,
						"with the status line up there must be exactly one more row than the transcript:\n%s",
						strings.Join(live, "\n"))
					require.Equal(t, want, live[:len(want)],
						"no transcript row may carry any part of a status line")
					require.Contains(t, live[len(want)], "elapsed",
						"the last row is the status line")
				})
			}
		}
	}
}

// statusDraws returns every status line as drawn: the text from each carriage
// return to the next carriage return or newline, colour removed. An erase is
// a carriage return followed by nothing, and is skipped.
func statusDraws(raw string) []string {
	var draws []string
	for _, seg := range strings.Split(raw, "\r")[1:] {
		if strings.HasPrefix(seg, "\x1b[2K") {
			continue // an erase, and whatever record was printed after it
		}
		if i := strings.IndexByte(seg, '\n'); i >= 0 {
			seg = seg[:i]
		}
		draws = append(draws, ansi.Strip(seg))
	}
	return draws
}

// The status line fits the row it is drawn on, strictly, at every width, and
// gives up its parts in order rather than being cut through the middle of the
// thing it exists to show. A line that wraps by one cell is the whole defect:
// the carriage return reaches back only to its second row.
func TestProgress_StatusLineFitsTheRowItIsDrawnOn(t *testing.T) {
	t.Parallel()
	cases := []struct {
		width      int
		hint, step bool // whether the interrupt hint and the step timer survive
	}{
		{width: 12}, {width: 20}, {width: 30}, {width: 34, step: true},
		{width: 40, step: true},
		{width: 80, step: true, hint: true},
		{width: 110, step: true, hint: true},
		{width: 200, step: true, hint: true},
	}
	for _, c := range cases {
		for _, color := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/color=%v", c.width, color), func(t *testing.T) {
				t.Parallel()
				var buf syncBuffer
				out := cli.NewOutput(&buf, &buf)
				out.TTY, out.Color, out.Width = true, color, c.width
				clk := clock.NewFake(epoch)
				// The clock never moves, so the only draw is the one Step makes
				// and no tick races it.
				p := cli.NewProgress(out, clk)
				p.Step("round 4 of 48: sending the mix at this build")
				p.Close()

				draws := statusDraws(buf.String())
				require.NotEmpty(t, draws)
				for _, d := range draws {
					require.LessOrEqual(t, ansi.StringWidth(d), c.width-1,
						"a status line of %d cells wraps a %d column terminal: %q", ansi.StringWidth(d), c.width, d)
					require.Contains(t, d, "00:00", "the run timer is the last thing to go")
					require.Equal(t, c.hint, strings.Contains(d, "Ctrl-C to stop and roll back"), "%q", d)
					require.Equal(t, c.step, strings.Contains(d, "step"), "%q", d)
				}
			})
		}
	}
}

// The terminal the status line is drawn on is measured when it is drawn, not
// when the command started. Two cases reach a width the startup measurement
// gets wrong: a terminal narrower than the floor the transcript is clamped to,
// and a pane resized in the middle of a long run.
func TestProgress_StatusLineFollowsTheTerminalItIsDrawnOn(t *testing.T) {
	t.Parallel()
	var buf syncBuffer
	out := cli.NewOutput(&buf, &buf)
	// What DetectWidth reported at startup, at or above its floor of 40.
	out.TTY, out.Width = true, 110
	now := 110
	out.LiveWidth = func() int { return now }
	clk := clock.NewFake(epoch)
	p := cli.NewProgress(out, clk)
	p.Step("round 1 of 48: sending the mix at main")
	now = 30 // the pane was split, or it was never wider than the floor
	p.Step("round 1 of 48: sending the mix at this build")
	p.Close()

	draws := statusDraws(buf.String())
	require.Len(t, draws, 2)
	require.Contains(t, draws[0], "Ctrl-C to stop and roll back", "at 110 columns the whole line fits")
	require.LessOrEqual(t, ansi.StringWidth(draws[1]), 29,
		"after the resize the line must fit 30 columns, not the 110 measured at startup: %q", draws[1])

	// A terminal that cannot be measured falls back to the startup width
	// rather than to none.
	now = 0
	require.Equal(t, 110, cli.LiveWidthOf(out))
}

// A record written without its newline, a prompt for example, is not ended by
// the status line. Drawing it there would put the status on the end of the
// partial line, and the erase before the rest of the record would take the
// partial line with it.
func TestProgress_StatusLineWaitsForAPartialLineToEnd(t *testing.T) {
	t.Parallel()
	var buf syncBuffer
	out := cli.NewOutput(&buf, &buf)
	out.TTY, out.Width = true, 80
	clk := clock.NewFake(epoch)
	p := cli.NewProgress(out, clk)
	p.Step("building web")
	out.Printf("  Continue? ")
	for i := 0; i < 3; i++ {
		clk.Advance(time.Second)
	}
	// The ticker goroutine has had three ticks to draw over the prompt.
	time.Sleep(50 * time.Millisecond)
	require.True(t, strings.HasSuffix(buf.String(), "  Continue? "),
		"nothing may be drawn after a partial line: %q", buf.String())
	out.Printf("yes\n")
	p.Close()
	require.Equal(t, []string{"  building web", "  Continue? yes"}, newScreen(80).feed(buf.String()).text())
}
