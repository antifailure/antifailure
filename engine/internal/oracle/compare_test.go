package oracle_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/oracle"
)

func TestMain(m *testing.M) {
	code := func() int {
		// The template databases are dropped before goleak looks, or the
		// admin connection this file holds open would be reported as a leak
		// the suite created rather than as one the package did.
		defer dropShared()
		return m.Run()
	}()
	if code == 0 {
		if err := goleak.Find(); err != nil {
			fmt.Fprintf(os.Stderr, "goroutines outlived the suite: %v\n", err)
			code = 1
		}
	}
	os.Exit(code)
}

// epoch is a fixed instant. Nothing in this package reads the clock for a
// comparison, and a clock that never moves is what proves it: if a duration
// ever reached a finding, every one of these tests would report it as equal
// and the assertion counts would not change, so the fake is a control rather
// than a convenience.
var epoch = time.Date(2026, 8, 30, 9, 0, 0, 0, time.UTC)

// Two real servers and a real HTTP round trip, not two hand written Response
// structs. The whole point of the normalisation layer is what a real server
// puts on the wire: a Date header, a Content-Length, an ETag, a Set-Cookie. A
// test that builds the Response by hand tests the differ and not the thing
// somebody will actually run.
func drive(t *testing.T, baseline, candidate http.Handler, probes []oracle.Probe, cfg oracle.Config) *oracle.Result {
	t.Helper()
	b := httptest.NewServer(baseline)
	t.Cleanup(b.Close)
	c := httptest.NewServer(candidate)
	t.Cleanup(c.Close)

	// The fake clock is the repository's rule and it is also load bearing
	// here: Response.DurationMs must never reach a finding, and a clock that
	// does not move proves it cannot.
	d := &oracle.Driver{Clock: clock.NewFake(epoch)}
	results := oracle.Drive(context.Background(), d, b.URL, c.URL, probes, nil)
	require.Len(t, results, len(probes))
	return oracle.Compare(oracle.Input{Config: cfg, Probes: results})
}

func json(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

func findingsOfKind(r *oracle.Result, kind oracle.Kind) []oracle.Finding {
	var out []oracle.Finding
	for _, f := range r.Findings {
		if f.Kind == kind {
			out = append(out, f)
		}
	}
	return out
}

var listProbe = []oracle.Probe{{Name: "list", Method: "GET", Path: "/orders"}}

// The change that does not matter. Every one of these is something a real
// server varies on every request, and every one of them would have produced a
// finding under a byte comparison.
func TestAChangeThatDoesNotMatterIsNotReported(t *testing.T) {
	baseline := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Set-Cookie", "session=aaaaaaaaaaaa; Path=/")
		w.Header().Set("ETag", `"v1-abc"`)
		w.Header().Set("X-Request-Id", "11111111-1111-4111-8111-111111111111")
		json(w, 200, `{"orders":[{"id":1,"total":25.99,
			"placed_at":"2026-08-30T09:00:00.123456Z",
			"trace":"3f8a1c2e-0000-4000-8000-aaaaaaaaaaaa"}]}`)
	})
	candidate := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Set-Cookie", "session=bbbbbbbbbbbb; Path=/")
		w.Header().Set("ETag", `"v1-def"`)
		w.Header().Set("X-Request-Id", "22222222-2222-4222-8222-222222222222")
		// The key order in the document is different, the timestamp has moved,
		// the request identifier is fresh, and the float has picked up
		// representation noise. None of it is a behaviour change.
		json(w, 200, `{"orders":[{"trace":"7c2b9d4f-1111-4111-9111-bbbbbbbbbbbb",
			"placed_at":"2026-08-30T09:00:04.998877Z",
			"total":25.990000000000002,"id":1}]}`)
	})

	res := drive(t, baseline, candidate, listProbe, oracle.Config{})
	require.Emptyf(t, res.Findings, "reported a difference that is not one: %+v", res.Findings)
	require.Equal(t, "identical", res.Verdict())

	// And it says what it absorbed rather than absorbing it silently.
	describe := res.Ignored.Describe()
	require.Contains(t, describe, "set-cookie")
	require.Contains(t, describe, "timestamp normaliser")
	require.Contains(t, describe, "uuid normaliser")
	require.Contains(t, describe, "number tolerance normaliser")
	require.Contains(t, describe, "$.orders[0].placed_at")
}

// The change that does matter, in the same shape, so the two tests differ only
// in what the candidate did.
func TestAChangeThatMattersIsReportedAndRanked(t *testing.T) {
	baseline := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json(w, 200, `{"orders":[{"id":1,"total_cents":2599,"customer_id":7}]}`)
	})
	candidate := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// total_cents became total, in dollars, and customer_id is gone. Every
		// client reading this response breaks.
		json(w, 200, `{"orders":[{"id":1,"total":25.99}]}`)
	})

	res := drive(t, baseline, candidate, listProbe, oracle.Config{})
	require.Equal(t, "differs", res.Verdict())

	missing := findingsOfKind(res, oracle.KindBodyMissing)
	require.Len(t, missing, 2)
	paths := []string{missing[0].Path, missing[1].Path}
	require.ElementsMatch(t,
		[]string{"$.orders[0].customer_id", "$.orders[0].total_cents"}, paths)
	for _, f := range missing {
		require.Equal(t, oracle.Major, f.Severity,
			"a field the baseline returned and the candidate does not is major")
	}

	extra := findingsOfKind(res, oracle.KindBodyExtra)
	require.Len(t, extra, 1)
	require.Equal(t, "$.orders[0].total", extra[0].Path)
	require.Equal(t, oracle.Minor, extra[0].Severity, "an added field is what a feature does")

	// Worst first, which is the only ordering that survives somebody reading
	// four lines and stopping.
	require.Equal(t, oracle.Major, res.Findings[0].Severity)
}

func TestAStatusThatFallsIntoAnErrorClassIsCritical(t *testing.T) {
	baseline := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json(w, 200, `{"ok":true}`)
	})
	candidate := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json(w, 500, `{"ok":true}`)
	})
	res := drive(t, baseline, candidate, listProbe, oracle.Config{})

	class := findingsOfKind(res, oracle.KindStatusClass)
	require.Len(t, class, 1)
	require.Equal(t, oracle.Critical, class[0].Severity)
	require.Equal(t, "500", class[0].Candidate)
	require.Equal(t, "regressed", res.Verdict())
	require.True(t, oracle.AtLeast(res.Findings, oracle.Critical))
}

// The direction matters. A candidate that fixed a 500 must not be reported as
// a regression, or the gate teaches people to ignore it.
func TestAStatusThatLeavesAnErrorClassIsMinor(t *testing.T) {
	baseline := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json(w, 500, `{"error":"boom"}`)
	})
	candidate := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json(w, 200, `{"error":"boom"}`)
	})
	res := drive(t, baseline, candidate, listProbe, oracle.Config{})

	class := findingsOfKind(res, oracle.KindStatusClass)
	require.Len(t, class, 1)
	require.Equal(t, oracle.Minor, class[0].Severity)
	require.False(t, oracle.AtLeast(res.Findings, oracle.Critical))
}

func TestASideThatDoesNotAnswerIsCritical(t *testing.T) {
	baseline := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json(w, 200, `{"ok":true}`)
	})
	// Closed before the probe runs, so the connection is refused rather than
	// slow. That is the shape of a candidate whose service crashed on start.
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()

	b := httptest.NewServer(baseline)
	t.Cleanup(b.Close)
	d := &oracle.Driver{Clock: clock.NewFake(epoch)}
	results := oracle.Drive(context.Background(), d, b.URL, deadURL, listProbe, nil)
	res := oracle.Compare(oracle.Input{Probes: results})

	transport := findingsOfKind(res, oracle.KindTransport)
	require.Len(t, transport, 1)
	require.Equal(t, oracle.Critical, transport[0].Severity)
	require.Contains(t, transport[0].Detail, "the baseline answered and the candidate did not")
}

// A timestamp against a value that is not one has to be reported. This is the
// rule that stops the normaliser from being a hole: it makes two values equal
// and never rewrites one side into something a third value could match.
func TestANormaliserOnlyFiresWhenBothSidesMatchIt(t *testing.T) {
	baseline := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json(w, 200, `{"placed_at":"2026-08-30T09:00:00Z"}`)
	})
	candidate := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json(w, 200, `{"placed_at":"pending"}`)
	})
	res := drive(t, baseline, candidate, listProbe, oracle.Config{})
	require.Len(t, res.Findings, 1)
	require.Equal(t, oracle.KindBodyValue, res.Findings[0].Kind)
	require.Equal(t, "$.placed_at", res.Findings[0].Path)
	require.Empty(t, res.Ignored.Normalisers, "no normaliser should have fired")
}

// Integer identifiers are compared exactly and that is the design. Both sides
// branch one golden and receive one request sequence, so a sequence that has
// diverged means the candidate wrote a row the baseline did not.
func TestIdentifiersAreComparedExactly(t *testing.T) {
	baseline := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json(w, 201, `{"id":41}`)
	})
	candidate := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json(w, 201, `{"id":42}`)
	})
	res := drive(t, baseline, candidate, listProbe, oracle.Config{})
	require.Len(t, res.Findings, 1)
	require.Equal(t, "$.id", res.Findings[0].Path)
	require.Equal(t, "41", res.Findings[0].Baseline)
	require.Equal(t, "42", res.Findings[0].Candidate)
}

// A numeric epoch is not guessed at, and the refusal comes with the line
// somebody would have to write. Advice, not behaviour.
func TestANumericClockIsComparedAndTheReportSaysHowToIgnoreIt(t *testing.T) {
	baseline := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json(w, 200, `{"expires_at":1756512345}`)
	})
	candidate := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json(w, 200, `{"expires_at":1756512399}`)
	})
	res := drive(t, baseline, candidate, listProbe, oracle.Config{})
	require.Len(t, res.Findings, 1)
	require.Contains(t, res.Findings[0].Detail, "$.expires_at")
	require.Contains(t, res.Findings[0].Detail, "never treated as clock readings")

	// And the line it printed actually works when pasted into the manifest.
	quiet := drive(t, baseline, candidate, listProbe,
		oracle.Config{IgnoreFields: []string{"$.expires_at"}})
	require.Empty(t, quiet.Findings)
	require.Contains(t, quiet.Ignored.Describe(), "$.expires_at")
}

// The normalisers can be turned off, and turning one off has to change the
// answer, or the manifest key is decoration.
func TestTurningOffANormaliserMakesTheDifferenceVisible(t *testing.T) {
	baseline := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json(w, 200, `{"created_at":"2026-08-30T09:00:00Z"}`)
	})
	candidate := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json(w, 200, `{"created_at":"2026-08-30T10:00:00Z"}`)
	})
	require.Empty(t, drive(t, baseline, candidate, listProbe, oracle.Config{}).Findings)

	strict := drive(t, baseline, candidate, listProbe, oracle.Config{KeepTimestamps: true})
	require.Len(t, strict.Findings, 1)
	require.Equal(t, "$.created_at", strict.Findings[0].Path)
}

// A reordered list is one sentence rather than a difference at every index.
func TestAReorderedArrayIsOneMinorFinding(t *testing.T) {
	baseline := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json(w, 200, `{"names":["ada","grace","alan"]}`)
	})
	candidate := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json(w, 200, `{"names":["alan","ada","grace"]}`)
	})
	res := drive(t, baseline, candidate, listProbe, oracle.Config{})
	require.Len(t, res.Findings, 1)
	require.Equal(t, oracle.KindBodyOrder, res.Findings[0].Kind)
	require.Equal(t, oracle.Minor, res.Findings[0].Severity)
}

// A shorter list is data the candidate stopped returning, which outranks a
// longer one.
func TestALostArrayElementOutranksAnAddedOne(t *testing.T) {
	three := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json(w, 200, `{"names":["ada","grace","alan"]}`)
	})
	two := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json(w, 200, `{"names":["ada","grace"]}`)
	})
	shorter := findingsOfKind(drive(t, three, two, listProbe, oracle.Config{}), oracle.KindBodyLength)
	require.Len(t, shorter, 1)
	require.Equal(t, oracle.Major, shorter[0].Severity)

	longer := findingsOfKind(drive(t, two, three, listProbe, oracle.Config{}), oracle.KindBodyLength)
	require.Len(t, longer, 1)
	require.Equal(t, oracle.Minor, longer[0].Severity)
}

// A probe plan of more than one request has to reach both sides in the same
// order, or a probe that depends on an earlier one compares two different
// states.
func TestEveryProbeReachesBothSidesInTheSameOrder(t *testing.T) {
	var seen []string
	record := func(side string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen = append(seen, side+" "+r.Method+" "+r.URL.Path)
			json(w, 200, `{}`)
		})
	}
	probes := []oracle.Probe{
		{Name: "one", Method: "GET", Path: "/a"},
		{Name: "two", Method: "POST", Path: "/b", Body: `{"x":1}`},
		{Name: "three", Method: "GET", Path: "/c"},
	}
	drive(t, record("baseline"), record("candidate"), probes, oracle.Config{})
	require.Equal(t, []string{
		"baseline GET /a", "candidate GET /a",
		"baseline POST /b", "candidate POST /b",
		"baseline GET /c", "candidate GET /c",
	}, seen)
}

// The request body reaches both sides byte for byte, which is what makes a
// POST probe a comparison rather than two unrelated writes.
func TestTheSameBodyReachesBothSides(t *testing.T) {
	bodies := map[string]string{}
	echo := func(side string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			buf := make([]byte, r.ContentLength)
			_, _ = r.Body.Read(buf)
			bodies[side] = string(buf)
			json(w, 201, `{}`)
		})
	}
	probes := []oracle.Probe{{
		Name: "place", Method: "POST", Path: "/orders",
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    `{"customer_id":1,"total_cents":2599}`,
	}}
	drive(t, echo("baseline"), echo("candidate"), probes, oracle.Config{})
	require.Equal(t, `{"customer_id":1,"total_cents":2599}`, bodies["baseline"])
	require.Equal(t, bodies["baseline"], bodies["candidate"])
}

func TestSeverityParsesTheWordsTheManifestAccepts(t *testing.T) {
	for word, want := range map[string]oracle.Severity{
		"none": 0, "": 0, "minor": oracle.Minor, "any": oracle.Minor,
		"major": oracle.Major, "critical": oracle.Critical, "CRITICAL": oracle.Critical,
	} {
		got, ok := oracle.ParseSeverity(word)
		require.Truef(t, ok, "%q", word)
		require.Equalf(t, want, got, "%q", word)
	}
	_, ok := oracle.ParseSeverity("severe")
	require.False(t, ok)

	// none must be a threshold nothing reaches, or "report and never fail" is
	// not expressible.
	require.False(t, oracle.AtLeast(
		[]oracle.Finding{{Severity: oracle.Critical}}, 0))
}

// The negative control. Every test above asserts that something is or is not
// reported, and a differ that reported nothing at all would pass half of them.
// This one proves the comparison can fail by handing it two documents that
// differ in every way it knows about, and requiring one finding of each kind.
func TestTheComparisonCanActuallyFail(t *testing.T) {
	baseline := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		json(w, 200, `{"kept":1,"gone":2,"typed":3,"list":[1,2]}`)
	})
	candidate := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "max-age=60")
		json(w, 201, `{"kept":9,"added":2,"typed":"3","list":[1]}`)
	})
	res := drive(t, baseline, candidate, listProbe, oracle.Config{})

	for _, kind := range []oracle.Kind{
		oracle.KindStatus, oracle.KindHeader, oracle.KindBodyValue,
		oracle.KindBodyMissing, oracle.KindBodyExtra, oracle.KindBodyType,
		oracle.KindBodyLength,
	} {
		require.NotEmptyf(t, findingsOfKind(res, kind), "no %s finding", kind)
	}
	// The headline is the only line most people read, so its counts have to be
	// the counts of the table under it.
	counts := oracle.Count(res.Findings)
	require.Equal(t,
		fmt.Sprintf("%d differences: %d major, %d minor.",
			len(res.Findings), counts[oracle.Major], counts[oracle.Minor]),
		res.Headline())
	require.Equal(t, len(res.Findings), counts[oracle.Major]+counts[oracle.Minor])
}

// The timestamp normaliser is a tolerance, not a hole. Two instants close
// enough to be the harness are equal; two that are far enough apart to be a
// decision are reported. Without the bound, a change that shifted every expiry
// by a day would have been absorbed silently, which is the failure this whole
// package exists to avoid.
func TestTheTimestampNormaliserIsBoundedAndSaysHowWideItWent(t *testing.T) {
	at := func(s string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json(w, 200, `{"expires_at":"`+s+`"}`)
		})
	}
	near := drive(t, at("2026-08-30T09:00:00Z"), at("2026-08-30T09:04:00Z"), listProbe, oracle.Config{})
	require.Empty(t, near.Findings)
	require.Len(t, near.Ignored.Normalisers, 1)
	require.Equal(t, "4m0s", near.Ignored.Normalisers[0].Widest,
		"the report has to say how large a gap it absorbed, or the tolerance is a claim")

	far := drive(t, at("2026-08-30T09:00:00Z"), at("2026-08-31T09:00:00Z"), listProbe, oracle.Config{})
	require.Len(t, far.Findings, 1)
	require.Equal(t, "$.expires_at", far.Findings[0].Path)
	require.Empty(t, far.Ignored.Normalisers, "nothing was absorbed, so nothing is reported as absorbed")

	// And the bound moves when the manifest moves it.
	wide := drive(t, at("2026-08-30T09:00:00Z"), at("2026-08-31T09:00:00Z"), listProbe,
		oracle.Config{TimestampSkew: 48 * time.Hour})
	require.Empty(t, wide.Findings)
}

// The reordering check is confirmed against the real comparison, not against
// the canonical form alone. Two lists whose timestamps all shifted by a day
// canonicalise identically, and reporting them as a reordering would swallow
// the shift.
func TestAShiftedListIsNotMistakenForAReordering(t *testing.T) {
	// Days apart, not hours: the skew makes two instants within an hour equal,
	// so a fixture inside the tolerance would prove nothing about the
	// reordering check.
	baseline := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json(w, 200, `{"at":["2026-08-01T09:00:00Z","2026-08-10T09:00:00Z"]}`)
	})
	candidate := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json(w, 200, `{"at":["2026-09-01T09:00:00Z","2026-09-10T09:00:00Z"]}`)
	})
	res := drive(t, baseline, candidate, listProbe, oracle.Config{})
	require.Empty(t, findingsOfKind(res, oracle.KindBodyOrder))
	require.Len(t, findingsOfKind(res, oracle.KindBodyValue), 2)

	// A genuine reordering of the same instants is still one finding.
	swapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json(w, 200, `{"at":["2026-08-10T09:00:00Z","2026-08-01T09:00:00Z"]}`)
	})
	genuine := drive(t, baseline, swapped, listProbe, oracle.Config{})
	require.Len(t, findingsOfKind(genuine, oracle.KindBodyOrder), 1)
}

// A one element array cannot be reordered, and calling it one would be a way
// to report "the same element in a different order" about an element that
// changed.
func TestAOneElementArrayIsNeverAReordering(t *testing.T) {
	baseline := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json(w, 200, `{"at":["2026-08-30T09:00:00Z"]}`)
	})
	candidate := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json(w, 200, `{"at":["2027-08-30T09:00:00Z"]}`)
	})
	res := drive(t, baseline, candidate, listProbe, oracle.Config{})
	require.Len(t, res.Findings, 1)
	require.Equal(t, oracle.KindBodyValue, res.Findings[0].Kind)
	require.Equal(t, "$.at[0]", res.Findings[0].Path)
}
