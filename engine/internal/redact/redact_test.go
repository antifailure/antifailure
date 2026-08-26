package redact_test

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
	"pgregory.net/rapid"

	"github.com/antifailure/antifailure/engine/internal/redact"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func TestRedactor_SecretCorpus_EveryFormatIsRedacted(t *testing.T) {
	t.Parallel()
	r := redact.New()
	corpus := secretCorpus()
	require.GreaterOrEqual(t, len(corpus), 300, "the secret corpus must be at least 300 entries")

	for _, c := range corpus {
		line := "level=info msg=\"calling provider\" detail=" + c.Value + " status=200"
		got := r.String(line)
		require.NotContains(t, got, c.Value,
			"format %s survived redaction:\n  in:  %s\n  out: %s", c.Name, line, got)
		require.Contains(t, got, redact.Marker, "format %s produced no marker", c.Name)
	}
}

func TestRedactor_BenignCorpus_NothingIsRedacted(t *testing.T) {
	t.Parallel()
	r := redact.New()
	corpus := benignCorpus()
	require.GreaterOrEqual(t, len(corpus), 300, "the benign corpus must be at least 300 entries")

	falsePositives := 0
	var examples []string
	for _, c := range corpus {
		got := r.String(c.Value)
		if got != c.Value {
			falsePositives++
			if len(examples) < 10 {
				examples = append(examples, c.Name+": "+c.Value+" -> "+got)
			}
		}
	}
	require.Zero(t, falsePositives,
		"%d of %d benign strings were redacted:\n%s",
		falsePositives, len(corpus), strings.Join(examples, "\n"))
}

func TestRedactor_ConnectionStringKeepsTheReadablePart(t *testing.T) {
	t.Parallel()
	r := redact.New()
	got := r.String("postgres://app_user:hunter2isnotgoodenough@db.internal:5432/production?sslmode=require")
	require.NotContains(t, got, "hunter2isnotgoodenough")
	// An operator needs the host and database to diagnose. Losing them to
	// redaction would make the control useless in practice.
	require.Contains(t, got, "db.internal:5432/production")
	require.Contains(t, got, "app_user")
	require.Contains(t, got, "sslmode=require")
}

func TestRedactor_AuthorizationHeaderKeepsTheScheme(t *testing.T) {
	t.Parallel()
	r := redact.New()
	got := r.String("authorization: Bearer abcdefghijklmnopqrstuvwxyz012345")
	require.NotContains(t, got, "abcdefghijklmnopqrstuvwxyz012345")
	require.Contains(t, strings.ToLower(got), "bearer")
}

func TestRedactor_RegisterReplacesAnExactValueInEveryEncoding(t *testing.T) {
	t.Parallel()
	r := redact.New()
	// A value with no recognisable shape. Only exact registration can catch it.
	const secret = "correct-horse-battery-staple-9917"
	require.True(t, r.Register(secret))

	cases := map[string]string{
		"plain":           secret,
		"base64":          "Y29ycmVjdC1ob3JzZS1iYXR0ZXJ5LXN0YXBsZS05OTE3",
		"base64 raw":      "Y29ycmVjdC1ob3JzZS1iYXR0ZXJ5LXN0YXBsZS05OTE3",
		"percent encoded": "correct-horse-battery-staple-9917",
		"inside json":     `{"value":"` + secret + `"}`,
		"inside a url":    "https://x.test/cb?state=" + secret,
		"repeated":        secret + " and again " + secret,
	}
	for name, in := range cases {
		got := r.String(in)
		require.NotContains(t, got, secret, "case %s leaked", name)
	}
}

func TestRedactor_RegisterRefusesShortValues(t *testing.T) {
	t.Parallel()
	r := redact.New()
	require.False(t, r.Register("short"), "a short value must not be registered")
	require.False(t, r.Register("elevenchar"), "eleven characters is still too short")
	require.True(t, r.Register("twelvechars0"), "twelve characters is the threshold")
	// A refused registration must leave ordinary text alone.
	require.Equal(t, "the short answer", r.String("the short answer"))
}

func TestRedactor_RegisterIsIdempotentAndCounted(t *testing.T) {
	t.Parallel()
	r := redact.New()
	require.True(t, r.Register("a-registered-value-here"))
	first := r.RegisteredCount()
	require.True(t, r.Register("a-registered-value-here"))
	require.Equal(t, first, r.RegisteredCount(), "re-registering must not grow the set")
	require.Greater(t, first, 1, "each value registers several encodings")
}

func TestRedactor_LongerRegisteredValueWinsOverAShorterPrefix(t *testing.T) {
	t.Parallel()
	r := redact.New()
	require.True(t, r.Register("prefix-secret-value"))
	require.True(t, r.Register("prefix-secret-value-with-more"))
	got := r.String("token=prefix-secret-value-with-more")
	require.NotContains(t, got, "with-more",
		"the longer secret must be replaced whole, not leave a tail")
}

func TestRedactor_IsIdempotent(t *testing.T) {
	t.Parallel()
	r := redact.New()
	require.True(t, r.Register("registered-secret-value-x"))
	rapid.Check(t, func(rt *rapid.T) {
		s := rapid.String().Draw(rt, "s")
		once := r.String(s)
		twice := r.String(once)
		if once != twice {
			rt.Fatalf("redaction is not idempotent:\n  once:  %q\n  twice: %q", once, twice)
		}
	})
}

func TestRedactor_NeverGrowsOutputUnboundedly(t *testing.T) {
	t.Parallel()
	r := redact.New()
	rapid.Check(t, func(rt *rapid.T) {
		s := rapid.StringN(0, 512, -1).Draw(rt, "s")
		out := r.String(s)
		// Each replacement can only shorten or lengthen by the marker's size,
		// and there can be at most one replacement per input rune.
		max := len(s)*len(redact.Marker) + len(redact.Marker)
		if len(out) > max {
			rt.Fatalf("output grew from %d to %d bytes", len(s), len(out))
		}
	})
}

func TestRedactor_TruncatesAPathologicallyLongLine(t *testing.T) {
	t.Parallel()
	r := redact.New()
	huge := strings.Repeat("a", 2<<20)
	got := r.String(huge)
	require.Less(t, len(got), len(huge))
	require.True(t, strings.HasSuffix(got, "[truncated]"))
}

func TestRedactor_BytesDoesNotModifyTheInput(t *testing.T) {
	t.Parallel()
	r := redact.New()
	in := []byte(fakeKey(stripeSecretLive, 24))
	cp := append([]byte(nil), in...)
	out := r.Bytes(in)
	require.Equal(t, cp, in, "Bytes must not write through its argument")
	require.NotContains(t, string(out), stripeSecretLive)
}

func TestWriter_RedactsPerLine(t *testing.T) {
	t.Parallel()
	r := redact.New()
	var buf bytes.Buffer
	w := r.Writer(&buf)

	_, err := io.WriteString(w, "first line ok\n"+fakeKey(stripeSecretLive, 24)+" here\nlast")
	require.NoError(t, err)
	require.NoError(t, w.Close())

	out := buf.String()
	require.Contains(t, out, "first line ok")
	require.NotContains(t, out, fakeKey(stripeSecretLive, 24))
	require.Contains(t, out, "last", "Close must flush the unterminated tail")
}

func TestWriter_RedactsASecretSplitAcrossWrites(t *testing.T) {
	t.Parallel()
	r := redact.New()
	var buf bytes.Buffer
	w := r.Writer(&buf)

	// The single most likely way a secret escapes a naive line redactor: an
	// io.Writer hands it over in two calls.
	_, err := io.WriteString(w, "token="+fakeKey(stripeSecretLive, 24)[:14])
	require.NoError(t, err)
	_, err = io.WriteString(w, fakeKey(stripeSecretLive, 24)[14:]+"\n")
	require.NoError(t, err)
	require.NoError(t, w.Close())

	require.NotContains(t, buf.String(), fakeKey(stripeSecretLive, 24))
}

func TestWriter_FlushesWhenTheCarryGrowsWithoutANewline(t *testing.T) {
	t.Parallel()
	r := redact.New()
	var buf bytes.Buffer
	w := r.Writer(&buf)
	// Ten thousand bytes with no newline must not be held in memory forever.
	_, err := io.WriteString(w, strings.Repeat("x", 10000))
	require.NoError(t, err)
	require.Positive(t, buf.Len(), "the writer must flush past the carry limit")
	require.NoError(t, w.Close())
	require.Equal(t, 10000, buf.Len())
}

func TestWriter_PropagatesWriteErrors(t *testing.T) {
	t.Parallel()
	r := redact.New()
	w := r.Writer(errWriter{})
	_, err := io.WriteString(w, "a line\n")
	require.Error(t, err)

	w2 := r.Writer(errWriter{})
	_, err = io.WriteString(w2, "no newline")
	require.NoError(t, err)
	require.Error(t, w2.Close(), "Close must surface the flush error")

	w3 := r.Writer(errWriter{})
	_, err = io.WriteString(w3, strings.Repeat("y", 10000))
	require.Error(t, err, "the carry flush must surface its error")
}

func TestWriter_CloseOnAnEmptyWriterIsANoOp(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	w := redact.New().Writer(&buf)
	require.NoError(t, w.Close())
	require.Zero(t, buf.Len())
}

func TestStream_RedactsLineByLine(t *testing.T) {
	t.Parallel()
	r := redact.New()
	var out bytes.Buffer
	in := strings.NewReader("ok\n" + fakeKey(githubClassic, 36) + "\nfine\n")
	require.NoError(t, r.Stream(&out, in))
	require.NotContains(t, out.String(), githubClassic)
	require.Equal(t, 3, strings.Count(out.String(), "\n"))
}

func TestStream_SurfacesAWriteError(t *testing.T) {
	t.Parallel()
	err := redact.New().Stream(errWriter{}, strings.NewReader("line\n"))
	require.Error(t, err)
}

func TestStream_SurfacesAnErrorWritingTheLineTerminator(t *testing.T) {
	t.Parallel()
	// The line body writes, the newline that follows does not. Without a check
	// on the second write the loop would silently drop the failure.
	w := &failAfterWriter{ok: 1}
	err := redact.New().Stream(w, strings.NewReader("line\n"))
	require.Error(t, err)
}

func TestStream_SurfacesAReadError(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	err := redact.New().Stream(&out, errReader{})
	require.Error(t, err)
}

func TestRedactor_ConcurrentRegisterAndRedactAreSafe(t *testing.T) {
	t.Parallel()
	r := redact.New()
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			r.Register("concurrent-secret-value-" + strings.Repeat("z", i))
		}(i)
		go func() {
			defer wg.Done()
			_ = r.String("some line with " + fakeKey(stripeSecretLive, 24) + " in it")
		}()
	}
	wg.Wait()
}

func TestRedactor_GroupRuleWithAnUnmatchedGroupIsSkipped(t *testing.T) {
	t.Parallel()
	// A rule whose group never participates must leave the input untouched
	// rather than panic on a negative index.
	r := redact.NewWithRules([]redact.Rule{{
		Name:    "optional-group",
		Group:   1,
		Pattern: mustCompile(`prefix(?:-(x))?`),
	}})
	require.Equal(t, "prefix here", r.String("prefix here"))
	require.Equal(t, "prefix-[redacted] here", r.String("prefix-x here"))
}

func TestRedactor_GroupIndexBeyondTheRuleIsSkipped(t *testing.T) {
	t.Parallel()
	r := redact.NewWithRules([]redact.Rule{{
		Name:    "no-such-group",
		Group:   5,
		Pattern: mustCompile(`token=(\w+)`),
	}})
	require.Equal(t, "token=abc", r.String("token=abc"))
}

func TestRedactor_RuleWithNoMatchLeavesInputAlone(t *testing.T) {
	t.Parallel()
	r := redact.NewWithRules([]redact.Rule{{
		Name:    "never",
		Group:   1,
		Pattern: mustCompile(`zzz(q)`),
	}})
	require.Equal(t, "nothing here", r.String("nothing here"))
}

type errWriter struct{}

func (errWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

// failAfterWriter succeeds for ok writes and then fails.
type failAfterWriter struct {
	ok int
	n  int
}

func (w *failAfterWriter) Write(p []byte) (int, error) {
	w.n++
	if w.n > w.ok {
		return 0, io.ErrClosedPipe
	}
	return len(p), nil
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestRedactor_RepeatsUntilTheLineStopsChanging(t *testing.T) {
	t.Parallel()
	r := redact.New()
	// The URL password rule replaces the password, which shortens the line and
	// brings the bearer token into a position an earlier pass had not reached.
	// Both must end up redacted.
	line := `msg="retry" url=postgres://svc:pa55word-not-real@db:5432/app ` +
		`hdr="authorization: Bearer abcdefghijklmnopqrstuvwxyz0123456789"`
	got := r.String(line)
	require.NotContains(t, got, "pa55word-not-real")
	require.NotContains(t, got, "abcdefghijklmnopqrstuvwxyz0123456789")
}

func TestRedactor_ConvergesOnARuleWhoseMarkerRetriggersIt(t *testing.T) {
	t.Parallel()
	// A deliberately pathological rule: it matches the marker itself, so
	// without the pass cap it would rewrite forever.
	r := redact.NewWithRules([]redact.Rule{{
		Name:    "self-triggering",
		Require: []string{"xx"},
		Pattern: mustCompile(`x+`),
	}})
	done := make(chan string, 1)
	go func() { done <- r.String("xxx and more xxx") }()
	select {
	case got := <-done:
		require.NotContains(t, got, "xxx")
	case <-time.After(5 * time.Second):
		t.Fatal("redaction did not terminate")
	}
}

func TestMatcher_FindsLiteralsRegardlessOfCaseWhenFolding(t *testing.T) {
	t.Parallel()
	r := redact.New()
	// AWS key identifiers are uppercase; the prefilter literal is lowercase.
	// A folding bug here silently disables the whole rule.
	got := r.String("id=" + awsKey + " region=us-east-1")
	require.NotContains(t, got, awsKey)
	require.Contains(t, got, "region=us-east-1")
}

func TestRedactor_ExactMatchIsCaseSensitive(t *testing.T) {
	t.Parallel()
	r := redact.New()
	require.True(t, r.Register("CaseSensitiveSecret1"))
	require.NotContains(t, r.String("v=CaseSensitiveSecret1"), "CaseSensitiveSecret1")
	// A different case is a different value and must not be treated as the
	// secret, or every case variant of a common word would be redacted.
	require.Contains(t, r.String("v=casesensitivesecret1"), "casesensitivesecret1")
}

func TestRedactor_ShortInputsAreHandled(t *testing.T) {
	t.Parallel()
	r := redact.New()
	require.True(t, r.Register("registered-value-here"))
	for _, s := range []string{"", "a", "ab", "abc"} {
		require.Equal(t, s, r.String(s))
	}
}
