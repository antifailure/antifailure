package cli

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// What af logs prints when there is nothing to print.
//
// It used to be "Nothing has been written yet" and "Bring the environment up
// with af up", whatever the reason. Observed on 2026-09-21 with the environment
// up and serving, where af up would have changed nothing. The rule these hold is
// that a remedy is printed only when running it would change the answer.

// fakeLogSource answers af logs without a runtime.
type fakeLogSource struct {
	lines     []provider.LogLine
	status    *env.Result
	statusErr error
	asked     int
}

func (f *fakeLogSource) Logs(context.Context, string, int) ([]provider.LogLine, error) {
	return f.lines, nil
}

func (f *fakeLogSource) Status(context.Context) (*env.Result, error) {
	f.asked++
	return f.status, f.statusErr
}

func runShowLogs(t *testing.T, src *fakeLogSource, service string) string {
	t.Helper()
	var buf bytes.Buffer
	out := NewOutput(&buf, &buf)
	require.NoError(t, showLogs(context.Background(), out, src, service, 60))
	return buf.String()
}

func upWith(services ...provider.RunningService) *env.Result {
	return &env.Result{EnvID: "ledger-main-1a2b3c", Services: services}
}

func TestLogsOffersAfUpWhenNothingIsRunning(t *testing.T) {
	src := &fakeLogSource{status: upWith()}
	got := runShowLogs(t, src, "ledger")
	require.Contains(t, got, "Nothing is running for this branch")
	require.Contains(t, got, "af up", "the one case af up changes the answer lost its remedy")
}

func TestLogsOffersNoAfUpToARunningServiceThatWroteNothing(t *testing.T) {
	src := &fakeLogSource{status: upWith(provider.RunningService{Name: "ledger", State: "running"})}
	got := runShowLogs(t, src, "ledger")
	require.Contains(t, got, "ledger is running and has written nothing yet.")
	require.NotContains(t, got, "af up",
		"told a reader to bring up an environment that is already up")
	require.Equal(t, 1, src.asked, "the empty case did not ask whether anything is running")
}

func TestLogsOffersNoAfUpWhenEveryServiceIsRunningAndSilent(t *testing.T) {
	src := &fakeLogSource{status: upWith(provider.RunningService{Name: "ledger", State: "running"})}
	got := runShowLogs(t, src, "")
	require.Contains(t, got, "none of its services has written anything yet")
	require.NotContains(t, got, "af up")
}

func TestLogsNamesAStoppedServiceByTheRuntimesWord(t *testing.T) {
	src := &fakeLogSource{status: upWith(provider.RunningService{
		Name: "ledger", State: "exited", Detail: "exit code 1",
	})}
	got := runShowLogs(t, src, "ledger")
	require.Contains(t, got, "the runtime reports it as exited: exit code 1")
	require.NotContains(t, got, "af up")
}

func TestLogsSaysADeclaredServiceIsAbsentFromARunningEnvironment(t *testing.T) {
	src := &fakeLogSource{status: upWith(provider.RunningService{Name: "web", State: "running"})}
	got := runShowLogs(t, src, "ledger")
	require.Contains(t, got, "nothing called ledger is running in it")
	require.Contains(t, got, "What is running: web.")
	require.NotContains(t, got, "af up")
}

func TestLogsOffersNoRemedyWhenStatusCannotBeRead(t *testing.T) {
	src := &fakeLogSource{statusErr: errors.New("daemon unreachable")}
	got := runShowLogs(t, src, "ledger")
	require.Contains(t, got, "checked: daemon unreachable")
	require.NotContains(t, got, "af up")
}

func TestLogsPrintsWhatADeclaredServiceWrote(t *testing.T) {
	// Running, so an empty message printed beside the output would be the
	// running one and the assertion below could see it.
	src := &fakeLogSource{lines: []provider.LogLine{
		{Service: "ledger", Text: "listening on :8080"},
		{Service: "ledger", Text: "posted entry 42"},
	}, status: upWith(provider.RunningService{Name: "ledger", State: "running"})}
	got := runShowLogs(t, src, "ledger")
	require.Contains(t, got, "listening on :8080\nposted entry 42\n")
	require.NotContains(t, got, "written nothing")
	require.Zero(t, src.asked, "status was asked with output in hand")
}
