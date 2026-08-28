package hud

import (
	"fmt"
	"strings"
	"time"

	"github.com/antifailure/antifailure/engine/internal/events"
)

// Pane identifies a focusable region.
type Pane int

// The panes, in tab order.
const (
	PaneServices Pane = iota
	PaneNetwork
	PaneDatabase
	PaneAgents
	PaneLog
	paneCount
)

// String names the pane for its heading.
func (p Pane) String() string {
	switch p {
	case PaneServices:
		return "SERVICES"
	case PaneNetwork:
		return "NETWORK"
	case PaneDatabase:
		return "DATABASE"
	case PaneAgents:
		return "AGENTS"
	case PaneLog:
		return "LOG"
	}
	return ""
}

// Next returns the pane after this one, wrapping.
func (p Pane) Next() Pane { return (p + 1) % paneCount }

// Prev returns the pane before this one, wrapping.
func (p Pane) Prev() Pane { return (p + paneCount - 1) % paneCount }

// Layout describes how the panes are arranged at a given width.
//
// Three widths rather than a continuum, because a layout that changes on every
// column is one nobody can predict, and the useful question is only ever "does
// the second column fit".
type Layout int

const (
	// LayoutNarrow stacks every pane, for 80 columns.
	LayoutNarrow Layout = iota
	// LayoutWide puts the summary panes beside the services, from 120.
	LayoutWide
	// LayoutFull adds the agents column, from 160.
	LayoutFull
)

// LayoutFor picks the arrangement for a terminal width.
func LayoutFor(width int) Layout {
	switch {
	case width >= 160:
		return LayoutFull
	case width >= 120:
		return LayoutWide
	}
	return LayoutNarrow
}

// Render draws the whole dashboard as plain text.
//
// Plain text on purpose: colour and borders are applied by the Bubble Tea
// program on top of this, and keeping the geometry here means a golden frame
// test compares characters rather than escape sequences. A test that has to
// strip ANSI to read its own expectation is a test nobody will update.
//
// Deterministic by construction. Nothing in here reads the clock; elapsed time
// comes from the events themselves, so the same events always draw the same
// frame.
func (m *Model) Render(focus Pane) string {
	width := m.Width
	if width < 40 {
		width = 40
	}
	height := m.Height
	if height < 10 {
		height = 10
	}

	var b strings.Builder
	b.WriteString(m.status(width))
	b.WriteString("\n")

	body := height - 2 // status line and its rule
	switch LayoutFor(width) {
	case LayoutNarrow:
		b.WriteString(m.narrow(width, body, focus))
	case LayoutWide:
		b.WriteString(m.wide(width, body, focus))
	default:
		b.WriteString(m.full(width, body, focus))
	}
	return b.String()
}

// status is the one line that is always visible.
func (m *Model) status(width int) string {
	env := m.Env
	if env == "" {
		env = "all"
	}
	left := fmt.Sprintf("antifailure  %s  up %s", env, short(m.Elapsed()))

	ready, total := 0, 0
	for _, s := range m.Services() {
		total++
		if s.Ready {
			ready++
		}
	}
	right := fmt.Sprintf("%d/%d ready", ready, total)
	if n := len(m.Errors()); n > 0 {
		right += fmt.Sprintf("  %d errors", n)
	}
	if d := m.Dropped(); d > 0 {
		// Shown rather than hidden. A dashboard that drops silently is one
		// that lies by omission, and the number is how somebody knows to
		// distrust the rest of the frame.
		right += fmt.Sprintf("  %d dropped", d)
	}

	gap := width - len([]rune(left)) - len([]rune(right))
	if gap < 1 {
		return Truncate(left, width)
	}
	return left + strings.Repeat(" ", gap) + right
}

// short renders a duration the way a status line wants it.
func short(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	d = d.Round(time.Second)
	h := int(d.Hours())
	mn := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh%02dm", h, mn)
	}
	if mn > 0 {
		return fmt.Sprintf("%dm%02ds", mn, s)
	}
	return fmt.Sprintf("%ds", s)
}

func (m *Model) narrow(width, height int, focus Pane) string {
	// Every pane stacked, each getting a share of the height. The log takes
	// what is left, because it is the one that reads well at any size.
	per := (height - 4) / 4
	if per < 2 {
		per = 2
	}
	var b strings.Builder
	b.WriteString(pane(PaneServices, focus, width, per, m.servicesLines(width)))
	b.WriteString(pane(PaneNetwork, focus, width, 3, m.networkLines(width)))
	b.WriteString(pane(PaneDatabase, focus, width, 3, m.databaseLines(width)))
	b.WriteString(pane(PaneAgents, focus, width, per, m.agentLines(width)))
	rest := height - (per*2 + 6 + 4)
	if rest < 3 {
		rest = 3
	}
	b.WriteString(pane(PaneLog, focus, width, rest, m.logLines(width, rest-1)))
	return b.String()
}

func (m *Model) wide(width, height int, focus Pane) string {
	left := width * 3 / 5
	right := width - left - 1

	top := height / 2
	if top < 5 {
		top = 5
	}
	services := pane(PaneServices, focus, left, top, m.servicesLines(left))
	side := pane(PaneNetwork, focus, right, 3, m.networkLines(right)) +
		pane(PaneDatabase, focus, right, top-3, m.databaseLines(right))

	var b strings.Builder
	b.WriteString(beside(services, side, left, right))
	b.WriteString(pane(PaneAgents, focus, width, 4, m.agentLines(width)))
	rest := height - top - 4
	if rest < 3 {
		rest = 3
	}
	b.WriteString(pane(PaneLog, focus, width, rest, m.logLines(width, rest-1)))
	return b.String()
}

func (m *Model) full(width, height int, focus Pane) string {
	col := (width - 2) / 3
	// The last column absorbs the remainder. Dividing three ways and using the
	// same width for each leaves the row up to two cells short of the full
	// width, which is invisible in isolation and obvious the moment a
	// full-width rule is drawn underneath it.
	last := width - 2 - col*2
	top := height / 2
	if top < 6 {
		top = 6
	}

	services := pane(PaneServices, focus, col, top, m.servicesLines(col))
	middle := pane(PaneNetwork, focus, col, 4, m.networkLines(col)) +
		pane(PaneDatabase, focus, col, top-4, m.databaseLines(col))
	agents := pane(PaneAgents, focus, last, top, m.agentLines(last))

	var b strings.Builder
	b.WriteString(beside(beside(services, middle, col, col), agents, col*2+1, last))
	rest := height - top - 1
	if rest < 3 {
		rest = 3
	}
	b.WriteString(pane(PaneLog, focus, width, rest, m.logLines(width, rest-1)))
	return b.String()
}

// beside joins two rendered blocks column-wise, padding the shorter one.
func beside(left, right string, leftWidth, rightWidth int) string {
	l := strings.Split(strings.TrimRight(left, "\n"), "\n")
	r := strings.Split(strings.TrimRight(right, "\n"), "\n")
	n := len(l)
	if len(r) > n {
		n = len(r)
	}

	var b strings.Builder
	for i := 0; i < n; i++ {
		var lc, rc string
		if i < len(l) {
			lc = l[i]
		}
		if i < len(r) {
			rc = r[i]
		}
		b.WriteString(padTo(lc, leftWidth))
		b.WriteString(" ")
		b.WriteString(padTo(rc, rightWidth))
		b.WriteString("\n")
	}
	return b.String()
}

// pane draws one bordered region with a heading.
//
// The focused pane is marked with a heavier rule rather than with colour,
// because the HUD has to be readable in a terminal with no colour at all and
// in a recording where the colour did not survive.
func pane(p, focus Pane, width, height int, lines []string) string {
	if width < 4 {
		width = 4
	}
	rule := "─"
	if p == focus {
		rule = "━"
	}

	head := p.String()
	if p == focus {
		head = "▸ " + head
	}
	head = Truncate(head, width)

	var b strings.Builder
	b.WriteString(head)
	if pad := width - len([]rune(head)); pad > 0 {
		b.WriteString(" ")
		b.WriteString(strings.Repeat(rule, pad-1))
	}
	b.WriteString("\n")

	body := height - 1
	for i := 0; i < body; i++ {
		if i < len(lines) {
			b.WriteString(Truncate(lines[i], width))
		}
		b.WriteString("\n")
	}
	return b.String()
}

func padTo(s string, width int) string {
	r := []rune(s)
	if len(r) >= width {
		return string(r[:width])
	}
	return s + strings.Repeat(" ", width-len(r))
}

func (m *Model) servicesLines(width int) []string {
	svcs := m.Services()
	if len(svcs) == 0 {
		// An empty state that says what it is waiting for, rather than a blank
		// region that reads as broken.
		return []string{"no services yet"}
	}

	// The name column takes a third, the rest goes to the URL, which is the
	// part somebody wants to copy.
	nameCol := width / 3
	if nameCol < 8 {
		nameCol = 8
	}
	out := make([]string, 0, len(svcs))
	for _, s := range svcs {
		mark := "·"
		if s.Ready {
			mark = "✓"
		}
		detail := s.URL
		if detail == "" {
			detail = s.State
		}
		if detail == "" {
			detail = s.Detail
		}
		out = append(out, fmt.Sprintf("%s %s  %s", mark, padTo(Truncate(s.Name, nameCol), nameCol), detail))
	}
	return out
}

func (m *Model) networkLines(width int) []string {
	n := m.Network()
	out := []string{fmt.Sprintf("allow %d   deny %d   mock %d   record %d",
		n.Allowed, n.Denied, n.Mocked, n.Recorded)}
	if n.LastDenied != "" {
		out = append(out, "last denied "+Truncate(n.LastDenied, width-12))
	}
	return out
}

func (m *Model) databaseLines(width int) []string {
	d := m.Database()
	if d.Phase == "" {
		return []string{"idle"}
	}

	var out []string
	// The version line already carries the word "verified", so repeating it as
	// the phase says the same thing twice in a pane three lines tall.
	if !d.Verified || d.Phase != "verified" {
		out = append(out, d.Phase)
	}
	// The bar is for work in flight. Once the scan has passed, a bar stuck at
	// whatever the last progress event said reads as a contradiction: sixty
	// four per cent, and verified. The version is the useful thing then.
	if d.Percent > 0 && !d.Verified {
		out = append(out, bar(d.Percent, width-6)+fmt.Sprintf(" %d%%", d.Percent))
	}
	if d.Version != "" {
		state := "unverified"
		if d.Verified {
			state = "verified"
		}
		out = append(out, fmt.Sprintf("%s  %s", Truncate(d.Version, width-14), state))
	}
	if d.Findings > 0 {
		out = append(out, fmt.Sprintf("%d findings", d.Findings))
	}
	return out
}

// bar draws a progress bar with characters that survive a terminal with no
// colour and a recording that lost it.
func bar(percent, width int) string {
	if width < 4 {
		width = 4
	}
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	filled := width * percent / 100
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}

func (m *Model) agentLines(width int) []string {
	agents := m.Agents()
	if len(agents) == 0 {
		return []string{"no agents running"}
	}
	nameCol := width / 3
	if nameCol < 8 {
		nameCol = 8
	}
	out := make([]string, 0, len(agents))
	for _, a := range agents {
		out = append(out, fmt.Sprintf("%s  %d passed  %d failed  %s",
			padTo(Truncate(a.Name, nameCol), nameCol), a.Passed, a.Failed, a.State))
	}
	return out
}

// logLines returns the last `lines` entries, newest last.
//
// The count matters: a log pane that takes the FIRST n entries shows the
// beginning of the run for ever and never the thing that is happening now,
// which is the opposite of what a tail is for. It rendered that way until
// somebody looked at a frame.
// logLine formats one event for the tail.
//
// The environment is left out when it is the one the dashboard is watching.
// The header already names it, every line in the pane belongs to it, and
// repeating it spends the width of an environment id on every single row,
// which is width the message needed. An event from somewhere else keeps its
// env, because there the name is the only thing that explains the line.
//
// The event is a value, so blanking the field edits a copy and the model's
// tail is untouched.
func logLine(e events.Event, env string) string {
	if env != "" && e.Env == env {
		e.Env = ""
	}
	return e.String()
}

func (m *Model) logLines(width, lines int) []string {
	tail := m.Tail()
	out := make([]string, 0, len(tail))
	for _, e := range tail {
		prefix := ""
		if !e.TS.IsZero() {
			prefix = e.TS.UTC().Format("15:04:05") + " "
		}
		out = append(out, Truncate(prefix+logLine(e, m.Env), width))
	}
	if len(out) == 0 {
		return []string{"waiting for events"}
	}
	if lines > 0 && len(out) > lines {
		out = out[len(out)-lines:]
	}
	return out
}
