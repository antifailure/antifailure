package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// newTestServer builds a server with one echo tool and no run store.
//
// The store is nil on purpose for the transport tests: nothing here reaches a
// handler that needs one, and a nil store proves that the transport layer does
// not quietly depend on state it should not touch.
func newTestServer(t *testing.T) (*Server, *bytes.Buffer) {
	t.Helper()
	logs := &bytes.Buffer{}
	s := NewServer("test-project", nil, logs)
	s.Register(&Tool{
		Name: "echo", Description: "Echo one bounded string back.",
		ReadOnly: true,
		Input: &Schema{
			Type:     "object",
			Required: []string{"text"},
			Properties: map[string]*Schema{
				"text":  {Type: "string", MaxLength: 32, Description: "Text to echo."},
				"count": {Type: "integer", HasMin: true, Minimum: 1, HasMax: true, Maximum: 5},
			},
		},
		Handler: func(_ context.Context, c *Call, args map[string]any) (any, *Fault) {
			return map[string]any{"text": args["text"], "caller": c.Caller}, nil
		},
	})
	return s, logs
}

// converse feeds frames through a server and returns the responses.
func converse(t *testing.T, s *Server, frames ...string) []map[string]any {
	t.Helper()
	in := strings.NewReader(strings.Join(frames, "\n") + "\n")
	out := &bytes.Buffer{}
	require.NoError(t, s.Serve(context.Background(), in, out))

	var got []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &m), "response is not JSON: %s", line)
		got = append(got, m)
	}
	return got
}

const initFrame = `{"jsonrpc":"2.0","id":1,"method":"initialize",` +
	`"params":{"protocolVersion":"2025-06-18","clientInfo":{"name":"tester","version":"1"}}}`

func TestServe_MalformedFramesDoNotEndTheSession(t *testing.T) {
	t.Parallel()
	s, _ := newTestServer(t)

	// Every one of these is a frame a hostile or broken client can send. The
	// property under test is not the individual answers, it is that the frame
	// AFTER all of them is still served: a transport that can be ended by one
	// bad message is a transport one confused client can take down.
	got := converse(t, s,
		`this is not json at all`,
		`{"jsonrpc":"1.0","id":1,"method":"initialize"}`,
		`{"id":2}`,
		`[]`,
		`null`,
		`{"jsonrpc":"2.0","id":3,"method":"nonexistent/method"}`,
		initFrame,
		`{"jsonrpc":"2.0","id":9,"method":"tools/list"}`,
	)

	last := got[len(got)-1]
	require.Equal(t, float64(9), last["id"], "the session survived every malformed frame")
	require.Contains(t, last, "result")
	tools := last["result"].(map[string]any)["tools"].([]any)
	require.Len(t, tools, 1)
}

func TestServe_ParseErrorUsesANullID(t *testing.T) {
	t.Parallel()
	s, _ := newTestServer(t)
	got := converse(t, s, `{"jsonrpc":"2.0",`)

	require.Len(t, got, 1)
	require.Nil(t, got[0]["id"], "a frame whose id could not be read is answered with null")
	require.Equal(t, float64(codeParseError), got[0]["error"].(map[string]any)["code"])
}

func TestServe_NotificationsAreNotAnswered(t *testing.T) {
	t.Parallel()
	s, _ := newTestServer(t)
	// A notification carries no id, and answering one is itself a protocol
	// error. The initialize afterwards proves the loop kept going.
	got := converse(t, s,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","method":"nonexistent/notification"}`,
		initFrame,
	)
	require.Len(t, got, 1, "only the initialize was answered")
	require.Equal(t, float64(1), got[0]["id"])
}

func TestServe_OversizedFrameIsRefusedAndTheStreamStaysAligned(t *testing.T) {
	t.Parallel()
	s, _ := newTestServer(t)

	// One frame far past the cap, then a good one. The second frame is the
	// point: the reader has to consume the whole oversized line rather than
	// resynchronising in the middle of it, or the tail would be parsed as a
	// frame of its own and the client would see an error it cannot explain.
	//
	// Several times the cap rather than just over it, and that matters. A
	// frame only slightly too long is caught by the read that also consumes
	// the newline, so the stream realigns by luck rather than by the drain.
	// Overshooting by megabytes forces the refusal to happen with most of the
	// line still unread, which is the case the drain exists for.
	huge := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"junk":"` +
		strings.Repeat("A", 3*maxFrameBytes) + `"}}`
	got := converse(t, s, huge, initFrame)

	require.Len(t, got, 2)
	require.Nil(t, got[0]["id"])
	require.Equal(t, float64(codeInvalidRequest), got[0]["error"].(map[string]any)["code"])
	require.Contains(t, got[0]["error"].(map[string]any)["message"], "maximum")
	require.Equal(t, float64(1), got[1]["id"], "the next frame was read cleanly")
}

func TestServe_ToolsCallBeforeInitializeIsRefused(t *testing.T) {
	t.Parallel()
	s, _ := newTestServer(t)
	got := converse(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{"text":"hi"}}}`)

	require.Len(t, got, 1)
	require.Equal(t, float64(codeInvalidRequest), got[0]["error"].(map[string]any)["code"])
}

func TestServe_ProtocolVersionIsNegotiatedNotDemanded(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ asked, want string }{
		{"2025-06-18", "2025-06-18"},
		{"2024-11-05", "2024-11-05"},
		{"1999-01-01", supportedProtocols[0]},
		{"", supportedProtocols[0]},
	} {
		s, _ := newTestServer(t)
		frame := fmt.Sprintf(
			`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":%q}}`, tc.asked)
		got := converse(t, s, frame)
		result := got[0]["result"].(map[string]any)
		require.Equal(t, tc.want, result["protocolVersion"], "asked for %q", tc.asked)
	}
}

func TestServe_PanickingToolDoesNotEndTheSession(t *testing.T) {
	t.Parallel()
	s, logs := newTestServer(t)
	s.Register(&Tool{
		Name: "boom", Description: "Panics.",
		Input: &Schema{Type: "object", Properties: map[string]*Schema{}},
		Handler: func(context.Context, *Call, map[string]any) (any, *Fault) {
			panic("a defect inside a tool")
		},
	})

	got := converse(t, s, initFrame,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"boom","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"ping"}`)

	require.Len(t, got, 3)
	require.Equal(t, float64(codeInternalError), got[1]["error"].(map[string]any)["code"])
	require.Equal(t, float64(3), got[2]["id"], "the session survived the panic")
	require.Contains(t, logs.String(), "panicked", "the defect reached the server log")
	require.NotContains(t, got[1]["error"].(map[string]any)["message"], "a defect inside a tool",
		"the panic value is not disclosed to the caller")
}

func TestServe_StdoutCarriesOnlyProtocolFrames(t *testing.T) {
	t.Parallel()
	s, logs := newTestServer(t)
	// Everything diagnostic has to reach the log and nothing may reach the
	// protocol stream. One stray human readable line on standard output
	// corrupts the session for the client, and the failure surfaces somewhere
	// else entirely.
	in := strings.NewReader(strings.Join([]string{
		`garbage`, initFrame,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"nope","arguments":{}}}`,
	}, "\n") + "\n")
	out := &bytes.Buffer{}
	require.NoError(t, s.Serve(context.Background(), in, out))

	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var m map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &m),
			"every line on the protocol stream must be one JSON object")
		require.Equal(t, jsonRPCVersion, m["jsonrpc"])
	}
	require.NotEmpty(t, logs.String(), "diagnostics went to the log")
}

func TestFrameReader_DrainsAnOversizedLineWithoutLosingTheNext(t *testing.T) {
	t.Parallel()
	// Directly, because this is the property the transport test depends on
	// and it deserves to fail on its own terms rather than as a confusing
	// symptom two layers up.
	//
	// The cap is tiny so that the reader's buffer is tiny too, which is what
	// makes a hundred byte line arrive in many reads instead of one. Without
	// that this test would pass even with the drain deleted, because a line
	// that fits in one buffer has its newline consumed by the same read that
	// notices the overflow.
	body := strings.Repeat("x", 100)
	r := newFrameReader(strings.NewReader(body+"\n"+"kept\n"), 10)

	_, err := r.readFrame()
	require.ErrorIs(t, err, errFrameTooLarge)

	next, err := r.readFrame()
	require.NoError(t, err)
	require.Equal(t, "kept", string(next))
}

func TestFrameReader_HandlesAFinalFrameWithNoNewline(t *testing.T) {
	t.Parallel()
	r := newFrameReader(strings.NewReader("one\ntwo"), 1024)

	first, err := r.readFrame()
	require.NoError(t, err)
	require.Equal(t, "one", string(first))

	second, err := r.readFrame()
	require.NoError(t, err)
	require.Equal(t, "two", string(second))

	_, err = r.readFrame()
	require.ErrorIs(t, err, io.EOF)
}

func TestFrameReader_ToleratesCRLFAndBlankLines(t *testing.T) {
	t.Parallel()
	r := newFrameReader(strings.NewReader("one\r\n\r\n\ntwo\r\n"), 1024)

	first, err := r.readFrame()
	require.NoError(t, err)
	require.Equal(t, "one", string(first))

	second, err := r.readFrame()
	require.NoError(t, err)
	require.Equal(t, "two", string(second), "blank lines are skipped, not read as frames")
}
