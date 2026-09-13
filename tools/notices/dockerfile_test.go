package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func dockerfile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "image.Dockerfile")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

const installStage = `
FROM node:26-alpine AS deps
WORKDIR /app
COPY web/package.json web/package-lock.json ./
# A comment between instructions, and one inside a continued RUN below.
COPY web/apps/api/package.json ./apps/api/
RUN npm ci --omit=dev --ignore-scripts \
      # the scoping is the point
      --workspace @antifailure/api --include-workspace-root
RUN test ! -d node_modules/next || exit 1

FROM node:26-alpine AS runtime
COPY --from=deps /app/node_modules ./node_modules
RUN npm ci --this-is-another-stage
`

func TestTheInstallStageIsReadWithItsFlagsExactly(t *testing.T) {
	st, err := parseStage(dockerfile(t, installStage), "deps")
	if err != nil {
		t.Fatalf("parseStage: %v", err)
	}
	var got []string
	for _, s := range st.steps {
		got = append(got, s.kind+" "+strings.Join(s.args, " "))
	}
	want := []string{
		"workdir /app",
		"copy web/package.json web/package-lock.json ./",
		"copy web/apps/api/package.json ./apps/api/",
		"npm --omit=dev --ignore-scripts --workspace @antifailure/api --include-workspace-root",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("steps:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestAStageWithNoInstallIsRefused(t *testing.T) {
	path := dockerfile(t, "FROM node:26-alpine AS deps\nWORKDIR /app\nCOPY web/package.json ./\n")
	if _, err := parseStage(path, "deps"); err == nil || !strings.Contains(err.Error(), "npm ci 0 times") {
		t.Fatalf("a stage that installs nothing was read as an install: %v", err)
	}
}

func TestAStageThatIsNotThereIsRefused(t *testing.T) {
	if _, err := parseStage(dockerfile(t, installStage), "eedeps"); err == nil ||
		!strings.Contains(err.Error(), "no stage called eedeps") {
		t.Fatalf("a missing stage was not refused: %v", err)
	}
}

func TestACopyFromAnotherStageCannotBeReplayedAndIsRefused(t *testing.T) {
	if _, err := parseStage(dockerfile(t, installStage), "runtime"); err == nil ||
		!strings.Contains(err.Error(), "copies from another stage") {
		t.Fatalf("a stage copying from another stage was replayed: %v", err)
	}
}

// The real files, so a Dockerfile that stops matching the shape this reads
// fails here rather than in a generator run somebody reads as npm's fault.
func TestTheRealImagesInstallStagesAreReadable(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, c := range []struct{ file, stage, flag string }{
		{"deploy/docker/control-plane.Dockerfile", "deps", "--omit=dev"},
		{"deploy/docker/control-plane-enterprise.Dockerfile", "deps", "--omit=dev"},
		{"deploy/docker/control-plane-enterprise.Dockerfile", "eedeps", "--omit=dev"},
		{"deploy/docker/control-plane.Dockerfile", "console", ""},
	} {
		st, err := parseStage(filepath.Join(root, c.file), c.stage)
		if err != nil {
			t.Errorf("%s %s: %v", c.file, c.stage, err)
			continue
		}
		for _, s := range st.steps {
			if s.kind == "npm" && c.flag != "" && !strings.Contains(strings.Join(s.args, " "), c.flag) {
				t.Errorf("%s %s installs without %s: %v", c.file, c.stage, c.flag, s.args)
			}
		}
	}
}

// fakeNPM is a shell script standing in for npm: it records its arguments and
// the directory it ran in, and exits with the code the test chose.
func fakeNPM(t *testing.T, exit int) (bin, record string) {
	t.Helper()
	dir := t.TempDir()
	record = filepath.Join(dir, "record")
	bin = filepath.Join(dir, "npm")
	script := "#!/bin/sh\nprintf '%s\\n' \"$PWD\" \"$*\" > '" + record + "'\necho 'npm error network request failed'\nexit " +
		strings.TrimSpace(strings.Repeat("1", exit)+strings.Repeat("0", 1-exit)) + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, record
}

func TestReplayLaysOutTheImageAndRunsItsOwnInstall(t *testing.T) {
	root := t.TempDir()
	for _, f := range []string{"web/package.json", "web/package-lock.json", "web/apps/api/package.json"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.Dir(f)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, f), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	st, err := parseStage(dockerfile(t, installStage), "deps")
	if err != nil {
		t.Fatal(err)
	}
	npm, record := fakeNPM(t, 0)
	tmp := t.TempDir()
	dir, err := replay(root, st, tmp, npm, gitTracked)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if want := filepath.Join(tmp, "app"); dir != want {
		t.Errorf("the install ran in %s, want %s", dir, want)
	}
	for _, f := range []string{"app/package.json", "app/package-lock.json", "app/apps/api/package.json"} {
		if _, err := os.Stat(filepath.Join(tmp, f)); err != nil {
			t.Errorf("%s was not laid out where the image puts it: %v", f, err)
		}
	}
	body, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("npm was not run: %v", err)
	}
	args := strings.Split(strings.TrimSpace(string(body)), "\n")[1]
	for _, want := range []string{"ci --omit=dev --ignore-scripts --workspace @antifailure/api --include-workspace-root", "--prefer-offline"} {
		if !strings.Contains(args, want) {
			t.Errorf("npm ran with %q, which lacks %q", args, want)
		}
	}
}

func TestAnInstallThatFailsIsAFailureThatSaysWhy(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "web", "apps", "api"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"web/package.json", "web/package-lock.json", "web/apps/api/package.json"} {
		if err := os.WriteFile(filepath.Join(root, f), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	st, err := parseStage(dockerfile(t, installStage), "deps")
	if err != nil {
		t.Fatal(err)
	}
	npm, _ := fakeNPM(t, 1)
	_, err = replay(root, st, t.TempDir(), npm, gitTracked)
	if err == nil {
		t.Fatal("a failed install was reported as an install")
	}
	for _, want := range []string{"npm ci", "stage deps", "network request failed", "cold cache"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the failure does not say %q: %v", want, err)
		}
	}
}

func TestNoNpmIsAFailureThatSaysSoRatherThanALockfileRead(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "web", "apps", "api"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"web/package.json", "web/package-lock.json", "web/apps/api/package.json"} {
		if err := os.WriteFile(filepath.Join(root, f), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	st, err := parseStage(dockerfile(t, installStage), "deps")
	if err != nil {
		t.Fatal(err)
	}
	_, err = replay(root, st, t.TempDir(), filepath.Join(t.TempDir(), "no-such-npm"), gitTracked)
	if err == nil || !strings.Contains(err.Error(), "npm is not on the path") {
		t.Fatalf("a missing npm was not named: %v", err)
	}
}
