package detect_test

import (
	"context"
	"testing"
	"testing/fstest"
	"time"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/detect"
)

// FuzzAnalyzers feeds arbitrary bytes to every file an analyzer knows how to
// read.
//
// The repository is untrusted input in the same sense a network packet is: it
// arrives from a pull request the engine did not write, and a panic while
// reading it would crash the engine on a hostile or merely unusual project. No
// analyzer may panic on any input, and none may hang.
func FuzzAnalyzers(f *testing.F) {
	f.Add(`{"name":"a","dependencies":{"next":"1"}}`, "FROM alpine\nEXPOSE 80\n", "services:\n  web:\n    build: .\n")
	f.Add("", "", "")
	f.Add("null", "FROM", "services:")
	f.Add(`{"workspaces":{"packages":["a/*"]}}`, "CMD [", "services:\n  a:\n    ports:\n      - \":::\"\n")
	f.Add(`{"scripts":{"start":"next start --port 99999999"}}`, "EXPOSE 99999999999", "ports:\n - -1\n")
	f.Add(`{"engines":{"node":"\u0000"}}`, "FROM \\\n", "services:\n  a:\n    depends_on:\n")

	f.Fuzz(func(t *testing.T, pkg, dockerfile, compose string) {
		fsys := fstest.MapFS{
			"package.json":       &fstest.MapFile{Data: []byte(pkg)},
			"Dockerfile":         &fstest.MapFile{Data: []byte(dockerfile)},
			"docker-compose.yml": &fstest.MapFile{Data: []byte(compose)},
			// The other analyzers read these, so they get the same bytes.
			".env.example":         &fstest.MapFile{Data: []byte(pkg)},
			"Procfile":             &fstest.MapFile{Data: []byte(compose)},
			"requirements.txt":     &fstest.MapFile{Data: []byte(pkg)},
			"Gemfile":              &fstest.MapFile{Data: []byte(pkg)},
			"go.mod":               &fstest.MapFile{Data: []byte(pkg)},
			"vercel.json":          &fstest.MapFile{Data: []byte(pkg)},
			"turbo.json":           &fstest.MapFile{Data: []byte(pkg)},
			"prisma/schema.prisma": &fstest.MapFile{Data: []byte(dockerfile)},
			"pnpm-workspace.yaml":  &fstest.MapFile{Data: []byte(compose)},
		}
		res, err := detect.Run(context.Background(), fsys, "fuzz", detect.Options{
			Clock:  clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)),
			Budget: 5 * time.Second,
		})
		if err != nil {
			t.Fatalf("Run returned an error, which it never should: %v", err)
		}
		if res.Draft == nil {
			t.Fatal("Run returned no draft")
		}
		// Whatever came out has to be a manifest the rest of the engine can
		// hold, so the invariants the merger promises must survive any input.
		if res.Draft.Egress == nil || res.Draft.Database == nil {
			t.Fatal("the draft is missing sections the merger always fills")
		}
		for _, s := range res.Draft.Services {
			if s.Name == "" {
				t.Fatal("a service with no name reached the draft")
			}
			if s.Port < 0 || s.Port > 65535 {
				t.Fatalf("service %q has an impossible port %d", s.Name, s.Port)
			}
		}
		for _, r := range res.Draft.Egress.Rules {
			if r.Host == "" || r.Mode == "" {
				t.Fatal("an egress rule with no host or mode reached the draft")
			}
		}
	})
}
