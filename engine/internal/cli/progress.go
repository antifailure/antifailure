package cli

import (
	"fmt"
	"sync"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/antifailure/antifailure/engine/internal/clock"
)

// Progress is the status line under a long running command.
//
// The problem it solves: af up prints a line as each step begins and then goes
// quiet, sometimes for minutes, while a build runs or a four hundred megabyte
// golden is branched. A reader has no way to tell a slow step from a hung one,
// no idea how long they have been waiting, and no reminder that the run can be
// stopped at all, which matters here more than it does in most tools because
// the first interrupt rolls the environment back rather than abandoning it.
//
// Why it does not print a duration into the transcript: text output is byte
// stable for the same input, deliberately, so that a snapshot test and a diff
// mean something and a timestamp does not make every CI log differ from every
// other. So the elapsed time lives on a transient line that is rewritten in
// place and erased before the next record, and it is drawn only where there is
// a terminal to erase it on. A pipe, a file, a CI log and a test buffer get
// exactly the bytes they got before this existed.
//
// And it does not animate. There is no spinner and nothing cycles: the line
// changes once a second because the number on it changed, which is the only
// honest reason for a terminal to redraw itself. A spinner turning in front of
// a process that died three minutes ago is worse than no indicator at all,
// because it is a claim of liveness that nothing is checking.
type Progress struct {
	o     *Output
	clock clock.Clock

	// live is whether there is a terminal to rewrite a line on. When there
	// is not, Progress still prints every record and simply never draws a
	// status line, so a caller has one path rather than an if around every
	// step and one of them eventually getting it wrong.
	live bool

	mu      sync.Mutex
	begun   time.Time
	stepAt  time.Time
	drawn   bool
	stopped bool
	// midLine is whether the last record written through the Output stopped
	// short of a newline. A status line drawn then would land on the end of
	// that partial line, and the erase before the rest of it would take the
	// partial line with it.
	midLine bool

	ticker clock.Ticker
	done   chan struct{}
	wg     sync.WaitGroup
}

// tick is how often the status line is redrawn. One second, because the
// smallest unit it shows is a second and redrawing faster than the display
// changes is how a terminal ends up flickering for no reason.
const tick = time.Second

// NewProgress returns the status line for a run.
func NewProgress(o *Output, clk clock.Clock) *Progress {
	p := &Progress{
		o: o, clock: clk, begun: clk.Now(), stepAt: clk.Now(),
		live: o.TTY && o.Format != FormatJSON && !o.Quiet,
		done: make(chan struct{}),
	}
	if !p.live {
		return p
	}
	o.live = p
	p.ticker = clk.NewTicker(tick)
	p.wg.Add(1)
	go p.redraw()
	return p
}

// Step records that a new step has begun.
//
// line is printed as the permanent record, exactly as it was before this type
// existed, and the status line moves to sit under it.
func (p *Progress) Step(line string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped {
		return
	}
	p.erase()
	// Straight to the stream rather than through Printf, which would come
	// back through the guard and ask for the lock this already holds. The
	// same two refusals Printf makes are made here.
	if p.o.Format != FormatJSON && !p.o.Quiet {
		p.write(blockIndent + line + "\n")
		p.midLine = false
	}
	p.stepAt = p.clock.Now()
	if p.live {
		p.draw()
	}
}

// Close erases the status line and stops redrawing it.
//
// Safe to call more than once, because the command that starts a run defers
// this and the failure path calls it early so that an error is not printed on
// top of a status line that is still being rewritten underneath it.
func (p *Progress) Close() {
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return
	}
	p.stopped = true
	p.erase()
	if p.live {
		p.ticker.Stop()
		close(p.done)
	}
	p.mu.Unlock()
	p.wg.Wait()
}

// redraw rewrites the status line once a second until Close.
func (p *Progress) redraw() {
	defer p.wg.Done()
	for {
		select {
		case <-p.done:
			return
		case <-p.ticker.C():
			p.mu.Lock()
			if !p.stopped {
				p.erase()
				p.draw()
			}
			p.mu.Unlock()
		}
	}
}

// draw writes the status line. The caller holds the lock.
func (p *Progress) draw() {
	if p.midLine {
		return
	}
	// Measured now rather than at startup, so that a terminal resized in the
	// middle of a twenty minute run gets a line that fits the new width at the
	// next tick rather than one that wraps for the rest of the run.
	width := p.o.liveWidth()
	line := statusText(mmss(p.clock.Since(p.begun)), mmss(p.clock.Since(p.stepAt)), width-1)
	if line == "" {
		return
	}
	// The leading carriage return puts the cursor at the left margin before
	// anything is written, so the status line always starts at column zero
	// whatever left the cursor where it is. It also makes the line
	// self-delimiting in the byte stream: everything from a carriage return to
	// the erase that follows it is status, and everything else is the
	// transcript, which is what lets a test prove the two are the same file.
	p.write("\r" + p.o.S(StyleDim, line))
	p.drawn = true
}

// statusText composes the status line to fit in room cells, or returns "" when
// nothing useful fits.
//
// It must fit, strictly. A carriage return only reaches back to the start of
// the row the cursor is on, so a line that wraps by a single cell is redrawn
// from its second row, and the first row is left on the screen once a second
// for as long as the run lasts. One cell short of the width rather than the
// width itself, because a terminal that has just written its last column may
// already have moved the cursor to the next row.
//
// The versions are in the order they give things up. The interrupt affordance
// is the first thing to go when the terminal is narrow, because a reader who
// cannot see the elapsed time has lost the thing this line exists for, and a
// reader who cannot see the reminder still has the habit. The step timer goes
// next and the run timer last. Below five cells even the bare timer is cut,
// which is a terminal nobody is reading a status line on, and a clipped
// timer that stays on one row still beats a whole one that wraps.
func statusText(total, step string, room int) string {
	if room <= 0 {
		return ""
	}
	base := fmt.Sprintf("%s%s elapsed, %s on this step", blockIndent, total, step)
	for _, v := range []string{
		base + "   Ctrl-C to stop and roll back",
		base,
		fmt.Sprintf("%s%s elapsed, %s this step", blockIndent, total, step),
		fmt.Sprintf("%s%s elapsed", blockIndent, total),
		total + " elapsed",
		total,
	} {
		if cells(v) <= room {
			return v
		}
	}
	return ansi.Truncate(total, room, "")
}

// erase removes the status line, if one is on the screen. The caller holds the
// lock.
//
// Carriage return and an erase-to-end-of-line rather than a count of backspaces
// or a newline: the first is wrong the moment the line wraps, and the second
// leaves a growing column of stale status lines in the scrollback, which is
// the thing anybody reading the log afterwards least wants.
func (p *Progress) erase() {
	if !p.drawn {
		return
	}
	p.write("\r\x1b[2K")
	p.drawn = false
}

// write puts bytes on the stream without passing through the guard. The caller
// holds the lock, which the guard would otherwise ask for again.
func (p *Progress) write(s string) {
	p.o.note(fmt.Fprint(p.o.Out, s))
}

// statusGuard is the writer Output hands out while a status line is live. It
// erases the status line before anything else reaches the screen, and it does
// not draw it again: the next tick does that, under whatever was written, at
// most a second later.
type statusGuard struct{ p *Progress }

func (g statusGuard) Write(b []byte) (int, error) {
	p := g.p
	p.mu.Lock()
	defer p.mu.Unlock()
	p.erase()
	n, err := p.o.Out.Write(b)
	if len(b) > 0 {
		p.midLine = b[len(b)-1] != '\n'
	}
	return n, err
}

// mmss renders a duration as minutes and seconds, always the same width so
// that the line does not jitter as the number grows.
func mmss(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int(d / time.Second)
	if total >= 100*60 {
		return "99:59+"
	}
	return fmt.Sprintf("%02d:%02d", total/60, total%60)
}
