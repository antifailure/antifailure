package review

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/change"
	"github.com/antifailure/antifailure/engine/internal/model"
	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/internal/secrets"
)

// fakeClient stands in for the model: it returns a canned completion and records
// what it was asked. It is the whole reason the package is testable without a
// network, the same way a fake reader lets the collector be tested without an
// environment.
type fakeClient struct {
	reply     string
	err       error
	called    bool
	gotSystem string
	gotUser   string
}

func (c *fakeClient) Complete(_ context.Context, system, user string) (string, error) {
	c.called = true
	c.gotSystem, c.gotUser = system, user
	return c.reply, c.err
}

// codeFile is one changed file with the added lines a review reads.
func codeFile(path string, lines ...change.AddedLine) change.File {
	return change.File{Path: path, Status: change.StatusModified, Added: len(lines), AddedLines: lines}
}

func TestReview_MapsAModelReviewIntoFindings(t *testing.T) {
	t.Parallel()
	client := &fakeClient{reply: `[
		{"category":"correctness","severity":"high","file":"app/pay.go","line":42,
		 "title":"off by one on the last item","explanation":"the loop stops one short",
		 "suggested_fix":"use <= len"},
		{"category":"error_handling","severity":"medium","file":"app/pay.go","line":10,
		 "title":"error is dropped","explanation":"the returned err is ignored",
		 "suggested_fix":"return the error"}
	]`}

	res, err := Review(context.Background(), client,
		[]change.File{codeFile("app/pay.go", change.AddedLine{N: 42, Text: "for i < len(xs)"})},
		report.LevelWarn, DefaultCaps)

	require.NoError(t, err)
	require.Len(t, res.Findings, 2)
	require.True(t, client.called, "the model was asked to review")
	require.Contains(t, client.gotUser, "app/pay.go", "the diff reached the model")

	first := res.Findings[0]
	require.Equal(t, "review.correctness", first.Rule)
	require.Equal(t, report.LevelWarn, first.Level, "the finding carries the level the caller passed, not one this package chose")
	require.Equal(t, "app/pay.go:42", first.Where)
	require.Equal(t, "off by one on the last item", first.Title)
	require.Equal(t, "the loop stops one short", first.Detail)
	require.Equal(t, "use <= len", first.Fix)
	require.Equal(t, 1, first.Count)
	require.Equal(t, "review.error_handling", res.Findings[1].Rule)
}

func TestReview_TheLevelIsWhateverTheCallerPassed(t *testing.T) {
	t.Parallel()
	client := &fakeClient{reply: `[{"category":"correctness","file":"a.go","line":1,"title":"x"}]`}

	res, err := Review(context.Background(), client,
		[]change.File{codeFile("a.go", change.AddedLine{N: 1, Text: "x"})},
		report.LevelFail, DefaultCaps)
	require.NoError(t, err)
	require.Len(t, res.Findings, 1)
	require.Equal(t, report.LevelFail, res.Findings[0].Level,
		"a project that raised the level to fail gets fail, so the level must come from the parameter")
}

func TestReview_EmptyReviewIsNoFindingAndNoError(t *testing.T) {
	t.Parallel()
	client := &fakeClient{reply: `[]`}
	res, err := Review(context.Background(), client,
		[]change.File{codeFile("a.go", change.AddedLine{N: 1, Text: "ok"})},
		report.LevelWarn, DefaultCaps)
	require.NoError(t, err)
	require.Empty(t, res.Findings, "an empty array is the model looking and finding nothing")
	require.Empty(t, res.Notes, "an empty review is not a note; it is the common clean outcome")
}

func TestReview_AChangeWithNoAddedLinesMakesNoModelCall(t *testing.T) {
	t.Parallel()
	client := &fakeClient{reply: `[{"category":"correctness","file":"a.go","line":1,"title":"x"}]`}
	// A binary file and a file with no added lines: nothing for a line reviewer.
	res, err := Review(context.Background(), client,
		[]change.File{{Path: "logo.png", Binary: true}, {Path: "a.go"}},
		report.LevelWarn, DefaultCaps)
	require.NoError(t, err)
	require.False(t, client.called, "with nothing to review the model is never called, so nothing is spent")
	require.Empty(t, res.Findings)
}

func TestReview_AModelErrorIsReturnedNotSwallowed(t *testing.T) {
	t.Parallel()
	client := &fakeClient{err: context.DeadlineExceeded}
	res, err := Review(context.Background(), client,
		[]change.File{codeFile("a.go", change.AddedLine{N: 1, Text: "x"})},
		report.LevelWarn, DefaultCaps)
	require.Error(t, err, "a call that failed is an error the collector turns into a note, never a clean pass")
	require.Empty(t, res.Findings)
}

func TestReview_AnUnreadableResponseIsANoteNotAFinding(t *testing.T) {
	t.Parallel()
	client := &fakeClient{reply: "I could not review this change, sorry."}
	res, err := Review(context.Background(), client,
		[]change.File{codeFile("a.go", change.AddedLine{N: 1, Text: "x"})},
		report.LevelWarn, DefaultCaps)
	require.NoError(t, err, "an unreadable answer is not an error; it is a note")
	require.Empty(t, res.Findings, "nothing readable came back, so no finding is invented")
	require.Len(t, res.Notes, 1)
	require.Contains(t, res.Notes[0], "not the expected JSON")
}

func TestReview_OneMalformedEntryDoesNotBlankTheRest(t *testing.T) {
	t.Parallel()
	// One entry has no title and one has no file: both are dropped. The good one
	// stands. A single bad element must never zero out the collection.
	client := &fakeClient{reply: `[
		{"category":"correctness","file":"a.go","line":1,"title":""},
		{"category":"correctness","line":2,"title":"has no file"},
		{"category":"correctness","file":"a.go","line":3,"title":"a real defect"}
	]`}
	res, err := Review(context.Background(), client,
		[]change.File{codeFile("a.go", change.AddedLine{N: 3, Text: "x"})},
		report.LevelWarn, DefaultCaps)
	require.NoError(t, err)
	require.Len(t, res.Findings, 1, "the two malformed entries are dropped and the good one survives")
	require.Equal(t, "a real defect", res.Findings[0].Title)
}

func TestReview_AnUnknownCategoryBecomesCorrectness(t *testing.T) {
	t.Parallel()
	client := &fakeClient{reply: `[{"category":"vibes","file":"a.go","line":1,"title":"weird"}]`}
	res, err := Review(context.Background(), client,
		[]change.File{codeFile("a.go", change.AddedLine{N: 1, Text: "x"})},
		report.LevelWarn, DefaultCaps)
	require.NoError(t, err)
	require.Len(t, res.Findings, 1)
	require.Equal(t, "review.correctness", res.Findings[0].Rule,
		"an odd category is normalised to correctness, not dropped, because a real defect with a bad label is still a defect")
}

func TestReview_DuplicateFindingsAreCollapsed(t *testing.T) {
	t.Parallel()
	client := &fakeClient{reply: `[
		{"category":"correctness","file":"a.go","line":1,"title":"same"},
		{"category":"correctness","file":"a.go","line":1,"title":"Same"}
	]`}
	res, err := Review(context.Background(), client,
		[]change.File{codeFile("a.go", change.AddedLine{N: 1, Text: "x"})},
		report.LevelWarn, DefaultCaps)
	require.NoError(t, err)
	require.Len(t, res.Findings, 1, "the same defect reported twice, differing only in case, is one finding")
}

func TestReview_ALinelessFindingKeepsTheFileInWhere(t *testing.T) {
	t.Parallel()
	client := &fakeClient{reply: `[{"category":"dead_code","file":"a.go","title":"unused func"}]`}
	res, err := Review(context.Background(), client,
		[]change.File{codeFile("a.go", change.AddedLine{N: 1, Text: "x"})},
		report.LevelWarn, DefaultCaps)
	require.NoError(t, err)
	require.Len(t, res.Findings, 1)
	require.Equal(t, "a.go", res.Findings[0].Where, "a missing line drops from Where but keeps the file, rather than dropping the finding")
}

func TestParseEntries_FindsTheArrayInsideProseAndFences(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"bare":         `[{"file":"a","title":"t"}]`,
		"prose around": "Here is my review:\n[{\"file\":\"a\",\"title\":\"t\"}]\nThat is all.",
		"code fence":   "```json\n[{\"file\":\"a\",\"title\":\"t\"}]\n```",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			entries, ok := parseEntries(in)
			require.True(t, ok, "the array is located despite the packaging")
			require.Len(t, entries, 1)
			require.Equal(t, "a", entries[0].File)
		})
	}
}

func TestParseEntries_AnEmptyArrayIsReadableAndEmpty(t *testing.T) {
	t.Parallel()
	entries, ok := parseEntries("[]")
	require.True(t, ok, "an empty array is a readable answer, not an unreadable one")
	require.Empty(t, entries)
}

func TestParseEntries_NoArrayIsUnreadable(t *testing.T) {
	t.Parallel()
	_, ok := parseEntries("I refuse to answer in JSON.")
	require.False(t, ok, "a response with no array at all is unreadable")
}

func TestFlexInt_ReadsANumberOrAQuotedNumberOrNeither(t *testing.T) {
	t.Parallel()
	var f struct {
		Line flexInt `json:"line"`
	}
	require.NoError(t, json.Unmarshal([]byte(`{"line":12}`), &f))
	require.Equal(t, flexInt(12), f.Line, "a bare number reads")

	require.NoError(t, json.Unmarshal([]byte(`{"line":"34"}`), &f))
	require.Equal(t, flexInt(34), f.Line, "a quoted number reads")

	require.NoError(t, json.Unmarshal([]byte(`{"line":"nope"}`), &f))
	require.Equal(t, flexInt(0), f.Line, "a non-numeric string is zero, not a decode failure that would drop the whole entry")
}

func TestBoundDiff_ReviewsTheHighestSignalFilesFirstAndNotesTheCap(t *testing.T) {
	t.Parallel()
	// Three files, a cap of one file. The file with the most added lines is the
	// one reviewed, and the cap is noted so a reader knows the rest were not.
	small := codeFile("small.go", change.AddedLine{N: 1, Text: "a"})
	big := codeFile("big.go",
		change.AddedLine{N: 1, Text: "a"}, change.AddedLine{N: 2, Text: "b"}, change.AddedLine{N: 3, Text: "c"})
	mid := codeFile("mid.go", change.AddedLine{N: 1, Text: "a"}, change.AddedLine{N: 2, Text: "b"})

	selected, notes := boundDiff([]change.File{small, big, mid}, Caps{MaxFiles: 1})
	require.Len(t, selected, 1)
	require.Equal(t, "big.go", selected[0].Path, "the file with the most added lines is reviewed first")
	require.Len(t, notes, 1, "dropping files is noted, never silent")
	require.Contains(t, notes[0], "highest-signal")
}

func TestBoundDiff_TheLineBudgetIsRespected(t *testing.T) {
	t.Parallel()
	big := codeFile("big.go",
		change.AddedLine{N: 1, Text: "a"}, change.AddedLine{N: 2, Text: "b"}, change.AddedLine{N: 3, Text: "c"})
	small := codeFile("small.go", change.AddedLine{N: 1, Text: "a"})
	// A two-line budget: big.go (three lines) overflows and is skipped, small.go
	// fits. The whole-file rule means big is dropped rather than cut mid-file.
	selected, notes := boundDiff([]change.File{big, small}, Caps{MaxLines: 2})
	require.Len(t, selected, 1)
	require.Equal(t, "small.go", selected[0].Path, "the file that fits the line budget is reviewed, the one that overflows is dropped whole")
	require.NotEmpty(t, notes)
}

// TestProviderClient_ReviewsThroughTheRealClient is the end to end proof: the
// real ProviderClient, built exactly as the collector builds it, posts a review
// to an httptest server speaking the provider's own shape and the parser turns
// the answer into a finding. It exercises the actual guarded HTTP path, the
// request body, the response extraction and the mapping, with no fake anywhere
// but the provider itself.
func TestProviderClient_ReviewsThroughTheRealClient(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/messages", r.URL.Path)
		require.Equal(t, "sk-ant-secret", r.Header.Get("x-api-key"), "the key is on the request")
		body, _ := json.Marshal(map[string]any{
			"content": []map[string]string{
				{"type": "text", "text": `[{"category":"correctness","file":"a.go","line":5,"title":"real defect","explanation":"why","suggested_fix":"fix"}]`},
			},
		})
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	p, _ := model.Lookup("anthropic")
	cfg := model.Config{Provider: p, Key: secrets.New("sk-ant-secret"), Model: "claude", BaseURL: srv.URL}
	client := NewProviderClient(cfg)

	res, err := Review(context.Background(), client,
		[]change.File{codeFile("a.go", change.AddedLine{N: 5, Text: "buggy"})},
		report.LevelWarn, DefaultCaps)
	require.NoError(t, err)
	require.Len(t, res.Findings, 1)
	require.Equal(t, "review.correctness", res.Findings[0].Rule)
	require.Equal(t, "a.go:5", res.Findings[0].Where)
}

func TestProviderClient_ReadsTheOpenAIShape(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/chat/completions", r.URL.Path)
		require.Equal(t, "Bearer sk-openai", r.Header.Get("authorization"))
		body, _ := json.Marshal(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"content": `[{"category":"edge_case","file":"b.go","line":2,"title":"boundary"}]`}},
			},
		})
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	p, _ := model.Lookup("openai")
	cfg := model.Config{Provider: p, Key: secrets.New("sk-openai"), Model: "gpt", BaseURL: srv.URL}
	res, err := Review(context.Background(), NewProviderClient(cfg),
		[]change.File{codeFile("b.go", change.AddedLine{N: 2, Text: "x"})},
		report.LevelWarn, DefaultCaps)
	require.NoError(t, err)
	require.Len(t, res.Findings, 1)
	require.Equal(t, "review.edge_case", res.Findings[0].Rule)
}

func TestProviderClient_ANon2xxIsAnErrorAndNeverEchoesTheKey(t *testing.T) {
	t.Parallel()
	const key = "sk-ant-supersecret-value"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		// A careless gateway echoing the key it was handed straight back.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]string{"message": "rejected " + r.Header.Get("x-api-key")},
		})
	}))
	t.Cleanup(srv.Close)

	p, _ := model.Lookup("anthropic")
	cfg := model.Config{Provider: p, Key: secrets.New(key), Model: "m", BaseURL: srv.URL}
	_, err := NewProviderClient(cfg).Complete(context.Background(), "sys", "user")
	require.Error(t, err)
	require.NotContains(t, err.Error(), key, "the key must never reach an error a person reads")
	require.Contains(t, err.Error(), "401")
}

func TestProviderClient_A200ThatIsNotACompletionIsUnreadable(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/html")
		_, _ = w.Write([]byte("<html>a proxy notice, not a completion</html>"))
	}))
	t.Cleanup(srv.Close)

	p, _ := model.Lookup("anthropic")
	cfg := model.Config{Provider: p, Key: secrets.New("sk"), Model: "m", BaseURL: srv.URL}
	_, err := NewProviderClient(cfg).Complete(context.Background(), "sys", "user")
	require.Error(t, err, "a 200 that is not the completion shape is unreadable, not a working call")
	require.Contains(t, strings.ToLower(err.Error()), "completion shape")
}
