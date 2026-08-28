package hud

import (
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/antifailure/antifailure/engine/internal/events"
)

// Plain is the fallback for anywhere that is not an interactive terminal: a CI
// log, a pipe, a file, a terminal too small to draw in.
//
// It prints one line per SIGNIFICANT event rather than every event, and that
// distinction is the whole design. A dashboard can show a progress bar moving
// because the previous frame is overwritten; a log file cannot, and a build log
// with four hundred lines of mask.progress in it has buried the one line that
// says what went wrong. So the noisy types are counted and reported once at the
// end, and everything a person would want to see in a postmortem is printed
// when it happens.
//
// It is safe for concurrent use, because unlike the model it is written to
// directly from whatever goroutine produced the event.
type Plain struct {
	mu  sync.Mutex
	w   io.Writer
	env string

	// suppressed counts the noisy events that were folded away, by type, so
	// the summary can say what was not shown rather than leaving the reader to
	// wonder whether it happened.
	suppressed map[events.Type]int
	shown      int

	// err is the first write failure, kept rather than ignored.
	//
	// A fallback whose output has gone to a closed pipe cannot fix that, but it
	// must not pretend either: the caller is usually a command that is about to
	// report success, and "I wrote 400 lines" and "I wrote 400 lines into a
	// pipe that stopped listening after the second one" deserve different exit
	// codes. Only the first is kept, because the four hundredth failure tells
	// you nothing the first did not.
	err error
}

// NewPlain returns a fallback writer. env empty means every environment.
func NewPlain(w io.Writer, env string) *Plain {
	return &Plain{w: w, env: env, suppressed: map[events.Type]int{}}
}

// noisy are the types that repeat often enough to bury a log.
//
// Chosen by what they are for rather than by measuring a rate: progress events
// exist to animate a bar, and a bar that cannot be redrawn is just repetition.
// Their terminal states are not in this list, so the reader still learns how
// each one ended.
var noisy = map[events.Type]bool{
	events.MaskProgress: true,
	"egress.decision":   true,
	"service.log":       true,
	"agent.step":        true,
}

// Significant reports whether an event earns a line of its own.
//
// Debug is never significant. Everything at warn or error always is, including
// a noisy type, because an egress denial is exactly the line somebody will grep
// for and suppressing it to keep a log tidy would be optimising the wrong
// thing.
func Significant(e events.Event) bool {
	switch e.Level {
	case events.LevelDebug:
		return false
	case events.LevelWarn, events.LevelError:
		return true
	}
	return !noisy[e.Type]
}

// Write prints the event if it is significant, and counts it if it is not.
func (p *Plain) Write(e events.Event) {
	if p.env != "" && e.Env != "" && e.Env != p.env {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	if !Significant(e) {
		p.suppressed[e.Type]++
		return
	}
	p.shown++

	var b strings.Builder
	if !e.TS.IsZero() {
		b.WriteString(e.TS.UTC().Format("15:04:05"))
		b.WriteString(" ")
	}
	if e.Level == events.LevelError || e.Level == events.LevelWarn {
		b.WriteString(strings.ToUpper(string(e.Level)))
		b.WriteString(" ")
	}
	// Same reason as the dashboard's log pane: every line in this stream
	// belongs to the environment the run is for, and repeating its name on
	// each one costs the width of an environment id per line for nothing.
	b.WriteString(logLine(e, p.env))
	if _, err := fmt.Fprintln(p.w, b.String()); err != nil && p.err == nil {
		p.err = err
	}
}

// Summary prints what was folded away. Called once when the stream ends.
//
// It prints nothing when nothing was suppressed, because a line saying zero
// events were hidden is itself noise.
func (p *Plain) Summary() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.suppressed) == 0 {
		return
	}

	types := make([]string, 0, len(p.suppressed))
	total := 0
	for t, n := range p.suppressed {
		types = append(types, fmt.Sprintf("%s=%d", t, n))
		total += n
	}
	sortStrings(types)
	if _, err := fmt.Fprintf(p.w, "%d further events not shown individually: %s\n",
		total, strings.Join(types, " ")); err != nil && p.err == nil {
		p.err = err
	}
}

// Err returns the first write failure, or nil. A caller that is about to report
// success should check it.
func (p *Plain) Err() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}

// Shown reports how many lines were printed.
func (p *Plain) Shown() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.shown
}

// Suppressed reports how many events of a type were folded away.
func (p *Plain) Suppressed(t events.Type) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.suppressed[t]
}

// sortStrings avoids pulling sort into this file's import list twice over; the
// list is tiny and stability is all that is wanted.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
