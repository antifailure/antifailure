package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/internal/live"
	"github.com/antifailure/antifailure/engine/internal/termimg"
)

// newWatchCommand runs the manifest's workflows and shows them live in the
// terminal, one pane per agent, switchable, as they happen.
//
// af test renders after the run is over, because its output is a verdict and a
// verdict exists only once the run finishes. af watch renders WHILE the run
// happens: the runner streams frames and steps over a local socket, and this
// command draws them. The frames stay on this machine; nothing here sends a
// frame to the control plane, which holds counts, verdicts and a reference to
// the durable recording, never a body.
func newWatchCommand(e *Env) *cobra.Command {
	var branch, runner string
	var only []string
	var attempts int
	var headed, noImages bool
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Watch the manifest's workflows run live in the terminal",
		Long: strings.TrimSpace(`
Runs the workflows and streams them as they happen, every agent on screen at
once, so you can see the swarm rather than read what it did afterwards.

Each pane names the personality driving that agent, the workflow it is running,
the account it signed in as, its state and its current step, and shows the
agent's most recent frame as a real picture in the terminal, about once a
second. Focus a pane with the number keys, the arrows or tab, press f to give
one agent the whole screen, and quit with q.

The picture needs a terminal that draws inline images, and the terminal is asked
rather than guessed at: iTerm2, kitty and anything that reports sixel graphics
all draw. A terminal that draws none of them gets the same panes with the
frame's own detail in place of the picture, and the footer says which terminal
you have. Set AF_IMAGES to iterm2, kitty, sixel or off when the question cannot
reach your terminal, which is what a multiplexer or a forwarded connection can
do to it. The frames never leave this machine for the control plane.

The verdict is the same one a plain run produces, printed when it finishes.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if fork := forkGate(e); fork.Refused {
				return refuseFork(fork)
			}
			o, err := orchestratorWithManifest3(e, watchLifecycle(e, branch))
			if err != nil {
				return err
			}
			return watchRun(cmd.Context(), e, o, noImages, env.TestOptions{
				Only: only, Attempts: attempts, Headed: headed, RunnerPath: runner,
			})
		},
	}
	cmd.Flags().StringVar(&branch, "branch", "", "the branch to watch, defaulting to the checkout's")
	cmd.Flags().StringSliceVar(&only, "only", nil, "watch just these workflows")
	cmd.Flags().IntVar(&attempts, "attempts", 0, "how many times to try a workflow")
	cmd.Flags().BoolVar(&headed, "headed", false, "show the browser window as well")
	cmd.Flags().StringVar(&runner, "runner", "", "override where the runner lives")
	cmd.Flags().BoolVar(&noImages, "no-images", false,
		"draw no pictures even on a terminal that would show them")
	return cmd
}

// watchRun wires the live socket to a hub, drives the run against it, and
// renders. The socket lives in a short-named temp directory because a unix
// socket path has a hard length limit that a deep artifacts path can exceed.
func watchRun(
	ctx context.Context, e *Env, o *env.Orchestrator, noImages bool, opts env.TestOptions,
) error {
	dir, err := os.MkdirTemp("", "afw")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	srv, err := live.Listen(filepath.Join(dir, "l.sock"))
	if err != nil {
		return err
	}
	defer func() { _ = srv.Close() }()

	hub := live.NewHub()
	go srv.Serve(hub)
	opts.LiveSocket = srv.Path()

	// The run drives on its own goroutine so the view can render while it goes.
	// Its result is read once the view ends.
	type outcome struct {
		report *env.TestReport
		err    error
	}
	result := make(chan outcome, 1)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	if e.Out.TTY {
		// Asked before the display starts, because the query writes to the
		// terminal and reads its reply, and neither is possible once a full
		// screen program owns both.
		prog := watchProgram(e, hub, imageCapability(e, noImages))
		go func() {
			report, runErr := o.Test(runCtx, opts)
			result <- outcome{report, runErr}
			// Give the last frames a moment to draw, then let the view finish.
			prog.Send(runDoneMsg{})
		}()
		if _, err := prog.Run(); err != nil {
			return err
		}
	} else {
		// No terminal to draw on. Stream each event as a plain line as it
		// arrives, which is the honest fallback for a pipe or a log, then print
		// the verdict. This is also what a script or a test sees.
		sub, unsub := hub.Subscribe()
		defer unsub()
		streamDone := make(chan struct{})
		go func() {
			for ev := range sub {
				e.Out.Println(plainLine(ev))
			}
			close(streamDone)
		}()
		report, runErr := o.Test(runCtx, opts)
		result <- outcome{report, runErr}
		unsub()
		<-streamDone
	}

	res := <-result
	if res.err != nil {
		return res.err
	}
	printWatchSummary(e, res.report)
	// The same exit rule af test uses, so a watched run and a plain run agree
	// on what counts as a failure.
	if res.report.AnyFailed() {
		return silent(failure(res.report))
	}
	if res.report.NothingVerified() {
		return silent(nothingVerified(res.report))
	}
	return nil
}

// watchLifecycle is how this command asks for its environment, and the one
// thing it says that af test does not is that the run must keep quiet.
//
// `af watch` had TWO writers on one terminal, and this was the omission.
// Building the orchestrator makes a Progress that is live on a TTY and rewrites
// a status line to e.Out.Out once a second, and the Bubble Tea program then
// takes the alternate screen on that same writer. Measured on a six second run
// against a real environment: six "elapsed, on this step" lines painted
// straight over the panes, with the permanent step records between them.
//
// The seam was already here and already carried a comment naming this exact
// case. `af up --live` has passed it since the dashboard was built and af watch
// never did, which is why the bug was invisible until this view had something
// worth covering up.
//
// Silence only when there is a screen to protect. A piped run has no dashboard,
// its events are already being streamed as plain lines, and the run's prose
// belongs in that log exactly as it does for af test.
//
// Nothing is lost either way: everything the prose would have said is on the
// panes, and the verdict is printed through e.Out after the program has exited
// and given the terminal back.
func watchLifecycle(e *Env, branch string) lifecycleOptions {
	return lifecycleOptions{branch: branch, silent: e.Out.TTY}
}

// imageCapability decides what this terminal will draw, and always says why.
//
// Three different answers land here and they are not the same fact: the person
// switched pictures off, there is no terminal to draw on, or the terminal was
// asked and answered. The reason travels into the view's footer so that
// somebody looking at a pane of text can tell which one they have rather than
// assuming the frames stopped arriving.
func imageCapability(e *Env, noImages bool) termimg.Capability {
	if noImages {
		return termimg.Capability{Why: "pictures were switched off with --no-images"}
	}
	in, inOK := e.Stdin.(*os.File)
	out, outOK := e.Out.Out.(*os.File)
	if !inOK || !outOK {
		// A test's buffer, or a stream somebody redirected. Nothing to query and
		// nothing that would draw the answer.
		return termimg.Capability{Why: "this run's streams are not a terminal, so no picture was drawn"}
	}
	getenv := e.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	// The terminal's own answer, then the person's, because a multiplexer or a
	// connection that rewrites escapes can stop the question ever reaching the
	// program that would answer it.
	return termimg.Override(termimg.Detect(in, out, getenv), getenv("AF_IMAGES"))
}

// plainLine renders one event as a single line for the non-terminal fallback.
func plainLine(ev live.Event) string {
	switch ev.T {
	case live.KindAgent:
		// The personality first, because on a run with a diversity block it is
		// the only thing that distinguishes one agent's lines from another's
		// running the same workflow.
		who := ev.Agent
		if ev.Persona != "" {
			who = ev.Persona + " / " + ev.Agent
		}
		if ev.Personality != "" {
			who = ev.Personality + " / " + who
		}
		if ev.Verdict != "" {
			return fmt.Sprintf("[%s] %s: %s", ev.State, who, ev.Verdict)
		}
		return fmt.Sprintf("[%s] %s", ev.State, who)
	case live.KindStep:
		return fmt.Sprintf("  %s: %s", ev.Agent, ev.Text)
	case live.KindFrame:
		return fmt.Sprintf("  %s: frame #%d %dx%d", ev.Agent, ev.Seq, ev.W, ev.H)
	case live.KindDone:
		return fmt.Sprintf("done: %d passed, %d failed, %d flaky, %d blocked, %d unverified",
			ev.Passed, ev.Failed, ev.Flaky, ev.Blocked, ev.Unverified)
	default:
		return ""
	}
}

// printWatchSummary prints the verdict the same way af test does, so a watched
// run ends with the same answer a plain run would.
func printWatchSummary(e *Env, report *env.TestReport) {
	if report == nil {
		return
	}
	e.Out.Println("")
	e.Out.Section("Verdict")
	for _, r := range report.Results {
		symbol, word := verdictStyle(e, r.Outcome.Verdict)
		e.Out.Status(symbol, r.Workflow, word)
	}
	e.Out.Printf("  %d passed, %d failed, %d flaky, %d blocked, %d unverified\n",
		report.Passed, report.Failed, report.Flaky, report.Blocked, report.Unverified)
}

// runDoneMsg tells the view the run has finished so it can quit.
type runDoneMsg struct{}

type liveEventMsg struct{}
type liveTickMsg struct{}

// watchModel is the Bubble Tea model for the live view. It holds no state of
// its own beyond the focus and the size: everything drawn comes from the hub's
// snapshot, so the view is a pure function of the hub and the focus and the
// rendering logic is the one already tested in the live package.
type watchModel struct {
	hub    *live.Hub
	color  bool
	images termimg.Capability
	width  int
	height int
	focus  int
	// solo gives the focused agent the whole screen. The swarm is the default
	// because the swarm is the thing worth seeing; solo is for looking closely
	// at one agent without losing where the others got to.
	solo  bool
	start time.Time
	done  bool
}

func (m watchModel) Init() tea.Cmd { return nil }

func (m watchModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case liveEventMsg, liveTickMsg:
		return m, nil
	case runDoneMsg:
		m.done = true
		return m, tea.Quit
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		case "f", "enter":
			m.solo = !m.solo
			return m, nil
		default:
			n := len(m.hub.Snapshot().Agents)
			m.focus = live.SwitchKey(msg.String(), m.focus, n)
			return m, nil
		}
	}
	return m, nil
}

func (m watchModel) View() string {
	return live.Render(m.hub.Snapshot(), live.RenderOpts{
		Focus:   m.focus,
		Width:   m.width,
		Height:  m.height,
		Color:   m.color,
		Light:   m.images.Background == termimg.BackgroundLight,
		Images:  m.images,
		Solo:    m.solo,
		Elapsed: mmssSince(m.start),
	})
}

// watchProgram builds the Bubble Tea program and the goroutines that pump live
// events and a one-second tick into it, so the view refreshes as the run
// progresses and the elapsed clock moves.
func watchProgram(e *Env, hub *live.Hub, images termimg.Capability) *tea.Program {
	m := watchModel{
		hub: hub, color: e.Out.Color, images: images,
		width: e.Out.Width, start: e.Clock.Now(),
	}
	prog := tea.NewProgram(m, tea.WithInput(e.Stdin), tea.WithOutput(e.Out.Out), tea.WithAltScreen())

	sub, unsub := hub.Subscribe()
	go func() {
		for range sub {
			prog.Send(liveEventMsg{})
		}
	}()
	ticker := time.NewTicker(time.Second)
	go func() {
		defer ticker.Stop()
		defer unsub()
		for range ticker.C {
			prog.Send(liveTickMsg{})
		}
	}()
	return prog
}

// mmssSince formats an elapsed duration as m:ss, matching the progress line.
func mmssSince(start time.Time) string {
	if start.IsZero() {
		return ""
	}
	d := time.Since(start)
	if d < 0 {
		d = 0
	}
	total := int(d.Seconds())
	return fmt.Sprintf("%d:%02d", total/60, total%60)
}
