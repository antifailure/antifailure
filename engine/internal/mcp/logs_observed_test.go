package mcp

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// read_service_logs reported "the services have written nothing" for an
// environment that was not running, because the runtime answers both with the
// same empty list. logsUnavailableReason had a sentence for nothing running
// and nothing ever produced it.

func statusOf(res *env.Result, err error) func(context.Context) (*env.Result, error) {
	return func(context.Context) (*env.Result, error) { return res, err }
}

func TestLogsFromNothingRunningAreNotAnObservation(t *testing.T) {
	require.False(t, logsObserved(context.Background(), nil, statusOf(&env.Result{}, nil)))

	got, fault := readServiceLogs(context.Background(),
		func(context.Context, string, int) ([]provider.LogLine, bool, error) {
			return nil, logsObserved(context.Background(), nil, statusOf(&env.Result{}, nil)), nil
		}, "", 10)
	require.Nil(t, fault)
	require.Contains(t, got.(logsResult).Summary, "Nothing is running for this branch")
}

func TestLogsFromARunningSilentEnvironmentAreAnObservation(t *testing.T) {
	running := &env.Result{Services: []provider.RunningService{{Name: "ledger", State: "running"}}}
	require.True(t, logsObserved(context.Background(), nil, statusOf(running, nil)))
}

func TestLogsWithLinesDoNotAskWhetherAnythingIsRunning(t *testing.T) {
	lines := []provider.LogLine{{Service: "ledger", Text: "posted entry 42"}}
	require.True(t, logsObserved(context.Background(), lines, func(context.Context) (*env.Result, error) {
		t.Fatal("status was asked with output in hand")
		return nil, nil
	}))
}

func TestLogsKeepTheirAnswerWhenStatusCannotBeRead(t *testing.T) {
	require.True(t, logsObserved(context.Background(), nil, statusOf(nil, errors.New("daemon unreachable"))))
}
