package ghevent_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/ghevent"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The payload GitHub sends, trimmed to the fields the decision turns on.
func payload(headRepo string, labels ...string) []byte {
	head := "null"
	if headRepo != "" {
		head = `{"full_name":"` + headRepo + `","fork":true}`
	}
	quoted := ""
	for i, l := range labels {
		if i > 0 {
			quoted += ","
		}
		quoted += `{"name":"` + l + `"}`
	}
	return []byte(`{"action":"opened","number":12,
	  "repository":{"full_name":"acme/shop"},
	  "pull_request":{"number":12,"labels":[` + quoted + `],
	    "head":{"sha":"c0ffee","ref":"topic","repo":` + head + `},
	    "base":{"ref":"main","repo":{"full_name":"acme/shop"}}}}`)
}

func TestFromFork(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		head string
		fork bool
	}{
		"a branch on the repository":     {"acme/shop", false},
		"the same name in another case":  {"Acme/Shop", false},
		"somebody else's copy":           {"stranger/shop", true},
		"a fork that has been deleted":   {"", true},
		"a fork under a similar account": {"acme-corp/shop", true},
	} {
		t.Run(name, func(t *testing.T) {
			pr, err := ghevent.Parse(payload(tc.head))
			require.NoError(t, err)
			require.Equal(t, tc.fork, pr.FromFork())
		})
	}
}

// The label is a string three other places spell out, so its exact form is
// worth pinning rather than trusting to four copies agreeing.
func TestApproved(t *testing.T) {
	t.Parallel()
	require.Equal(t, "antifailure:allow", ghevent.ApprovalLabel)

	for name, tc := range map[string]struct {
		labels   []string
		approved bool
	}{
		"nothing at all":            {nil, false},
		"the label":                 {[]string{"antifailure:allow"}, true},
		"the label among others":    {[]string{"bug", "antifailure:allow", "p1"}, true},
		"a near miss":               {[]string{"antifailure-allow"}, false},
		"the prefix on its own":     {[]string{"antifailure"}, false},
		"a different case":          {[]string{"Antifailure:Allow"}, true},
		"padded, as GitHub trims":   {[]string{" antifailure:allow "}, true},
		"an unrelated allow label":  {[]string{"allow"}, false},
		"the label on another tool": {[]string{"other:allow"}, false},
	} {
		t.Run(name, func(t *testing.T) {
			pr, err := ghevent.Parse(payload("stranger/shop", tc.labels...))
			require.NoError(t, err)
			require.Equal(t, tc.approved, pr.Approved())
		})
	}
}

func TestDecide(t *testing.T) {
	t.Parallel()
	fork, err := ghevent.Parse(payload("stranger/shop"))
	require.NoError(t, err)
	approved, err := ghevent.Parse(payload("stranger/shop", "antifailure:allow"))
	require.NoError(t, err)
	own, err := ghevent.Parse(payload("acme/shop"))
	require.NoError(t, err)

	for name, tc := range map[string]struct {
		policy  schema.ForkPolicy
		pr      *ghevent.PullRequest
		refused bool
	}{
		"never refuses a fork":               {schema.ForkNever, fork, true},
		"never refuses an approved fork":     {schema.ForkNever, approved, true},
		"never is silent about own branch":   {schema.ForkNever, own, false},
		"label refuses without the label":    {schema.ForkLabel, fork, true},
		"label allows with the label":        {schema.ForkLabel, approved, false},
		"always allows a fork":               {schema.ForkAlways, fork, false},
		"no pull request means no opinion":   {schema.ForkLabel, nil, false},
		"an unknown policy behaves as label": {schema.ForkPolicy("sometimes"), fork, true},
	} {
		t.Run(name, func(t *testing.T) {
			d := ghevent.Decide(tc.policy, tc.pr)
			require.Equal(t, tc.refused, d.Refused)
			if tc.refused {
				require.NotEmpty(t, d.Reason, "a refusal nobody can read is a refusal nobody can act on")
			}
		})
	}
}

// never refusing an APPROVED fork is worth its own sentence, because it is the
// one that looks like a bug and is not. never means never; a maintainer who
// wants the label to mean something sets label.
func TestDecide_NeverIgnoresTheLabel(t *testing.T) {
	t.Parallel()
	approved, err := ghevent.Parse(payload("stranger/shop", "antifailure:allow"))
	require.NoError(t, err)
	d := ghevent.Decide(schema.ForkNever, approved)
	require.True(t, d.Refused)
	require.Contains(t, d.Reason, "never")
}

func TestRead(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "event.json")
	require.NoError(t, os.WriteFile(path, payload("stranger/shop"), 0o644))

	t.Run("a pull request", func(t *testing.T) {
		pr, err := ghevent.Read(func(k string) string {
			return map[string]string{
				"GITHUB_EVENT_NAME": "pull_request", "GITHUB_EVENT_PATH": path,
			}[k]
		})
		require.NoError(t, err)
		require.NotNil(t, pr)
		require.Equal(t, 12, pr.Number)
		require.Equal(t, "stranger/shop", pr.HeadRepository)
		require.Equal(t, "acme/shop", pr.BaseRepository)
	})

	// The three results are distinct states, and this is the pair that a
	// caller must never fold together: "not a pull request" is an allow and
	// "a pull request I could not read" is not.
	t.Run("not a pull request is nil and no error", func(t *testing.T) {
		pr, err := ghevent.Read(func(k string) string {
			return map[string]string{"GITHUB_EVENT_NAME": "push"}[k]
		})
		require.NoError(t, err)
		require.Nil(t, pr)
	})

	t.Run("a pull request with no payload is an error", func(t *testing.T) {
		pr, err := ghevent.Read(func(k string) string {
			return map[string]string{"GITHUB_EVENT_NAME": "pull_request"}[k]
		})
		require.Error(t, err)
		require.Nil(t, pr)
	})

	t.Run("pull_request_target is a pull request", func(t *testing.T) {
		pr, err := ghevent.Read(func(k string) string {
			return map[string]string{
				"GITHUB_EVENT_NAME": "pull_request_target", "GITHUB_EVENT_PATH": path,
			}[k]
		})
		require.NoError(t, err)
		require.NotNil(t, pr)
	})
}

func TestParse_RefusesWhatItCannotDecideFrom(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]string{
		"not JSON":           `{`,
		"no pull request":    `{"zen":"hello","repository":{"full_name":"acme/shop"}}`,
		"no base repository": `{"pull_request":{"number":1}}`,
	} {
		t.Run(name, func(t *testing.T) {
			pr, err := ghevent.Parse([]byte(body))
			require.Error(t, err)
			require.Nil(t, pr)
		})
	}
}
