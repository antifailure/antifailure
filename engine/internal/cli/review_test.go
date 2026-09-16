package cli

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/change"
	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// noRing models a machine with no credential store, so the only key a chain can
// find is the one the environment provides. It keeps reviewClient's test off the
// developer's real keychain.
type noRing struct{}

func (noRing) Get(string, string) (string, error) { return "", secrets.ErrKeyringUnavailable }
func (noRing) Set(string, string, string) error   { return secrets.ErrKeyringUnavailable }
func (noRing) Delete(string, string) error        { return secrets.ErrKeyringUnavailable }

// fakeReviewReader hands the reviewer a profile and code files without an
// environment, the same way fakeReader does for the security collector.
type fakeReviewReader struct {
	profile    *change.Profile
	changeErr  error
	codeFiles  []change.File
	codeErr    error
	changed    bool
	codeCalled bool
}

func (f *fakeReviewReader) Change(context.Context, env.ChangeOptions) (*change.Profile, error) {
	f.changed = true
	return f.profile, f.changeErr
}

func (f *fakeReviewReader) CodeFiles(context.Context, env.ChangeOptions) ([]change.File, error) {
	f.codeCalled = true
	return f.codeFiles, f.codeErr
}

// fakeReviewClient is a model the collector can drive without a network.
type fakeReviewClient struct {
	reply  string
	err    error
	called bool
}

func (c *fakeReviewClient) Complete(context.Context, string, string) (string, error) {
	c.called = true
	return c.reply, c.err
}

func docsProfile() *change.Profile {
	return &change.Profile{
		Files: 1,
		Facts: []change.Fact{
			{Path: "README.md", Surface: change.SurfaceDocs, Rule: "path.docs", Evidence: "it is documentation"},
		},
	}
}

func reviewCodeFiles() []change.File {
	return []change.File{{
		Path: "app/pay.go", Status: change.StatusModified, Added: 1,
		AddedLines: []change.AddedLine{{N: 42, Text: "for i < len(items)"}},
	}}
}

const oneReviewFinding = `[{"category":"correctness","file":"app/pay.go","line":42,` +
	`"title":"off by one","explanation":"stops one short","suggested_fix":"use <="}]`

func TestReviewFindings_EmitsFindingsForACodeChange(t *testing.T) {
	reader := &fakeReviewReader{profile: codeProfile(), codeFiles: reviewCodeFiles()}
	client := &fakeReviewClient{reply: oneReviewFinding}
	run := report.Run{}

	got := reviewFindings(context.Background(), testEnv(), reader, client,
		report.Configure(nil), &run, "")

	require.True(t, reader.changed, "the reviewer reads the change to decide whether to run")
	require.True(t, reader.codeCalled, "a code change makes the reviewer read the code diff")
	require.True(t, client.called, "the model is asked to review the code change")
	require.Len(t, got, 1)
	require.Equal(t, "review.correctness", got[0].Rule)
	require.Equal(t, report.LevelWarn, got[0].Level, "the finding defaults to warn, the advisory level for a probabilistic reviewer")
	require.Equal(t, "app/pay.go:42", got[0].Where)
}

func TestReviewFindings_TheLevelComesFromPolicyNotThisCollector(t *testing.T) {
	reader := &fakeReviewReader{profile: codeProfile(), codeFiles: reviewCodeFiles()}
	client := &fakeReviewClient{reply: oneReviewFinding}
	run := report.Run{}
	// A project that raised the reviewer to fail gets fail, which proves the
	// level is read from the gate and never hard-coded in the collector.
	gate := report.Configure(&schema.Policy{Review: schema.PolicyFail})

	got := reviewFindings(context.Background(), testEnv(), reader, client, gate, &run, "")

	require.Len(t, got, 1)
	require.Equal(t, report.LevelFail, got[0].Level,
		"the manifest raised review to fail, so the finding is a fail")
}

func TestReviewFindings_ADocsOnlyChangeRunsNothingAndPaysNothing(t *testing.T) {
	reader := &fakeReviewReader{profile: docsProfile(), codeFiles: reviewCodeFiles()}
	client := &fakeReviewClient{reply: oneReviewFinding}
	run := report.Run{}

	got := reviewFindings(context.Background(), testEnv(), reader, client,
		report.Configure(nil), &run, "")

	require.Nil(t, got, "a docs-only change touches no code surface, so no review runs")
	require.False(t, reader.codeCalled, "a docs-only change never reads the code diff")
	require.False(t, client.called, "a docs-only change never calls the model")
	require.Empty(t, run.Notes, "nothing happened, so nothing is noted")
}

func TestReviewFindings_NoModelKeyIsAnHonestSkipNote(t *testing.T) {
	reader := &fakeReviewReader{profile: codeProfile(), codeFiles: reviewCodeFiles()}
	run := report.Run{}

	// A nil client is the collector's no-key signal.
	got := reviewFindings(context.Background(), testEnv(), reader, nil,
		report.Configure(nil), &run, "")

	require.Nil(t, got, "with no model to call the reviewer produces no finding")
	require.Len(t, run.Notes, 1, "the skip is named, so 'no key' is never a silent clean pass")
	require.Contains(t, run.Notes[0], "code review skipped: no model key configured")
	require.False(t, reader.codeCalled, "the skip is decided before the code diff is read")
}

func TestReviewFindings_AChangeThatCannotBeReadIsANoteNotAFailure(t *testing.T) {
	reader := &fakeReviewReader{changeErr: errors.New("no base ref")}
	client := &fakeReviewClient{reply: oneReviewFinding}
	run := report.Run{}

	got := reviewFindings(context.Background(), testEnv(), reader, client,
		report.Configure(nil), &run, "")

	require.Nil(t, got, "a diff we could not read is not evidence about the change")
	require.Len(t, run.Notes, 1)
	require.Contains(t, run.Notes[0], "could not read the change")
	require.False(t, client.called, "no model runs against a change we could not classify")
}

func TestReviewFindings_ACodeDiffThatCannotBeReadIsANote(t *testing.T) {
	reader := &fakeReviewReader{profile: codeProfile(), codeErr: errors.New("git blew up")}
	client := &fakeReviewClient{reply: oneReviewFinding}
	run := report.Run{}

	got := reviewFindings(context.Background(), testEnv(), reader, client,
		report.Configure(nil), &run, "")

	require.Nil(t, got)
	require.Len(t, run.Notes, 1)
	require.Contains(t, run.Notes[0], "could not read the code diff")
	require.False(t, client.called, "with no diff to review the model is not called")
}

func TestReviewFindings_AModelErrorIsANoteNotAFinding(t *testing.T) {
	reader := &fakeReviewReader{profile: codeProfile(), codeFiles: reviewCodeFiles()}
	client := &fakeReviewClient{err: errors.New("the provider timed out")}
	run := report.Run{}

	got := reviewFindings(context.Background(), testEnv(), reader, client,
		report.Configure(nil), &run, "")

	require.Empty(t, got, "a call that could not complete reaches no verdict")
	require.Len(t, run.Notes, 1)
	require.Contains(t, run.Notes[0], "could not complete")
}

func TestProfileTouchesCode(t *testing.T) {
	t.Parallel()
	surfaces := []struct {
		surface change.Surface
		want    bool
	}{
		{change.SurfaceCode, true},
		{change.SurfaceAuth, true},
		{change.SurfaceSchema, true},
		{change.SurfaceDocs, false},
		{change.SurfaceConfig, false},
	}
	for _, s := range surfaces {
		p := &change.Profile{Facts: []change.Fact{{Path: "x", Surface: s.surface}}}
		require.Equalf(t, s.want, profileTouchesCode(p), "surface %q", s.surface)
	}
	require.False(t, profileTouchesCode(nil), "a nil profile touches no code")
}

// TestReviewFindings_FiresEndToEndThroughTheRealClient is the collector-level
// proof that the reviewer actually fires: a key in the environment is resolved
// by reviewClient into the real ProviderClient, reviewFindings drives it against
// a change carrying a planted bug, the client posts over real HTTP to a fake
// provider, and the model's answer comes back as a finding in run.Findings. The
// only fake is the provider itself; the key resolution, the client, the guarded
// HTTP path, the parse and the mapping are all the shipping code.
func TestReviewFindings_FiresEndToEndThroughTheRealClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/messages", r.URL.Path)
		require.Contains(t, r.Header.Get("x-api-key"), "sk-ant")
		body, _ := json.Marshal(map[string]any{
			"content": []map[string]string{{"type": "text", "text": planted}},
		})
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	// A real Env whose environment carries a key and points the provider at the
	// fake server. reviewClient resolves this exactly as af ci does.
	e := &Env{
		Getenv: func(k string) string {
			return map[string]string{
				"ANTHROPIC_API_KEY":  "sk-ant-e2e",
				"ANTHROPIC_BASE_URL": srv.URL,
			}[k]
		},
		WorkDir: t.TempDir(), Ring: noRing{}, Clock: clock.New(),
	}
	client := reviewClient(context.Background(), e)
	require.NotNil(t, client, "a key in the environment resolves into a real client")

	reader := &fakeReviewReader{profile: codeProfile(), codeFiles: []change.File{{
		Path: "app/orders.go", Status: change.StatusModified, Added: 1,
		AddedLines: []change.AddedLine{{N: 7, Text: "return items[len(items)]"}},
	}}}
	run := report.Run{}

	got := reviewFindings(context.Background(), e, reader, client, report.Configure(nil), &run, "")

	require.Len(t, got, 1, "the planted bug comes back as a finding through the whole real path")
	require.Equal(t, "review.correctness", got[0].Rule)
	require.Equal(t, "app/orders.go:7", got[0].Where)
	require.Equal(t, report.LevelWarn, got[0].Level)
}

const planted = `[{"category":"correctness","severity":"high","file":"app/orders.go","line":7,` +
	`"title":"index out of range on the last element","explanation":"items[len(items)] is one past the end",` +
	`"suggested_fix":"use items[len(items)-1]"}]`

func TestReviewClient_ResolvesAKeyIntoAClientAndIsNilWithout(t *testing.T) {
	t.Parallel()
	// deadRing models a machine with no keyring, so the only key is the one the
	// environment provides. With a key, reviewClient returns a real client; with
	// none, it returns nil, which is the collector's skip signal.
	withKey := &Env{
		Getenv:  func(k string) string { return map[string]string{"ANTHROPIC_API_KEY": "sk-ant-test"}[k] },
		WorkDir: t.TempDir(), Ring: noRing{}, Clock: clock.New(),
	}
	require.NotNil(t, reviewClient(context.Background(), withKey),
		"a resolvable key produces a client the collector can call")

	noKey := &Env{
		Getenv:  func(string) string { return "" },
		WorkDir: t.TempDir(), Ring: noRing{}, Clock: clock.New(),
	}
	require.Nil(t, reviewClient(context.Background(), noKey),
		"no key resolves to a nil client, which the collector turns into an honest skip note")
}
