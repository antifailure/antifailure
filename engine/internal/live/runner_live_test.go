package live

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// The two halves of the live channel are tested apart everywhere else: the
// engine's own tests dial its endpoint with Go, and the runner's tests listen
// with Node. The connection the watch view actually depends on, the runner's
// real socketSink in Node reaching the endpoint the engine really opened, was
// in neither, so a Windows pipe Node could not open would have passed both.
// This runs that exact pairing.
func TestTheRunnersRealSinkReachesTheEndpointTheEngineOpens(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil && os.Getenv("AF_REQUIRE_RUNNER") == "" {
		t.Skip("node is not installed; set AF_REQUIRE_RUNNER to make this a failure")
	}
	sink, err := filepath.Abs(filepath.Join("..", "..", "..", "runner", "src", "live.ts"))
	if err != nil {
		t.Fatalf("locating the runner: %v", err)
	}
	if _, err := os.Stat(sink); err != nil {
		t.Fatalf("the runner's live sink is not where this test expects it: %v", err)
	}

	srv := listenForTest(t)
	hub := NewHub()
	go srv.Serve(hub)

	// The engine's address goes to Node exactly as the job document carries
	// it, through argv rather than spliced into the script, so a pipe name's
	// backslashes arrive unescaped.
	const script = `
import { pathToFileURL } from 'node:url';
const { socketSink } = await import(pathToFileURL(process.argv[1]).href);
const sink = socketSink(process.argv[2]);
sink.hello('r');
sink.agent({ id: 'a', surface: 'web', persona: 'owner', workflow: 'signup' }, 'live');
sink.frame('a', { mime: 'image/jpeg', w: 640, h: 480, b64: 'aW1n' });
await new Promise((r) => setTimeout(r, 500));
await sink.close();
`
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", "--experimental-strip-types", "--no-warnings",
		"--input-type=module", "-e", script, sink, srv.Path())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the runner's sink failed: %v\n%s", err, out)
	}

	// The sink never throws: an endpoint it cannot reach is nobody watching.
	// So Node exiting cleanly proves nothing, and only the frame arriving in
	// the hub does.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		snap := hub.Snapshot()
		if len(snap.Agents) == 1 && snap.Agents[0].Frame != nil && snap.Agents[0].Frame.B64 == "aW1n" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the runner's sink ran against %s and nothing reached the hub", srv.Path())
}
