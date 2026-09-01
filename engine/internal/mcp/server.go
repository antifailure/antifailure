package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
)

// The protocol versions this server speaks, newest first.
//
// A client naming one of these gets it back. A client naming anything else
// gets the newest, which is what the specification asks for: the version is
// negotiated rather than demanded, and a client too old or too new is told
// what it is talking to rather than disconnected.
var supportedProtocols = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

// serverName identifies this implementation in the handshake.
const serverName = "antifailure"

// Handler runs one tool call.
//
// It returns the result document, or a fault. It never returns both, and it
// never returns a bare error: everything a caller sees has passed through the
// closed vocabulary in errors.go.
type Handler func(ctx context.Context, call *Call, args map[string]any) (any, *Fault)

// Tool is one published capability.
type Tool struct {
	// Name is what the caller invokes.
	Name string
	// Title is a short human label for a tool picker.
	Title string
	// Description is what the model reads to decide whether to call it. It
	// says what question the tool answers and what it costs. It never
	// describes a way to weaken an experiment, because there is not one.
	Description string
	// Input is the argument schema, which is both validated and published.
	Input *Schema
	// Handler runs the call.
	Handler Handler
	// ReadOnly marks a tool that observes without changing anything. It is
	// published as a hint so a client can decide what to prompt for.
	ReadOnly bool
}

// Call is the per call context handed to a handler.
//
// Caller and Project are established by the server, never by the arguments of
// the call. That distinction is the whole of rule five: a field in a tool's
// arguments can narrow what a call does, and can never widen who it is or what
// it may reach.
type Call struct {
	// Caller identifies the connected client, from the initialize handshake.
	//
	// It is NOT an authenticated identity and must never be used to decide
	// what is permitted. This server speaks over standard input and output to
	// a client that started it, so it runs with exactly the privileges of the
	// person who launched it and there is nobody else on the connection to be
	// distinguished from. The name is used only to partition idempotency
	// keys, so that two clients sharing a checkout do not collide on a key,
	// and for the server log.
	Caller string
	// Project is the repository this server was started against. Fixed at
	// startup, identical for every call, and never taken from arguments.
	Project string
}

// Server serves the Model Context Protocol over a pair of streams.
type Server struct {
	tools map[string]*Tool
	order []string

	store   *Store
	project string
	// log receives every diagnostic. It is standard error, always, and the
	// reason is in the package comment: one human readable line on standard
	// output corrupts the session.
	log io.Writer

	mu          sync.Mutex
	out         *frameWriter
	initialized bool
	caller      string
}

// NewServer builds a server bound to one project and one run store.
//
// log must not be the same stream as the protocol output. There is no way for
// this package to check that, so it is stated here and honoured at the one
// place a server is constructed.
func NewServer(project string, store *Store, log io.Writer) *Server {
	if log == nil {
		log = io.Discard
	}
	return &Server{
		tools: map[string]*Tool{}, store: store,
		project: project, log: log,
	}
}

// Register publishes a tool. Registering the same name twice is a programming
// error and panics at startup rather than serving two meanings for one name.
func (s *Server) Register(t *Tool) {
	if _, exists := s.tools[t.Name]; exists {
		panic(fmt.Sprintf("mcp: the tool %q is registered twice", t.Name))
	}
	s.tools[t.Name] = t
	s.order = append(s.order, t.Name)
	sort.Strings(s.order)
}

// logf writes one diagnostic line to standard error.
func (s *Server) logf(format string, args ...any) {
	_, _ = fmt.Fprintf(s.log, "af mcp: "+format+"\n", args...)
}

// Serve reads frames until the input ends or the context is cancelled.
//
// It returns nil for an ordinary end of stream, which is how a client closes a
// session. Nothing a client can send makes this function return early: a
// malformed frame, an oversized frame, an unknown method and a panicking tool
// are each answered and the loop continues. A server that could be stopped by
// one bad message would be a server a single confused client could take down.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	reader := newFrameReader(in, maxFrameBytes)
	s.mu.Lock()
	s.out = newFrameWriter(out)
	s.mu.Unlock()

	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		frame, err := reader.readFrame()
		switch {
		case err == nil:
		case errors.Is(err, errFrameTooLarge):
			s.logf("refused a frame larger than %d bytes", maxFrameBytes)
			// Answered with a null id, because the frame was never parsed and
			// its id is unknown. That is what the specification prescribes for
			// a message that could not be read.
			s.respondError(nil, codeInvalidRequest,
				fmt.Sprintf("The message exceeds the maximum of %d bytes.", maxFrameBytes), nil)
			continue
		case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
			return nil
		default:
			// A closed pipe is how a client disconnects and is not a failure
			// worth reporting as one.
			s.logf("reading from the client: %v", err)
			return nil
		}

		var req request
		if err := json.Unmarshal(frame, &req); err != nil {
			s.respondError(nil, codeParseError, "The message is not valid JSON.", nil)
			continue
		}
		if req.JSONRPC != jsonRPCVersion || req.Method == "" {
			// A notification with a bad envelope is dropped rather than
			// answered, because answering a notification is itself a protocol
			// error.
			if !req.isNotification() {
				s.respondError(req.ID, codeInvalidRequest,
					"The message is not a JSON-RPC 2.0 request.", nil)
			}
			continue
		}
		s.dispatch(ctx, req)
	}
}

// dispatch routes one request and guarantees a reply for anything that is not
// a notification.
//
// The recover is not decoration. A panic in a tool would otherwise unwind
// through Serve and end the session, taking every other pending answer with
// it; here it becomes one internal error on one call and the session
// continues.
func (s *Server) dispatch(ctx context.Context, req request) {
	defer func() {
		if r := recover(); r != nil {
			s.logf("the method %s panicked: %v", req.Method, r)
			if !req.isNotification() {
				s.respondError(req.ID, codeInternalError,
					"The server failed to complete this call.", nil)
			}
		}
	}()

	switch req.Method {
	case "initialize":
		s.handleInitialize(req)
	case "notifications/initialized", "notifications/cancelled":
		// Notifications. Nothing to answer and nothing to do: this server
		// holds no per request state a cancellation notice could release,
		// because a long experiment is cancelled through cancel_rehearsal_run
		// rather than through the transport.
	case "ping":
		s.respond(req.ID, map[string]any{})
	case "tools/list":
		s.handleToolsList(req)
	case "tools/call":
		s.handleToolsCall(ctx, req)
	default:
		if req.isNotification() {
			return
		}
		s.respondError(req.ID, codeMethodNotFound,
			fmt.Sprintf("This server does not serve the method %q.", req.Method), nil)
	}
}

// initializeParams is the client half of the handshake.
type initializeParams struct {
	ProtocolVersion string `json:"protocolVersion"`
	ClientInfo      struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"clientInfo"`
}

func (s *Server) handleInitialize(req request) {
	var p initializeParams
	if len(req.Params) > 0 {
		// A handshake that will not decode is answered with the defaults
		// rather than refused. The client has told us its version in a shape
		// we do not recognise, which is exactly the case version negotiation
		// exists for.
		_ = json.Unmarshal(req.Params, &p)
	}

	version := supportedProtocols[0]
	for _, v := range supportedProtocols {
		if p.ProtocolVersion == v {
			version = v
			break
		}
	}
	caller := p.ClientInfo.Name
	if caller == "" {
		caller = "unknown-client"
	}
	if len(caller) > 128 {
		caller = caller[:128]
	}

	s.mu.Lock()
	s.initialized = true
	s.caller = caller
	s.mu.Unlock()

	s.logf("session with %s, protocol %s, project %s", caller, version, s.project)
	s.respond(req.ID, map[string]any{
		"protocolVersion": version,
		"capabilities": map[string]any{
			// listChanged is false: the tool set is fixed at startup, so there
			// is no notification this server would ever send.
			"tools": map[string]any{"listChanged": false},
		},
		"serverInfo": map[string]any{
			"name": serverName, "version": buildVersion, "title": "Antifailure",
		},
		"instructions": serverInstructions,
	})
}

// serverInstructions is what a client shows a model about this server.
//
// It states the one thing a model most needs to know and most often gets
// wrong: it chooses what to test, and it does not choose how safely the test
// runs. Saying so plainly costs nothing and heads off a class of calls that
// would only be refused.
const serverInstructions = `Antifailure rehearses a change against a sanitized copy of production before it merges.

You choose what to rehearse. Antifailure chooses the safety controls, and they are not adjustable from here: there is no way to disable sanitization, widen the network policy, lower a threshold, or point a run at a real database. A verdict is decided by the project's own policy, which lives in its manifest.

Submitting a rehearsal returns a run id immediately. Poll it with get_rehearsal_run. A verdict of INCONCLUSIVE means the experiment did not finish, so it says nothing about the change; it is never a weaker PASS.`

func (s *Server) handleToolsList(req request) {
	list := make([]map[string]any, 0, len(s.order))
	for _, name := range s.order {
		t := s.tools[name]
		entry := map[string]any{
			"name":        t.Name,
			"description": t.Description,
			"inputSchema": t.Input.document(),
		}
		if t.Title != "" {
			entry["title"] = t.Title
		}
		entry["annotations"] = map[string]any{
			"readOnlyHint": t.ReadOnly,
			// Nothing here destroys anything a caller owns: an experiment
			// creates a throwaway environment and removes it again.
			"destructiveHint": false,
			"openWorldHint":   false,
		}
		list = append(list, entry)
	}
	s.respond(req.ID, map[string]any{"tools": list})
}

// callParams is the client half of a tool call.
type callParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func (s *Server) handleToolsCall(ctx context.Context, req request) {
	s.mu.Lock()
	ready, caller := s.initialized, s.caller
	s.mu.Unlock()
	if !ready {
		s.respondError(req.ID, codeInvalidRequest,
			"This session has not been initialized.", nil)
		return
	}

	var p callParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		s.respondError(req.ID, codeInvalidParams, "The parameters are not valid.", nil)
		return
	}
	tool, known := s.tools[p.Name]
	if !known {
		// A method that does not exist is a protocol error; a TOOL that does
		// not exist is answered as a tool result, because the caller is a
		// model that should read the refusal and pick a real tool rather than
		// see a transport failure.
		s.respondToolFault(req.ID, faultf(FaultUnsupported,
			"This server does not serve a tool called %q.", trimForMessage(p.Name)))
		return
	}

	args, fault := validateArguments(tool.Input, p.Arguments)
	if fault != nil {
		s.respondToolFault(req.ID, fault)
		return
	}

	call := &Call{Caller: caller, Project: s.project}
	result, fault := tool.Handler(ctx, call, args)
	if fault != nil {
		s.logf("%s refused: %s", p.Name, fault.Error())
		if fault.wrapped != nil {
			s.logf("%s cause: %v", p.Name, fault.wrapped)
		}
		s.respondToolFault(req.ID, fault)
		return
	}
	s.respondToolResult(req.ID, result)
}

// respondToolResult sends a successful tool result.
//
// The document is sent twice: once as structured content, which a client that
// understands it can hand to a model as data, and once as text, for a client
// that only renders text. Encoding it twice rather than picking one keeps the
// server useful across client versions, and the two are the same bytes so they
// cannot disagree.
func (s *Server) respondToolResult(id json.RawMessage, doc any) {
	body, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		s.logf("encoding a result: %v", err)
		s.respondToolFault(id, internalFault(err))
		return
	}
	s.respond(id, map[string]any{
		"content":           []any{map[string]any{"type": "text", "text": string(body)}},
		"structuredContent": doc,
		"isError":           false,
	})
}

// respondToolFault sends a refusal as a tool result rather than a protocol
// error, with isError set so a client renders it as a failure.
func (s *Server) respondToolFault(id json.RawMessage, f *Fault) {
	doc := f.document()
	body, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		body = []byte(`{"kind":"error","code":"INTERNAL"}`)
	}
	s.respond(id, map[string]any{
		"content":           []any{map[string]any{"type": "text", "text": string(body)}},
		"structuredContent": doc,
		"isError":           true,
	})
}

// respond writes one successful response.
func (s *Server) respond(id json.RawMessage, result any) {
	if len(id) == 0 {
		return
	}
	s.write(response{JSONRPC: jsonRPCVersion, ID: id, Result: result})
}

// respondError writes one protocol level error.
func (s *Server) respondError(id json.RawMessage, code int, message string, data any) {
	if len(id) == 0 {
		// A notification gets no reply, not even a failure. The null id form
		// is reserved for a frame whose id could not be read at all, which the
		// caller signals by passing a literal null rather than nothing.
		id = json.RawMessage("null")
	}
	s.write(response{
		JSONRPC: jsonRPCVersion, ID: id,
		Error: &rpcError{Code: code, Message: message, Data: data},
	})
}

// write serialises one frame onto the protocol stream.
func (s *Server) write(r response) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.out == nil {
		return
	}
	if err := s.out.write(r); err != nil {
		s.logf("writing to the client: %v", err)
	}
}

// trimForMessage bounds caller supplied text before it appears in a message.
func trimForMessage(s string) string {
	if len(s) > 64 {
		return s[:64] + "..."
	}
	return s
}

// buildVersion is stamped by the command that constructs the server.
var buildVersion = "dev"

// SetBuildVersion records the engine version for the handshake.
func SetBuildVersion(v string) {
	if v != "" {
		buildVersion = v
	}
}
