package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type failingEvidenceWriter struct {
	bytes.Buffer
	writes int
}

func (w *failingEvidenceWriter) Write(body []byte) (int, error) {
	w.writes++
	if w.writes == 2 {
		return 0, errors.New("log sink unavailable")
	}
	return w.Buffer.Write(body)
}

func TestEvidenceControlExposesFinalFailedDecisionWithoutLaterLog(t *testing.T) {
	for _, messageFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "decision", true: "message"}[messageFailure], func(t *testing.T) {
			sink := &failingEvidenceWriter{}
			p := &proxy{envID: "test-env", out: json.NewEncoder(sink)}
			dir, err := os.MkdirTemp("/tmp", "af-e-")
			require.NoError(t, err)
			defer os.RemoveAll(dir)
			socket := filepath.Join(dir, "s")
			listener, err := p.startEvidenceControl(socket)
			require.NoError(t, err)
			defer listener.Close()
			p.emit(record{Event: "ready"})
			if messageFailure {
				p.emitMessage(message{})
			} else {
				p.emit(record{Event: "decision", Mode: "block"})
			}
			var log record
			require.NoError(t, json.Unmarshal(bytes.TrimSpace(sink.Bytes()), &log))
			require.Equal(t, uint64(1), log.Seq)
			require.Equal(t, "ready", log.Event)
			conn, err := net.DialTimeout("unix", socket, time.Second)
			require.NoError(t, err)
			defer conn.Close()
			require.NoError(t, conn.SetDeadline(time.Now().Add(time.Second)))
			var status evidenceStatus
			require.NoError(t, json.NewDecoder(conn).Decode(&status))
			require.True(t, status.Failed)
			require.Equal(t, uint64(2), status.Sequence)
			require.Equal(t, "test-env", status.Env)
			require.Len(t, status.Instance, 32)
			p.out = json.NewEncoder(&bytes.Buffer{})
			p.emit(record{Event: "ready"})
			require.True(t, p.evidenceSnapshot().Failed)
		})
	}
}

func TestEvidenceWatermarkMatchesConcurrentCompleteLog(t *testing.T) {
	var sink bytes.Buffer
	p := &proxy{envID: "test-env", out: json.NewEncoder(&sink)}
	var wg sync.WaitGroup
	for n := 0; n < 100; n++ {
		wg.Add(1)
		go func() { defer wg.Done(); p.emit(record{Event: "decision", Mode: "block"}) }()
	}
	wg.Wait()
	decoder := json.NewDecoder(bytes.NewReader(sink.Bytes()))
	for n := 1; n <= 100; n++ {
		var r record
		require.NoError(t, decoder.Decode(&r))
		require.Equal(t, uint64(n), r.Seq)
	}
	status := p.evidenceSnapshot()
	require.Equal(t, uint64(100), status.Sequence)
	require.False(t, status.Failed)
}

func TestEvidenceWatermarkIncludesAcceptedWorkBeforeFirstRecord(t *testing.T) {
	p := &proxy{out: json.NewEncoder(&bytes.Buffer{})}
	p.emit(record{Event: "ready"})
	done := p.beginEvidenceWork()
	active := p.evidenceSnapshot()
	require.Equal(t, uint64(1), active.Sequence)
	require.Equal(t, uint64(1), active.Active)
	done()
	idle := p.evidenceSnapshot()
	require.Zero(t, idle.Active)
	require.Equal(t, uint64(1), idle.Work)
}
