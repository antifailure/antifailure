package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/cli"
	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/secrets"
)

// deadRing is a machine with no credential store: a Linux server without
// libsecret, a container, and every platform this has no implementation for.
//
// Worth having a test for rather than one that only exists on somebody else's
// laptop. The working in-memory ring is memoryRing, in provider_test.go.
type deadRing struct{}

func (deadRing) Get(string, string) (string, error) {
	return "", secrets.ErrKeyringUnavailable
}
func (deadRing) Set(string, string, string) error { return secrets.ErrKeyringUnavailable }
func (deadRing) Delete(string, string) error      { return secrets.ErrKeyringUnavailable }

// runModelCLI is runCLI with a substituted keyring and an input stream.
func runModelCLI(
	t *testing.T, dir string, env map[string]string,
	ring secrets.Keyring, stdin string, args ...string,
) result {
	t.Helper()
	var out, errW bytes.Buffer
	code := cli.Execute(context.Background(), args, cli.Options{
		Stdout:  &out,
		Stderr:  &errW,
		Stdin:   strings.NewReader(stdin),
		Getenv:  func(k string) string { return env[k] },
		Clock:   clock.NewFake(epoch),
		WorkDir: dir,
		Keyring: ring,
	})
	return result{code: code, stdout: out.String(), stderr: errW.String()}
}

// Running with no key is a supported mode and the command has to say so. A
// message that read as a failure would tell somebody their product is broken
// when it works.
func TestModelShow_NoKeyIsNotAFailure(t *testing.T) {
	t.Parallel()
	res := runModelCLI(t, t.TempDir(), nil, newMemoryRing(), "", "model", "show")
	require.Zero(t, res.code)
	require.Contains(t, res.stdout, "deterministic planner")
	require.Contains(t, res.stdout, "supported mode")
	// Where a key can go, rather than only that there is not one. The place a
	// key most often belongs is the .env that does not exist yet.
	require.Contains(t, res.stdout, "this shell's environment")
	require.Contains(t, res.stdout, ".env")
	require.Contains(t, res.stdout, "the system keyring")
}

// The whole point of the command, and the guarantee under everything else:
// nothing it can print is the key.
func TestModelShow_NeverPrintsTheKey(t *testing.T) {
	t.Parallel()
	// Assembled rather than written out: scanrepo refuses a literal its own
	// detector reads as a live Anthropic key.
	key := strings.Join([]string{"sk", "test", "thisisthesecretvalue"}, "-")
	dir := t.TempDir()

	text := runModelCLI(t, dir, map[string]string{"ANTHROPIC_API_KEY": key},
		newMemoryRing(), "", "model", "show")
	require.Zero(t, text.code)
	require.NotContains(t, text.stdout, key)
	require.NotContains(t, text.stderr, key)
	// Not a suffix either. A partial key on a screen is still a partial key,
	// and there is nothing here that needs one.
	require.NotContains(t, text.stdout, key[len(key)-8:])
	require.Contains(t, text.stdout, "anthropic")
	require.Contains(t, text.stdout, "this shell's environment")

	// The JSON shape is what gets piped into a log aggregator, so it is checked
	// as a whole rather than field by field: a key field that exists is a key
	// field that ends up somewhere it was not meant to.
	js := runModelCLI(t, dir, map[string]string{"ANTHROPIC_API_KEY": key},
		newMemoryRing(), "", "model", "show", "-o", "json")
	require.Zero(t, js.code)
	require.NotContains(t, js.stdout, key)
	var got cli.ModelShowJSON
	require.NoError(t, json.Unmarshal([]byte(js.stdout), &got))
	require.True(t, got.Configured)
	require.Equal(t, "anthropic", got.Provider)
	require.Equal(t, "model", got.Planner)
	require.NotEmpty(t, got.Fingerprint)
	require.NotContains(t, key, got.Fingerprint,
		"the fingerprint must not be a substring of the key it identifies")
}

// The single most likely first-use confusion: a key exported months ago, a
// freshly stored one, and every run silently using the old one. Nothing is
// broken and nothing says anything, which is the worst combination.
func TestModelSet_SaysWhenTheStoredKeyIsShadowed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ring := newMemoryRing()

	res := runModelCLI(t, dir,
		map[string]string{"ANTHROPIC_API_KEY": "sk-exported-long-ago"},
		ring, "sk-the-new-one\n", "model", "set", "anthropic", "--stdin")
	require.Zero(t, res.code)
	require.Contains(t, res.stdout, "Stored the anthropic key in the system keyring")
	require.Contains(t, res.stdout, "It is not the key runs will use")
	require.Contains(t, res.stdout, "this shell's environment")
	require.NotContains(t, res.stdout, "sk-the-new-one")

	// And show says the same thing, from the other direction.
	shown := runModelCLI(t, dir,
		map[string]string{"ANTHROPIC_API_KEY": "sk-exported-long-ago"},
		ring, "", "model", "show")
	require.Contains(t, shown.stdout,
		"ANTHROPIC_API_KEY is also set in the system keyring")
}

func TestModelSet_StoresAndResolves(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ring := newMemoryRing()

	set := runModelCLI(t, dir, nil, ring, "sk-stored-key\n",
		"model", "set", "anthropic", "--stdin")
	require.Zero(t, set.code)
	require.NotContains(t, set.stdout, "sk-stored-key")
	require.Contains(t, set.stdout, "af model test")

	shown := runModelCLI(t, dir, nil, ring, "", "model", "show", "-o", "json")
	var got cli.ModelShowJSON
	require.NoError(t, json.Unmarshal([]byte(shown.stdout), &got))
	require.Equal(t, "the system keyring", got.Source)
	require.Empty(t, got.VerifiedAt, "a key that was never tested must not read as verified")
}

// A key can be given three ways and none of them may put it in the argument
// vector, because an argument is in the shell history, in ps, and in any
// recording of the terminal.
func TestModelSet_TheKeyIsNeverAnArgument(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ring := newMemoryRing()

	// --from-env is the third way in.
	res := runModelCLI(t, dir, map[string]string{"MY_KEY": "sk-from-a-variable"},
		ring, "", "model", "set", "anthropic", "--from-env", "MY_KEY")
	require.Zero(t, res.code)
	require.Equal(t, "sk-from-a-variable",
		ring.items[secrets.DefaultKeyringService+"/ANTHROPIC_API_KEY"])

	// There is no flag that takes one, and cobra rejects an unknown flag as a
	// usage error rather than quietly ignoring it.
	flag := runModelCLI(t, dir, nil, ring, "", "model", "set", "anthropic", "--key", "sk-oops")
	require.NotZero(t, flag.code)
	require.Contains(t, flag.stderr+flag.stdout, "unknown flag")
}

func TestModelSet_RejectsAnUnknownProviderBeforeReadingAKey(t *testing.T) {
	t.Parallel()
	// The stdin here would be consumed if the provider were checked after the
	// key was read, which is the ordering that makes somebody paste a secret
	// and then be told the command was wrong.
	res := runModelCLI(t, t.TempDir(), nil, newMemoryRing(), "sk-should-not-be-read\n",
		"model", "set", "gemini", "--stdin")
	require.NotZero(t, res.code)
	require.Contains(t, res.stderr+res.stdout, "anthropic, openai")
	require.NotContains(t, res.stderr+res.stdout, "sk-should-not-be-read")
}

// A machine with no keyring and no passphrase has nowhere to write a key, and
// that is reported rather than written to a file that only looks encrypted.
func TestModelSet_NowhereToStoreIsAnError(t *testing.T) {
	t.Parallel()
	res := runModelCLI(t, t.TempDir(), nil, deadRing{},
		"sk-nowhere\n", "model", "set", "anthropic", "--stdin")
	require.NotZero(t, res.code)
	require.Contains(t, res.stderr, "AF-SEC-004")
	require.NotContains(t, res.stderr, "sk-nowhere")
}

// Somebody removing a leaked key needs to be told that their shell still has
// it far more than they need to be told the keyring no longer does.
func TestModelRemove_NamesWhatStillSuppliesAKey(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ring := newMemoryRing()
	require.NoError(t, ring.Set(secrets.DefaultKeyringService, "ANTHROPIC_API_KEY", "sk-in-ring"))

	res := runModelCLI(t, dir, map[string]string{"ANTHROPIC_API_KEY": "sk-still-exported"},
		ring, "", "model", "rm", "anthropic")
	require.Zero(t, res.code)
	require.Contains(t, res.stdout, "Removed the anthropic key from the system keyring")
	require.Contains(t, res.stdout,
		"ANTHROPIC_API_KEY is still set in this shell's environment")
	require.Contains(t, res.stdout, "This command cannot reach there")
	require.NotContains(t, res.stdout, "sk-still-exported")
}

// A retry after a timeout must not report failure for reaching the state the
// caller asked for.
func TestModelRemove_MissingIsNotAnError(t *testing.T) {
	t.Parallel()
	res := runModelCLI(t, t.TempDir(), nil, newMemoryRing(), "", "model", "rm", "openai")
	require.Zero(t, res.code)
	require.Contains(t, res.stdout, "There was no stored openai key")
}

// ---------------------------------------------------------------------------
// af model test
// ---------------------------------------------------------------------------

func TestModelTest_SuccessRecordsTheVerification(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"type":"message","content":[{"type":"text","text":"hi"}]}`))
	}))
	t.Cleanup(srv.Close)

	env := map[string]string{
		"ANTHROPIC_API_KEY":  "sk-ant-verified",
		"ANTHROPIC_BASE_URL": srv.URL,
	}

	res := runModelCLI(t, dir, env, newMemoryRing(), "", "model", "test")
	require.Zero(t, res.code, res.stderr)
	require.Contains(t, res.stdout, "The key works")
	require.NotContains(t, res.stdout, "sk-ant-verified")

	// The verification reaches show and doctor, which is the only reason to
	// write it down at all.
	shown := runModelCLI(t, dir, env, newMemoryRing(), "", "model", "show", "-o", "json")
	var got cli.ModelShowJSON
	require.NoError(t, json.Unmarshal([]byte(shown.stdout), &got))
	require.Equal(t, epoch.UTC().Format("2006-01-02T15:04:05Z"), got.VerifiedAt)
	require.True(t, got.Custom)

	doc := runModelCLI(t, dir, env, newMemoryRing(), "", "doctor", "-o", "json")
	require.Contains(t, doc.stdout, "verified 2026-01-01")
	require.NotContains(t, doc.stdout, "sk-ant-verified")
}

// The failure that separates a useful command from a useless one: each of these
// has a different fix, and being told only that the call failed sends somebody
// to the wrong one first.
func TestModelTest_ReportsWhatIsActuallyWrong(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		status   int
		body     string
		code     string
		contains string
	}{
		{
			name:     "a revoked key is a configuration failure nobody should retry",
			status:   401,
			body:     `{"error":{"type":"authentication_error","message":"invalid x-api-key"}}`,
			code:     "AF-AGT-005",
			contains: "af model set anthropic",
		},
		{
			name:     "an empty balance says retrying will not help",
			status:   400,
			body:     `{"error":{"message":"Your credit balance is too low"}}`,
			code:     "AF-AGT-005",
			contains: "Retrying will not help",
		},
		{
			name:     "an outage is retryable and says nothing about the key",
			status:   503,
			body:     `{"error":{"message":"overloaded"}}`,
			code:     "AF-AGT-006",
			contains: "says nothing about the key",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("content-type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			t.Cleanup(srv.Close)

			dir := t.TempDir()
			res := runModelCLI(t, dir, map[string]string{
				"ANTHROPIC_API_KEY":  "sk-ant-failing",
				"ANTHROPIC_BASE_URL": srv.URL,
			}, newMemoryRing(), "", "model", "test")

			require.NotZero(t, res.code)
			require.Contains(t, res.stderr, tc.code)
			require.Contains(t, res.stderr, tc.contains)
			require.NotContains(t, res.stderr, "sk-ant-failing")

			// A failed test must not leave a verification behind.
			_, err := os.Stat(filepath.Join(dir, ".antifailure", "model-verified.json"))
			require.True(t, os.IsNotExist(err))
		})
	}
}

// A revoked key and a provider having a bad afternoon deserve different exit
// codes, because a script reads the exit code and only one of the two is worth
// retrying.
func TestModelTest_ExitCodesSeparateConfigurationFromOutage(t *testing.T) {
	t.Parallel()

	statuses := map[int]int{
		401: 4, // auth
		503: 5, // provider, retryable
	}
	for status, wantCode := range statuses {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"message":"no"}}`))
		}))
		res := runModelCLI(t, t.TempDir(), map[string]string{
			"ANTHROPIC_API_KEY":  "sk",
			"ANTHROPIC_BASE_URL": srv.URL,
		}, newMemoryRing(), "", "model", "test")
		srv.Close()
		require.Equal(t, wantCode, res.code, "status %d", status)
	}
}

// Nothing to test is not a failure. Somebody with no key has a working product
// and telling them their setup failed would be false.
func TestModelTest_NoKeyIsNotAFailure(t *testing.T) {
	t.Parallel()
	res := runModelCLI(t, t.TempDir(), nil, newMemoryRing(), "", "model", "test")
	require.Zero(t, res.code)
	require.Contains(t, res.stdout, "nothing to test")
	require.Contains(t, res.stdout, "deterministic planner")
}

// ---------------------------------------------------------------------------
// af doctor
// ---------------------------------------------------------------------------

// Doctor is what somebody runs first, and until this existed it said nothing at
// all about the model, which left the most confusing thing about the product
// undiscoverable: whether a run will read pages with a model or fall back.
func TestDoctor_ReportsTheModelKey(t *testing.T) {
	t.Parallel()

	t.Run("no key passes and says the mode is supported", func(t *testing.T) {
		t.Parallel()
		res := runModelCLI(t, t.TempDir(), nil, newMemoryRing(), "", "doctor", "-o", "json")
		var report cli.DoctorReport
		require.NoError(t, json.Unmarshal([]byte(res.stdout), &report))

		check := findCheck(t, report, "Model key")
		require.Equal(t, cli.CheckPass, check.Status)
		require.Contains(t, check.Detail, "deterministic planner")
		require.Contains(t, check.Remediation, "supported")
	})

	t.Run("an unverified key warns rather than passing", func(t *testing.T) {
		t.Parallel()
		res := runModelCLI(t, t.TempDir(),
			map[string]string{"ANTHROPIC_API_KEY": "sk-never-tested", "AF_MODEL": "claude-opus-5"},
			newMemoryRing(), "", "doctor", "-o", "json")
		require.NotContains(t, res.stdout, "sk-never-tested")

		var report cli.DoctorReport
		require.NoError(t, json.Unmarshal([]byte(res.stdout), &report))
		check := findCheck(t, report, "Model key")
		// A key that is set and revoked is indistinguishable from a working one
		// without a call, and the difference costs somebody a whole run.
		require.Equal(t, cli.CheckWarn, check.Status)
		require.Contains(t, check.Detail, "anthropic/claude-opus-5")
		require.Contains(t, check.Detail, "never verified")
		require.Contains(t, check.Remediation, "af model test")
	})

	t.Run("a custom endpoint is named", func(t *testing.T) {
		t.Parallel()
		res := runModelCLI(t, t.TempDir(), map[string]string{
			"ANTHROPIC_API_KEY":  "sk",
			"ANTHROPIC_BASE_URL": "http://127.0.0.1:11434",
		}, newMemoryRing(), "", "doctor", "-o", "json")

		var report cli.DoctorReport
		require.NoError(t, json.Unmarshal([]byte(res.stdout), &report))
		require.Contains(t, findCheck(t, report, "Model key").Detail, "http://127.0.0.1:11434")
	})
}

func findCheck(t *testing.T, report cli.DoctorReport, name string) cli.CheckResult {
	t.Helper()
	for _, c := range report.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no check named %q in %d checks", name, len(report.Checks))
	return cli.CheckResult{}
}
