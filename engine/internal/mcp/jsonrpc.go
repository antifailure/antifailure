// Package mcp serves the engine's rehearsal primitives to a model over the
// Model Context Protocol.
//
// The server is a thin outcome oriented frontend over the orchestrator in
// engine/internal/env. It re-implements nothing: a tool call resolves to a
// typed Go call on the same code paths af ci and af insights use, and the
// deterministic evaluator in engine/internal/report owns every verdict. The
// model chooses the hypothesis; it never chooses the safety controls.
//
// Two properties hold everywhere in this package and both are load bearing.
//
// Standard output carries protocol frames and nothing else. A single stray
// line of human text on the stream corrupts the session for the client, and
// the failure looks like a parse error somewhere else entirely, so every
// diagnostic in this package goes to standard error and the engine's own
// progress output is redirected there before an orchestrator is built.
//
// The transport survives whatever the peer sends. A frame that is too large, a
// frame that is not JSON, and a frame that is JSON but not a request are all
// answered with an error and the loop continues. There is no input from the
// peer that ends the session other than the peer closing it.
package mcp

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
)

// jsonRPCVersion is the only version this server speaks.
const jsonRPCVersion = "2.0"

// maxFrameBytes caps one incoming frame.
//
// A cap rather than a stream, because every message this server accepts is a
// tool call whose arguments are names, paths and small numbers. A megabyte is
// already far past anything legitimate, so a frame above it is either a
// mistake or an attempt to make the server allocate, and both are answered the
// same way: refused, with the rest of the line drained so the next frame still
// parses.
const maxFrameBytes = 1 << 20

// request is one incoming JSON-RPC message.
//
// ID is kept as raw JSON rather than decoded. The specification allows a
// string or a number and requires the response to echo it back unchanged, so
// decoding it into either Go type would corrupt the other. Absent means a
// notification, which is answered with nothing at all, and that is why this is
// a pointer: a request with the id null is a request, and telling it apart
// from an absent id needs the distinction json.RawMessage gives us.
type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// isNotification reports whether no response may be sent.
func (r request) isNotification() bool { return len(r.ID) == 0 }

// response is one outgoing JSON-RPC message.
//
// Result is an interface holding a pointer so that a nil result still encodes
// as a present member rather than being omitted, because a response with
// neither result nor error is not a valid response.
type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// rpcError is the transport level error object.
//
// This is the JSON-RPC envelope and not the tool error model. A malformed
// frame or an unknown method is reported here; a tool that ran and decided
// against the caller reports itself inside a successful result, because a tool
// failure is data the model is meant to read rather than a protocol fault.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// The JSON-RPC error codes this server returns. These are the standard ones;
// the tool level model has its own vocabulary and lives in errors.go.
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
)

// errFrameTooLarge is returned by readFrame when a frame exceeded the cap.
//
// It is deliberately recoverable: the reader has already drained the rest of
// the oversized line, so the caller answers with an error and reads the next
// frame normally.
var errFrameTooLarge = errors.New("frame exceeds the maximum size")

// frameReader reads newline delimited JSON frames with a size cap.
//
// bufio.Scanner is not usable here. Its buffer limit is fatal: once a token
// exceeds the maximum, Scan reports an error and every later call returns
// false, so one oversized frame would end the session. Refusing a frame must
// not end a session, so the reading is done by hand.
type frameReader struct {
	r   *bufio.Reader
	max int
}

// readBufferBytes is the largest buffer the reader holds while looking for a
// newline. A frame longer than this is assembled across several reads.
const readBufferBytes = 64 << 10

func newFrameReader(r io.Reader, max int) *frameReader {
	if max <= 0 {
		max = maxFrameBytes
	}
	// The buffer is sized to the cap rather than fixed. Buffering more than
	// the largest frame that could ever be accepted is wasted memory, and the
	// clamp is also what lets a test drive the multi read path with a small
	// cap instead of a megabyte of input: with a fixed buffer, any frame
	// shorter than the buffer arrives whole and the assembly path below is
	// never exercised by anything a test can afford to write.
	size := max + 2 // room for the delimiter, so a maximal frame arrives whole
	if size > readBufferBytes {
		size = readBufferBytes
	}
	if size < 16 { // bufio's own minimum
		size = 16
	}
	return &frameReader{r: bufio.NewReaderSize(r, size), max: max}
}

// readFrame returns the next line, without its terminator.
//
// A line longer than the cap is drained to its newline and reported as
// errFrameTooLarge, so the stream stays aligned to frame boundaries and the
// next frame is read cleanly. Blank lines are skipped rather than treated as
// empty frames, because a peer that writes CRLF or a trailing newline is not
// making a protocol error.
func (f *frameReader) readFrame() ([]byte, error) {
	for {
		line, err := f.readLine()
		if err != nil {
			return line, err
		}
		if len(line) == 0 {
			continue
		}
		return line, nil
	}
}

// readLine accumulates one line, enforcing the cap as it goes.
func (f *frameReader) readLine() ([]byte, error) {
	var buf []byte
	over := false
	for {
		// ReadSlice returns what it has when its buffer fills, with
		// bufio.ErrBufferFull, which is the normal case for a long line
		// rather than a failure.
		chunk, err := f.r.ReadSlice('\n')
		switch {
		case err == nil:
			if over || len(buf)+len(chunk) > f.max {
				return nil, errFrameTooLarge
			}
			buf = append(buf, chunk...)
			return trimEOL(buf), nil
		case errors.Is(err, bufio.ErrBufferFull):
			if !over && len(buf)+len(chunk) > f.max {
				// Past the cap. Stop accumulating but keep reading to the
				// newline, so the bytes are consumed rather than parsed as
				// the next frame.
				over = true
				buf = nil
			}
			if !over {
				buf = append(buf, chunk...)
			}
		default:
			// io.EOF with bytes in hand is a final frame with no trailing
			// newline, which is a legitimate way for a peer to finish.
			if errors.Is(err, io.EOF) && !over && len(buf)+len(chunk) > 0 {
				if len(buf)+len(chunk) > f.max {
					return nil, errFrameTooLarge
				}
				buf = append(buf, chunk...)
				return trimEOL(buf), nil
			}
			return nil, err
		}
	}
}

// trimEOL removes the line terminator, tolerating CRLF.
func trimEOL(b []byte) []byte {
	b = b[:len(b):len(b)]
	if n := len(b); n > 0 && b[n-1] == '\n' {
		b = b[:n-1]
	}
	if n := len(b); n > 0 && b[n-1] == '\r' {
		b = b[:n-1]
	}
	return b
}

// frameWriter writes one JSON frame per line.
//
// Writes are serialised by the caller holding it, and every frame is written
// with a single Write so that two goroutines cannot interleave halves of two
// messages onto the stream. A decision log with interleaved bytes is the one
// artifact whose whole value is that it can be trusted afterwards, and the
// same is true of a protocol stream.
type frameWriter struct {
	w io.Writer
}

func newFrameWriter(w io.Writer) *frameWriter { return &frameWriter{w: w} }

// write encodes v and writes it as one newline terminated frame.
func (f *frameWriter) write(v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	body = append(body, '\n')
	_, err = f.w.Write(body)
	return err
}
