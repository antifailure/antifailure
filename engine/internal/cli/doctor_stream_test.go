package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// gatedTerminal is the terminal. It counts the check lines as they are written and
// opens the slow check's way the moment the fast ones are all on screen, so
// the test's ordering is decided by what was written and not by a clock.
type gatedTerminal struct {
	buf     bytes.Buffer
	want    int
	seen    int
	release chan struct{}
}

func (g *gatedTerminal) Write(p []byte) (int, error) {
	n, err := g.buf.Write(p)
	g.seen += strings.Count(string(p), "  ok   ")
	if g.seen == g.want {
		close(g.release)
		g.want = -1
	}
	return n, err
}

var _ io.Writer = (*gatedTerminal)(nil)

// TestDoctorStreamsEachLineAsItsCheckLands is the two minute silence of
// 2026-09-06: a Docker daemon slow to answer, and af doctor printing nothing
// until it did, then all fifteen checks at once. Here the slow check cannot
// finish until every fast line has been written to the terminal, so a doctor
// that waited for it before printing anything could never release it, and
// fails on the bound below instead of passing by timing.
func TestDoctorStreamsEachLineAsItsCheckLands(t *testing.T) {
	const fast = 14
	term := &gatedTerminal{want: fast, release: make(chan struct{})}
	env := &Env{Out: NewOutput(term, term)}

	var seenWhenSlowRan string
	slow := func(context.Context, *Env, Prober) CheckResult {
		<-term.release
		// Ordered after the fourteenth line by the close in Write, and
		// before the consumer's next write by the send that follows this
		// return.
		seenWhenSlowRan = term.buf.String()
		return CheckResult{Name: "Docker daemon", Status: CheckPass, Detail: "answered at last"}
	}
	checks := []doctorCheck{quickCheck("CLI version"), quickCheck("Project manifest"), slow}
	for i := 3; i <= fast; i++ {
		checks = append(checks, quickCheck(fmt.Sprintf("Check %d", i)))
	}

	done := make(chan error, 1)
	go func() { done <- runDoctorText(context.Background(), env, fakeProber{}, checks) }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(30 * time.Second):
		t.Fatalf("the doctor never finished: the slow check was waiting for the fast lines "+
			"to be on the terminal and they were not. Printed so far:\n%s", term.buf.String())
	}

	require.Contains(t, seenWhenSlowRan, "Antifailure doctor", "the header came first")
	require.Contains(t, seenWhenSlowRan, "CLI version",
		"the first fast line was on the terminal before the slow check had finished")
	require.Equal(t, fast, strings.Count(seenWhenSlowRan, "  ok   "),
		"every fast line was written before the slow check finished")
	require.NotContains(t, seenWhenSlowRan, "Docker daemon")
	require.NotContains(t, seenWhenSlowRan, "This machine can run",
		"the verdict waits for the last check")

	whole := term.buf.String()
	require.Less(t, strings.LastIndex(whole, "  ok   "), strings.Index(whole, "This machine can run"),
		"the summary follows the last line")
	require.Contains(t, whole, "Docker daemon")
}

func TestDoctorJSONIsOneDocumentInCatalogOrder(t *testing.T) {
	// The JSON form does not stream. A script parsing it wants one document,
	// and in the catalog's order whatever order the probes answered in.
	var out bytes.Buffer
	env := &Env{Out: NewOutput(&out, &out)}
	env.Out.Format = FormatJSON
	checks := []doctorCheck{
		quickCheck("First"),
		func(context.Context, *Env, Prober) CheckResult {
			return CheckResult{Name: "Second", Status: CheckFail, Detail: "broken"}
		},
		quickCheck("Third"),
	}

	report := runDoctor(context.Background(), env, fakeProber{}, checks, nil)
	err := renderDoctor(env, report)

	require.ErrorIs(t, err, errDoctorFailed, "one failure fails the report")
	var doc DoctorReport
	dec := json.NewDecoder(&out)
	require.NoError(t, dec.Decode(&doc))
	require.False(t, dec.More(), "exactly one document")
	require.False(t, doc.OK)
	require.Equal(t, []string{"First", "Second", "Third"},
		[]string{doc.Checks[0].Name, doc.Checks[1].Name, doc.Checks[2].Name})
}

func quickCheck(name string) doctorCheck {
	return func(context.Context, *Env, Prober) CheckResult {
		return CheckResult{Name: name, Status: CheckPass, Detail: "fine"}
	}
}
