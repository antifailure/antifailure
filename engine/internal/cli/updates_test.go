package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDoctorWarningsDoNotClaimReadiness(t *testing.T) {
	var output bytes.Buffer
	env := &Env{Out: NewOutput(&output, &output)}
	err := renderDoctor(env, DoctorReport{OK: true, Checks: []CheckResult{{Name: "CLI version", Status: CheckWarn, Detail: "could not check the latest release", Remediation: "Try again when connected."}}})
	if err != nil {
		t.Fatalf("an unavailable version check should warn, not fail: %v", err)
	}
	if !strings.Contains(output.String(), "Checks completed with warnings") || strings.Contains(output.String(), "This machine can run") {
		t.Fatalf("warning was rendered as readiness: %s", output.String())
	}
}

func TestReleaseCheck(t *testing.T) {
	cases := []struct {
		name, current, body string
		code                int
		want                CheckStatus
	}{
		{"stale", "v0.1.1", `{"tag_name":"v1.1.1"}`, 200, CheckFail},
		{"minor", "v1.2.9", `{"tag_name":"v1.10.0"}`, 200, CheckFail},
		{"patch", "v1.1.0", `{"tag_name":"v1.1.1"}`, 200, CheckFail},
		{"current", "v1.1.1", `{"tag_name":"v1.1.1"}`, 200, CheckPass},
		{"ahead", "v2.0.0", `{"tag_name":"v1.1.1"}`, 200, CheckWarn},
		{"unstamped", "dev", `{"tag_name":"v1.1.1"}`, 200, CheckWarn},
		{"denied", "v1.1.1", `{"tag_name":"v1.1.1"}`, 403, CheckWarn},
		{"malformed", "v1.1.1", `broken`, 200, CheckWarn},
		{"missing", "v1.1.1", `{}`, 200, CheckWarn},
		{"draft", "v1.1.1", `{"tag_name":"v1.1.1","draft":true}`, 200, CheckWarn},
		{"prerelease", "v1.1.1", `{"tag_name":"v1.1.1","prerelease":true}`, 200, CheckWarn},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.code)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer s.Close()
			r := releaseCheck(context.Background(), tc.current, s.URL, s.Client())
			if r.Status != tc.want {
				t.Fatalf("status %s, want %s: %s", r.Status, tc.want, r.Detail)
			}
		})
	}
}

func TestReleaseCheckOfflineNamesUncertainty(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := releaseCheck(ctx, "v1.1.1", "http://127.0.0.1:1", &http.Client{Timeout: time.Millisecond})
	if r.Status != CheckWarn || !strings.Contains(r.Detail, "could not check") {
		t.Fatalf("offline release check claimed certainty: %+v", r)
	}
}

func TestDoctorManifest(t *testing.T) {
	for _, tc := range []struct {
		name, content string
		want          CheckStatus
	}{
		{"absent", "", CheckWarn},
		{"invalid", "version: invalid\n", CheckFail},
		{"valid", "version: 1\nname: fixture\nservices:\n  - name: web\n    kind: web\n    path: .\n    port: 3000\n    build:\n      strategy: dockerfile\n      dockerfile: Dockerfile\n", CheckPass},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.name == "valid" {
				if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.content != "" {
				if err := os.WriteFile(filepath.Join(dir, "antifailure.yaml"), []byte(tc.content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			r := checkProjectManifest(context.Background(), &Env{WorkDir: dir}, nil)
			if r.Status != tc.want {
				t.Fatalf("status %s, want %s: %s", r.Status, tc.want, r.Detail)
			}
		})
	}
}

// TestCurrentReleaseIsNotToldToUpdate. Every failing path here carries "run
// 'af update'", and the passing path inherited it, so a machine already on the
// newest release was advised to install it. Text mode prints remediations for
// problems only, which is why it was invisible there and shipped in the JSON
// that every script and support bundle reads.
func TestCurrentReleaseIsNotToldToUpdate(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v1.1.1"}`))
	}))
	defer s.Close()
	r := releaseCheck(context.Background(), "v1.1.1", s.URL, s.Client())
	if r.Status != CheckPass {
		t.Fatalf("the current release did not pass: %+v", r)
	}
	if strings.Contains(r.Remediation, "af update") {
		t.Fatalf("a current installation was told to update: %q", r.Remediation)
	}
	if r.Remediation == "" {
		t.Fatal("the check names no remediation, which doctor requires of every check")
	}
}
