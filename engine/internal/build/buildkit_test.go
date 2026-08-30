package build

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/redact"
)

// The regression this whole file exists for.
//
// BuildKit answers the build endpoint in a different language from the legacy
// builder, and asking for it without reading that language produces a build
// with an empty log. A build log is the only thing that explains a failed
// build, so a fast build with no log is a worse product than a slow build with
// one. The fixture is a real failing build captured from a real daemon.
func TestStreamBuildKit_AFailingBuildStillExplainsItself(t *testing.T) {
	b := &DockerBuilder{redactor: redact.New()}
	body, err := os.Open("testdata/buildkit-failing-build.ndjson")
	require.NoError(t, err)
	defer func() { _ = body.Close() }()

	lines, buildErr := b.streamBuildKit(body, nil)

	require.Error(t, buildErr, "a build that failed must return an error")
	require.NotEmpty(t, lines, "a failing build with no log is the bug this guards")

	log := strings.Join(lines, "\n")
	require.Contains(t, log, "RUN echo about-to-fail",
		"the step that failed has to be named")
	require.Contains(t, log, "about-to-fail",
		"what the step printed has to survive")
	require.Contains(t, log, "exit code: 7",
		"the reason has to be the last thing somebody reads")
	require.Contains(t, buildErr.Error(), "exit code: 7")
}

// A successful build produces a readable log too, in the shape somebody
// comparing against their own terminal will recognise.
func TestStreamBuildKit_ASuccessfulBuildReadsLikeProgressPlain(t *testing.T) {
	b := &DockerBuilder{redactor: redact.New()}
	body, err := os.Open("testdata/buildkit-successful-build.ndjson")
	require.NoError(t, err)
	defer func() { _ = body.Close() }()

	lines, buildErr := b.streamBuildKit(body, nil)
	require.NoError(t, buildErr)
	require.NotEmpty(t, lines)

	log := strings.Join(lines, "\n")
	require.Contains(t, log, "hello-from-buildkit", "step output has to survive")
	require.Contains(t, log, "DONE", "a finished step has to say so")
	require.Regexp(t, `#\d+ `, log, "every line is attributed to a step")
}

// Progress reaches the caller as it happens, not at the end.
func TestStreamBuildKit_ReportsProgressAsItArrives(t *testing.T) {
	b := &DockerBuilder{redactor: redact.New()}
	body, err := os.Open("testdata/buildkit-successful-build.ndjson")
	require.NoError(t, err)
	defer func() { _ = body.Close() }()

	var seen []string
	lines, _ := b.streamBuildKit(body, func(l string) { seen = append(seen, l) })
	require.Equal(t, lines, seen, "every logged line was also reported live")
}

// Redaction happens at the writer, because a build log is the single most
// likely place for a credential to appear.
func TestStreamBuildKit_RedactsBeforeAnythingIsKept(t *testing.T) {
	r := redact.New()
	r.Register("npm_supersecrettoken")
	b := &DockerBuilder{redactor: r}

	body := strings.NewReader(buildKitTrace(t,
		vertexMessage("sha256:aa", "[2/2] RUN npm ci"),
		logMessage("sha256:aa", "//registry.npmjs.org/:_authToken=npm_supersecrettoken\n"),
	))
	lines, _ := b.streamBuildKit(body, nil)
	log := strings.Join(lines, "\n")
	require.NotContains(t, log, "npm_supersecrettoken")
	require.Contains(t, log, "RUN npm ci")
}

// A step name is printed once, not on every update.
//
// BuildKit sends a vertex again for every state change. Reprinting the name
// each time turns a forty step build into a log that is mostly the same forty
// lines.
func TestBuildKitLog_NamesAStepOnce(t *testing.T) {
	l := newBuildKitLog()
	first := l.consume(decodeTrace(nil, vertexMessage("sha256:aa", "[1/2] FROM alpine")))
	second := l.consume(decodeTrace(nil, vertexMessage("sha256:aa", "[1/2] FROM alpine")))
	require.Len(t, first, 1)
	require.Contains(t, first[0], "[1/2] FROM alpine")
	require.Empty(t, second, "the same vertex reported twice printed its name twice")
}

// Malformed bytes end the walk instead of panicking or inventing fields.
//
// This parses bytes produced by another process. A decoder that panics fails
// the build over its own log, which is the opposite of what the log is for.
func TestForEachField_SurvivesAnythingItIsGiven(t *testing.T) {
	cases := map[string][]byte{
		"empty":              {},
		"truncated tag":      {0xff},
		"truncated length":   {0x0a, 0xff},
		"length past he end": {0x0a, 0x7f},
		"overlong varint":    {0x08, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f},
		"group start":        {0x0b, 0x01},
		"field zero":         {0x00, 0x01},
		"truncated 64 bit":   {0x09, 0x01, 0x02},
		"truncated 32 bit":   {0x0d, 0x01},
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			require.NotPanics(t, func() {
				forEachField(in, func(int, int, []byte, uint64) {})
				newBuildKitLog().consume(in)
			})
		})
	}
}

// And so does a real payload with a byte flipped in it, which is the shape a
// truncated write actually takes.
func TestBuildKitLog_SurvivesACorruptedRealPayload(t *testing.T) {
	body, err := os.ReadFile("testdata/buildkit-failing-build.ndjson")
	require.NoError(t, err)
	for cut := 1; cut < len(body); cut += 37 {
		require.NotPanics(t, func() {
			b := &DockerBuilder{redactor: redact.New()}
			_, _ = b.streamBuildKit(strings.NewReader(string(body[:cut])), nil)
		})
	}
}

// DOCKER_BUILDKIT=0 is the escape hatch every Docker user already knows, so it
// is the one this honours rather than inventing a second.
func TestWantsBuildKit_HonoursTheVariableDockerAlreadyDefines(t *testing.T) {
	for value, want := range map[string]bool{"0": false, "false": false, "FALSE": false} {
		got := wantsBuildKit(t.Context(), nil, func(string) string { return value })
		require.Equal(t, want, got, "DOCKER_BUILDKIT=%s", value)
	}
}

// Protobuf encoding, for tests only.
//
// Small enough to be obvious and independent of the decoder, so a bug in the
// decoder cannot hide behind a matching bug in the fixture builder.

func protoTag(field, wire int) []byte { return protoVarint(uint64(field)<<3 | uint64(wire)) }

func protoVarint(v uint64) []byte {
	var out []byte
	for v >= 0x80 {
		out = append(out, byte(v)|0x80)
		v >>= 7
	}
	return append(out, byte(v))
}

func protoBytes(field int, body []byte) []byte {
	out := protoTag(field, wireBytes)
	out = append(out, protoVarint(uint64(len(body)))...)
	return append(out, body...)
}

// vertexMessage builds a StatusResponse carrying one Vertex.
func vertexMessage(digest, name string) []byte {
	var v []byte
	v = append(v, protoBytes(vertexDigest, []byte(digest))...)
	v = append(v, protoBytes(vertexName, []byte(name))...)
	return protoBytes(statusVertexes, v)
}

// logMessage builds a StatusResponse carrying one VertexLog.
func logMessage(digest, msg string) []byte {
	var l []byte
	l = append(l, protoBytes(logVertex, []byte(digest))...)
	l = append(l, protoBytes(logMsg, []byte(msg))...)
	return protoBytes(statusLogs, l)
}

// decodeTrace concatenates payloads into one StatusResponse body.
func decodeTrace(_ *testing.T, payloads ...[]byte) []byte {
	var out []byte
	for _, p := range payloads {
		out = append(out, p...)
	}
	return out
}

// buildKitTrace renders payloads as the newline delimited documents the daemon
// sends.
func buildKitTrace(t *testing.T, payloads ...[]byte) string {
	t.Helper()
	var b strings.Builder
	for _, p := range payloads {
		line, err := json.Marshal(map[string]string{
			"id": traceID, "aux": base64.StdEncoding.EncodeToString(p),
		})
		require.NoError(t, err)
		b.Write(line)
		b.WriteString("\n")
	}
	return b.String()
}

// The last document of a successful build carries an object, not a string.
//
// `aux` is base64 text on a status update and `{"ID":"sha256:..."}` on the
// final `moby.image.id` document. Declaring it as a string makes the decoder
// fail on that last document and abandon the stream, so every successful build
// returns an error and a truncated log. Caught by decoding a captured stream
// rather than a synthesised one.
func TestStreamBuildKit_TheFinalImageDocumentIsNotAString(t *testing.T) {
	b := &DockerBuilder{redactor: redact.New()}
	body := strings.NewReader(
		buildKitTrace(t, vertexMessage("sha256:aa", "[1/1] FROM alpine")) +
			`{"aux":{"ID":"sha256:deeacb21"},"id":"moby.image.id"}` + "\n")

	lines, err := b.streamBuildKit(body, nil)
	require.NoError(t, err, "the final document must not be read as a failure")
	require.Contains(t, strings.Join(lines, "\n"), "FROM alpine")
}
