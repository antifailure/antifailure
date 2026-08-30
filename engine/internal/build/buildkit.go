// BuildKit's side of a build log.
//
// The daemon accepts a BuildKit build through the same endpoint as the legacy
// builder and answers in a different language. The legacy builder sends
// `{"stream":"Step 3/34 : RUN npm ci\n"}`, which is already the log. BuildKit
// sends `{"id":"moby.buildkit.trace","aux":"<base64 protobuf>"}`, and the log
// is inside that protobuf. Setting the version without reading it produces a
// build with no log at all, and the log is the only thing that explains a
// failed build.
//
// The protobuf is decoded here by hand rather than by importing BuildKit. That
// is a deliberate trade and it is worth writing down. Importing
// github.com/moby/buildkit for a log format pulls grpc, containerd, and their
// transitive graph into a binary customers run in their own network, which is
// a large increase in the surface somebody has to trust for a decoder that
// reads four fields. The wire format is not a moving target: field numbers in
// a released protobuf are fixed forever, precisely so that a reader that does
// not know about a new field keeps working. So this reads the four fields it
// needs, skips everything else by the rules the format guarantees, and never
// fails a build because a message surprised it.
//
// The field numbers below were read off a real daemon rather than remembered,
// and testdata holds the captured stream they came from.

package build

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Field numbers in BuildKit's control.proto.
//
// From StatusResponse, Vertex, and VertexLog. A released protobuf never
// renumbers a field, so these are stable in a way a struct layout is not.
const (
	statusVertexes = 1 // repeated Vertex
	statusLogs     = 3 // repeated VertexLog

	vertexDigest    = 1
	vertexName      = 3
	vertexCached    = 4
	vertexCompleted = 6
	vertexError     = 7

	logVertex = 1
	logMsg    = 4
)

// buildKitMessage is one document in the daemon's response.
//
// Both shapes are read from one struct because a BuildKit build still sends
// plain `stream` documents for a few things, and the terminal error arrives in
// `error` either way.
type buildKitMessage struct {
	ID string `json:"id"`
	// Aux is raw because it is not one type. A status update carries a base64
	// string; the final `moby.image.id` document carries an object. Declaring
	// it as a string makes the decoder fail on that last document and abandon
	// the stream, which turns every successful build into an error with no
	// log. Found by reading a captured stream rather than by reasoning about
	// one, which is the only reason it was found before shipping.
	Aux    json.RawMessage `json:"aux"`
	Stream string          `json:"stream"`
	Error  string          `json:"error"`
	Detail *struct {
		Message string `json:"message"`
	} `json:"errorDetail"`
}

// tracePayload returns the status bytes in a document, if it holds any.
func (m buildKitMessage) tracePayload() ([]byte, bool) {
	if m.ID != traceID || len(m.Aux) == 0 {
		return nil, false
	}
	var encoded string
	if json.Unmarshal(m.Aux, &encoded) != nil {
		return nil, false
	}
	payload, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, false
	}
	return payload, true
}

// traceID marks a document whose aux payload is a BuildKit status update.
const traceID = "moby.buildkit.trace"

// vertexView is what has been seen about one step of the build.
type vertexView struct {
	num    int
	name   string
	named  bool
	done   bool
	cached bool
}

// buildKitLog turns the status stream into the log a person reads.
//
// The output deliberately resembles `docker build --progress=plain`, because
// that is what somebody comparing a failing CI build against their own machine
// will have in front of them. A step is numbered on first sight, its output is
// prefixed with that number, and it ends in DONE, CACHED, or ERROR.
type buildKitLog struct {
	seen  map[string]*vertexView
	order int
}

func newBuildKitLog() *buildKitLog {
	return &buildKitLog{seen: map[string]*vertexView{}}
}

// vertex returns the view for a digest, assigning a number on first sight.
func (l *buildKitLog) vertex(digest string) *vertexView {
	if v, ok := l.seen[digest]; ok {
		return v
	}
	l.order++
	v := &vertexView{num: l.order}
	l.seen[digest] = v
	return v
}

// consume decodes one status payload and returns the lines it produced.
//
// A payload that does not decode produces no lines and no error. A build must
// not fail because its log was unreadable, and a decoder that returns an error
// here would do exactly that at the moment the log matters most.
func (l *buildKitLog) consume(payload []byte) []string {
	var out []string
	forEachField(payload, func(field int, wire int, body []byte, _ uint64) {
		if wire != wireBytes {
			return
		}
		switch field {
		case statusVertexes:
			out = append(out, l.vertexLines(body)...)
		case statusLogs:
			out = append(out, l.logLines(body)...)
		}
	})
	return out
}

// vertexLines renders what changed about one step.
func (l *buildKitLog) vertexLines(body []byte) []string {
	var digest, name, vErr string
	var cached, completed bool
	forEachField(body, func(field int, wire int, b []byte, v uint64) {
		switch {
		case field == vertexDigest && wire == wireBytes:
			digest = string(b)
		case field == vertexName && wire == wireBytes:
			name = string(b)
		case field == vertexCached && wire == wireVarint:
			cached = v != 0
		case field == vertexCompleted && wire == wireBytes:
			completed = true
		case field == vertexError && wire == wireBytes:
			vErr = string(b)
		}
	})
	if digest == "" {
		return nil
	}
	view := l.vertex(digest)
	if name != "" {
		view.name = name
	}

	var out []string
	// The name once, when the step first has one. Repeating it on every
	// update would produce a log that is mostly the same forty lines.
	if !view.named && view.name != "" {
		view.named = true
		out = append(out, fmt.Sprintf("#%d %s", view.num, view.name))
	}
	switch {
	case vErr != "":
		// Not gated on `done`, because the error is the line somebody needs
		// and a step can report one more than once.
		out = append(out, fmt.Sprintf("#%d ERROR: %s", view.num, vErr))
	case view.done:
		// Already reported.
	case cached:
		view.done, view.cached = true, true
		out = append(out, fmt.Sprintf("#%d CACHED", view.num))
	case completed:
		view.done = true
		out = append(out, fmt.Sprintf("#%d DONE", view.num))
	}
	return out
}

// logLines renders output a step wrote.
func (l *buildKitLog) logLines(body []byte) []string {
	var digest string
	var msg []byte
	forEachField(body, func(field int, wire int, b []byte, _ uint64) {
		switch {
		case field == logVertex && wire == wireBytes:
			digest = string(b)
		case field == logMsg && wire == wireBytes:
			msg = b
		}
	})
	if len(msg) == 0 {
		return nil
	}
	num := 0
	if digest != "" {
		num = l.vertex(digest).num
	}
	var out []string
	for _, line := range strings.Split(strings.TrimRight(string(msg), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		out = append(out, fmt.Sprintf("#%d %s", num, line))
	}
	return out
}

// Steps returns the step names seen, in the order they were numbered. Tests
// use it; so does the message that says how much of a build was cached.
func (l *buildKitLog) Steps() []string {
	nums := make([]*vertexView, 0, len(l.seen))
	for _, v := range l.seen {
		nums = append(nums, v)
	}
	sort.Slice(nums, func(i, j int) bool { return nums[i].num < nums[j].num })
	out := make([]string, 0, len(nums))
	for _, v := range nums {
		if v.name != "" {
			out = append(out, v.name)
		}
	}
	return out
}

// streamBuildKit reads a BuildKit response the way stream reads a legacy one.
func (b *DockerBuilder) streamBuildKit(r io.Reader, progress func(string)) ([]string, error) {
	dec := json.NewDecoder(r)
	log := newBuildKitLog()
	var lines []string
	var buildErr error

	emit := func(line string) {
		clean := b.redactor.String(line)
		if progress != nil {
			progress(clean)
		}
		lines = append(lines, clean)
		if len(lines) > maxLoggedLines {
			lines = lines[len(lines)-maxLoggedLines:]
		}
	}

	for {
		var msg buildKitMessage
		if err := dec.Decode(&msg); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return lines, err
		}

		if payload, ok := msg.tracePayload(); ok {
			for _, line := range log.consume(payload) {
				emit(line)
			}
			continue
		}
		// A BuildKit build still sends a few plain documents, and the
		// terminal failure arrives as one.
		for _, line := range strings.Split(strings.TrimRight(msg.Stream, "\n"), "\n") {
			if strings.TrimSpace(line) != "" {
				emit(line)
			}
		}
		if detail := failureText(msg); detail != "" {
			emit(detail)
			// Recorded rather than returned, so the rest of the stream is
			// drained and the daemon is not left holding the build open.
			buildErr = errors.New(b.redactor.String(detail))
		}
	}
	return lines, buildErr
}

func failureText(msg buildKitMessage) string {
	if msg.Detail != nil && msg.Detail.Message != "" {
		return msg.Detail.Message
	}
	return msg.Error
}

// Protobuf wire types, from the encoding's own specification.
const (
	wireVarint = 0
	wire64     = 1
	wireBytes  = 2
	wireStart  = 3 // deprecated groups
	wireEnd    = 4
	wire32     = 5
)

// forEachField walks a protobuf message, calling fn for every field it can
// read and skipping the rest.
//
// Bounds are checked on every read and a malformed message ends the walk
// rather than panicking, because this parses bytes produced by another process
// and the caller is a build that must not crash over its own log. Unknown
// fields are skipped by the length rules the format guarantees, which is what
// makes this survive a BuildKit that adds a field.
func forEachField(b []byte, fn func(field int, wire int, body []byte, value uint64)) {
	i := 0
	for i < len(b) {
		tag, n := readVarint(b[i:])
		if n == 0 {
			return
		}
		i += n
		field, wire := int(tag>>3), int(tag&7)
		if field <= 0 {
			return
		}
		switch wire {
		case wireVarint:
			v, n := readVarint(b[i:])
			if n == 0 {
				return
			}
			i += n
			fn(field, wire, nil, v)
		case wire64:
			if i+8 > len(b) {
				return
			}
			i += 8
		case wireBytes:
			l, n := readVarint(b[i:])
			if n == 0 {
				return
			}
			i += n
			// The length is attacker controlled in the general case and
			// daemon controlled here. Either way it is checked against what
			// is actually present before it is used to slice.
			if l > uint64(len(b)-i) {
				return
			}
			body := b[i : i+int(l)]
			i += int(l)
			fn(field, wire, body, 0)
		case wire32:
			if i+4 > len(b) {
				return
			}
			i += 4
		case wireStart, wireEnd:
			// Groups were removed from the language long before this message
			// was written. One appearing means the bytes are not what they
			// claim to be, and guessing at the nesting would be worse than
			// stopping.
			return
		default:
			return
		}
	}
}

// readVarint decodes one base 128 varint, returning the value and how many
// bytes it used. A zero length means the encoding was truncated or overlong.
func readVarint(b []byte) (uint64, int) {
	var v uint64
	for i := 0; i < len(b); i++ {
		if i == 10 {
			// Ten bytes is the most a 64 bit varint can occupy. More than
			// that is a malformed message, not a larger number.
			return 0, 0
		}
		v |= uint64(b[i]&0x7f) << (7 * i)
		if b[i]&0x80 == 0 {
			return v, i + 1
		}
	}
	return 0, 0
}
